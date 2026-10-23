package handler

import (
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"

	"ci-platform/internal/executor"
	"ci-platform/internal/model"
	"ci-platform/internal/pkg"
	"ci-platform/internal/service"
)

// WSHandler 提供构建日志的实时推送。
type WSHandler struct {
	hub       *executor.Hub
	builds    *service.BuildService
	logBuffer int
	upgrader  websocket.Upgrader
}

// NewWSHandler 构造 WebSocket 处理器。
// origins 为空或包含 * 时允许任意来源（兼容 Vite 代理）；生产可配置精确来源。
func NewWSHandler(hub *executor.Hub, builds *service.BuildService, logBuffer int, origins []string) *WSHandler {
	if logBuffer <= 0 {
		logBuffer = 512
	}
	allowAll := len(origins) == 0
	allowed := make(map[string]bool, len(origins))
	for _, origin := range origins {
		origin = strings.TrimSpace(origin)
		if origin == "*" {
			allowAll = true
		}
		if origin != "" {
			allowed[origin] = true
		}
	}
	return &WSHandler{
		hub:       hub,
		builds:    builds,
		logBuffer: logBuffer,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
			CheckOrigin: func(r *http.Request) bool {
				origin := strings.TrimSpace(r.Header.Get("Origin"))
				// 非浏览器客户端通常不带 Origin，交给 token 鉴权。
				if origin == "" {
					return true
				}
				if allowAll {
					return true
				}
				if sameHostOrigin(origin, r.Host) {
					return true
				}
				return allowed[origin]
			},
		},
	}
}

// sameHostOrigin 判断 Origin 是否与请求 Host 同源（含端口）。
func sameHostOrigin(origin, host string) bool {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return false
	}
	return strings.EqualFold(parsed.Host, host)
}

// ServeLog GET /ws/pipelines/{pid}/builds/{bid}/logs
func (h *WSHandler) ServeLog(w http.ResponseWriter, r *http.Request) {
	pipelineID := chi.URLParam(r, "pid")
	buildID := chi.URLParam(r, "bid")

	// 构建不存在直接拒绝，避免建立无意义的长连接。
	if _, err := h.builds.Get(r.Context(), pipelineID, buildID); err != nil {
		pkg.Fail(w, err)
		return
	}

	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	key := model.LogKey(pipelineID, buildID)
	// 先声明再赋值，使闭包可以引用 client 自身（短变量声明内无法自引用）。
	var client *wsClient
	client = newWSClient(key, conn, h.logBuffer, func() { h.hub.Unregister(key, client) })
	h.hub.Register(key, client)

	go client.writePump()
	go client.readPump()
}

// wsClient 实现 executor.Subscriber：每个连接只有 writePump 一个 goroutine 写，
// 避免 gorilla/websocket 的并发写限制。
type wsClient struct {
	key     string
	conn    *websocket.Conn
	send    chan []byte
	closed  chan struct{}
	once    sync.Once
	onClose func()
}

func newWSClient(key string, conn *websocket.Conn, buffer int, onClose func()) *wsClient {
	return &wsClient{
		key:     key,
		conn:    conn,
		send:    make(chan []byte, buffer),
		closed:  make(chan struct{}),
		onClose: onClose,
	}
}

// Enqueue 非阻塞投递日志帧；必须拷贝，因为上游 writer 会复用底层缓冲区。
func (c *wsClient) Enqueue(data []byte) bool {
	select {
	case <-c.closed:
		return false
	default:
	}
	buf := make([]byte, len(data))
	copy(buf, data)
	select {
	case c.send <- buf:
		return true
	default:
		return false // 慢消费者主动丢帧，绝不阻塞构建
	}
}

// writePump 是唯一写协程：推送日志 + 周期心跳。
func (c *wsClient) writePump() {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		c.close()
	}()
	for {
		select {
		case msg := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-c.closed:
			return
		}
	}
}

// readPump 仅用于感知对端关闭与消费控制帧，业务数据不再从客户端读取。
func (c *wsClient) readPump() {
	defer c.close()
	// 日志连接不应接收业务数据；限制帧大小并依赖 ping/pong 检测死连接。
	c.conn.SetReadLimit(1024)
	_ = c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	})
	for {
		if _, _, err := c.conn.ReadMessage(); err != nil {
			return
		}
	}
}

// close 幂等关闭。
func (c *wsClient) close() {
	c.once.Do(func() {
		close(c.closed)
		_ = c.conn.Close()
		if c.onClose != nil {
			c.onClose()
		}
	})
}
