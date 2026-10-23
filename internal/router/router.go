package router

import (
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/go-chi/chi/v5"

	"ci-platform/internal/config"
	"ci-platform/internal/handler"
	"ci-platform/internal/middleware"
	"ci-platform/internal/pkg"
	"ci-platform/internal/service"
)

// Deps 汇总路由所需的全部依赖，避免 router 直接依赖具体 service。
type Deps struct {
	Config *config.Config

	Pipeline   *handler.PipelineHandler
	Build      *handler.BuildHandler
	Webhook    *handler.WebhookHandler
	WebSocket  *handler.WSHandler
	Auth       *handler.AuthHandler
	System     *handler.SystemHandler
	Credential *handler.CredentialHandler

	// AuthService 同时供中间件校验会话。为 nil 或未启用时接口不鉴权。
	AuthService *service.AuthService

	// WebAssets 是前端静态资源文件系统（内嵌或磁盘均可）。为 nil 时将返回明确的提示页。
	WebAssets fs.FS
}

// New 组装路由表与中间件。
func New(d Deps) http.Handler {
	r := chi.NewRouter()
	origins := []string{"*"}
	trustProxy := false
	if d.Config != nil {
		trustProxy = d.Config.TrustProxy
		if len(d.Config.CORSOrigins) > 0 {
			origins = d.Config.CORSOrigins
		}
	}
	r.Use(middleware.RealIP(trustProxy))
	r.Use(middleware.RequestID)
	r.Use(middleware.SecurityHeaders)
	r.Use(middleware.CORS(origins))
	r.Use(middleware.Recover)
	r.Use(middleware.Logger)

	// 供容器/负载均衡健康检查；不经过登录，也不暴露业务数据。
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok"))
	})
	r.Get("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if d.Config == nil || !pkg.IsDir(d.Config.DataHome) {
			http.Error(w, "data home is not ready", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok"))
	})

	spa := spaHandler(d.WebAssets)
	apiMiss := apiNotFound()

	r.Route("/api", func(r chi.Router) {
		// 公开接口：登录、登录态探测、Webhook（Webhook 用自身密钥鉴权）。
		r.Get("/auth/status", d.Auth.Status)
		r.Post("/auth/login", d.Auth.Login)
		r.Post("/webhook/{pipeline_id}", d.Webhook.Handle)

		// 需要登录的接口。
		r.Group(func(r chi.Router) {
			r.Use(middleware.Auth(d.AuthService))

			r.Get("/auth/me", d.Auth.Me)
			r.Post("/auth/logout", d.Auth.Logout)
			r.Post("/auth/password", d.Auth.ChangePassword)

			r.Get("/overview", d.Pipeline.Overview)
			r.Get("/builds", d.Pipeline.AllBuilds)

			r.Route("/system", func(r chi.Router) {
				r.Get("/info", d.System.Info)
				r.Get("/load", d.System.Load)
				r.Get("/log", d.System.Log)
				r.Delete("/log", d.System.ClearLog)
				r.Get("/features", d.System.Features)
				r.Get("/settings", d.System.GetSettings)
				r.Put("/settings", d.System.UpdateSettings)
				r.Post("/reload", d.System.ReloadSettings)
				r.Post("/quiet", d.System.SetQuiet)
				r.Post("/email/test", d.System.TestEmail)
				r.Get("/old-data", d.System.OldData)
				r.Post("/old-data/clean", d.System.CleanOldData)
				r.Get("/data-home", d.System.GetDataHome)
				r.Put("/data-home", d.System.UpdateDataHome)
			})

			r.Route("/credentials", func(r chi.Router) {
				r.Get("/", d.Credential.List)
				r.Post("/", d.Credential.Create)
				r.Get("/{cid}", d.Credential.Get)
				r.Put("/{cid}", d.Credential.Update)
				r.Delete("/{cid}", d.Credential.Delete)
			})

			r.Route("/pipelines", func(r chi.Router) {
				r.Get("/", d.Pipeline.List)
				r.Post("/", d.Pipeline.Create)

				r.Route("/{pid}", func(r chi.Router) {
					r.Get("/", d.Pipeline.Get)
					r.Put("/", d.Pipeline.Update)
					r.Delete("/", d.Pipeline.Delete)
					r.Post("/build", d.Pipeline.TriggerBuild)
					r.Post("/rename", d.Pipeline.Rename)

					r.Get("/builds", d.Build.List)
					r.Get("/builds/{bid}", d.Build.Get)
					r.Get("/builds/{bid}/log", d.Build.Log)
					r.Get("/builds/{bid}/artifacts", d.Build.Artifacts)
					r.Get("/builds/{bid}/artifacts/download", d.Build.DownloadArtifact)
					r.Get("/builds/{bid}/artifacts.zip", d.Build.DownloadArtifactsZip)
					r.Post("/builds/{bid}/republish", d.Build.Republish)
				})
			})
		})

		// /api 下未命中的路径：返回统一 JSON 体，而不是 chi 默认的纯文本
		// "404 page not found"。前端 request 层因此能照常解析并给出可读提示。
		r.NotFound(apiMiss)
	})

	// 实时日志流（WebSocket 通过 token 查询参数鉴权）。
	r.With(middleware.Auth(d.AuthService)).
		Get("/ws/pipelines/{pid}/builds/{bid}/logs", d.WebSocket.ServeLog)

	// 前端资源：真实文件直出，未命中回退 index.html（支持 history 模式深链）。
	// 注册在所有精确路由之后，作为最终兜底。
	r.Handle("/*", spa)

	// 兜底（正常情况下 /* 已覆盖全部路径，这里保证任何分支都不会落到
	// 框架默认的纯文本 404 上）。
	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		if isAPIPath(req.URL.Path) {
			apiMiss(w, req)
			return
		}
		spa(w, req)
	})

	return r
}

// apiNotFound 返回统一响应体的 404：HTTP 200 + code 40400，
// 与项目「HTTP 状态恒 200、业务态由 code 表达」的约定保持一致。
func apiNotFound() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pkg.JSON(w, pkg.CodeNotFound, fmt.Sprintf("接口不存在: %s %s", r.Method, r.URL.Path), nil)
	}
}

// isAPIPath 判断是否为后端接口或 WebSocket 路径，
// 这类路径绝不能被前端 SPA 的 index.html 兜底吞掉。
func isAPIPath(p string) bool {
	return p == "/api" || strings.HasPrefix(p, "/api/") ||
		p == "/ws" || strings.HasPrefix(p, "/ws/")
}

// spaHandler 基于 fs.FS 实现静态资源托管 + SPA 深链回退。
// 资源来源（内嵌 / 磁盘）在此处完全透明。
func spaHandler(webAssets fs.FS) http.HandlerFunc {

	// 资源缺失时，对接口路径仍给出统一 JSON，避免前端拿到 HTML 而解析失败。
	unavailable := func(w http.ResponseWriter, r *http.Request, reason string) {
		if isAPIPath(r.URL.Path) {
			pkg.JSON(w, pkg.CodeInternal, reason, nil)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(unavailablePage(reason)))
	}

	if webAssets == nil {
		return func(w http.ResponseWriter, r *http.Request) {
			unavailable(w, r, "前端资源不可用：二进制未内嵌 web/dist，且磁盘回退目录不存在。")
		}
	}

	indexHTML, err := fs.ReadFile(webAssets, "index.html")
	if err != nil {
		return func(w http.ResponseWriter, r *http.Request) {
			unavailable(w, r, "前端资源缺少 index.html，请重新执行前端构建。")
		}
	}

	fileServer := http.FileServer(http.FS(webAssets))

	return func(w http.ResponseWriter, r *http.Request) {
		if isAPIPath(r.URL.Path) {
			apiNotFound()(w, r)
			return
		}

		name := cleanAssetName(r.URL.Path)

		// 根路径与 index.html 直接返回首页（避免 FileServer 对 /index.html 的 301）。
		if name == "" || name == "index.html" {
			writeIndex(w, indexHTML)
			return
		}

		if info, statErr := fs.Stat(webAssets, name); statErr == nil && !info.IsDir() {
			fileServer.ServeHTTP(w, r)
			return
		}

		// 静态资源（/assets/*）缺失必须显式 404。
		// 若这里回退成 index.html，浏览器会拿到 text/html 的「JS 文件」并报
		// MIME type 错误，排查成本极高。
		if strings.HasPrefix(name, "assets/") {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusNotFound)
			_, _ = fmt.Fprintf(w, "静态资源不存在: %s\n"+
				"（前端产物为构建时嵌入，若刚更新过前端请重新编译服务端）\n", r.URL.Path)
			return
		}

		// 其余路径一律交给前端路由处理（history 模式深链）。
		writeIndex(w, indexHTML)
	}
}

func writeIndex(w http.ResponseWriter, indexHTML []byte) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(indexHTML)
}

// cleanAssetName 把 URL 路径转换为 fs.FS 使用的相对路径。
// 根路径返回空串；同时挡掉 ../ 形式的路径穿越。
func cleanAssetName(urlPath string) string {
	name := strings.TrimPrefix(urlPath, "/")
	if name == "" {
		return ""
	}
	cleaned := path.Clean(name)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return ""
	}
	return cleaned
}

// unavailablePage 生成一张自解释的诊断页。
// 目标是把「为什么打开是空白/报错」直接在页面上讲清楚，而不是丢一个
// 需要翻日志的纯文本错误。
func unavailablePage(reason string) string {
	return "<!DOCTYPE html><html lang=\"zh-CN\"><head><meta charset=\"utf-8\">" +
		"<title>ci-platform · 前端资源不可用</title>" +
		"<style>body{font-family:system-ui,-apple-system,'Segoe UI',sans-serif;" +
		"max-width:720px;margin:64px auto;padding:0 24px;line-height:1.7;color:#1f2328}" +
		"code{background:#f3f4f6;padding:2px 6px;border-radius:4px}" +
		"pre{background:#f6f8fa;padding:14px 16px;border-radius:8px;overflow:auto}" +
		"h1{font-size:20px}</style></head><body>" +
		"<h1>前端资源不可用</h1>" +
		"<p>" + reason + "</p>" +
		"<p>请先构建前端，再重新编译服务端（前端产物通过 <code>go:embed</code> 内嵌进二进制）：</p>" +
		"<pre>cd web\nnpm install\nnpm run build\ncd ..\ngo build -o ci-platform ./cmd/server</pre>" +
		"<p>Windows 上也可以直接双击运行构建脚本：<code>build.bat</code></p>" +
		"</body></html>"
}
