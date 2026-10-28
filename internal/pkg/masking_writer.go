package pkg

import (
	"bytes"
	"io"
	"sort"
	"strings"
	"sync"
)

// MaskingWriter 把构建日志里出现的已知敏感值替换成 ***。
//
// 按行缓冲而不是逐 Write 直接替换：命令输出可能把同一个密钥拆成多个 Write
// （例如 stdout 管道按 4KB 分块），直接替换会漏遮。缓冲区超过 maxMaskBuffer
// 时只保留最长密钥长度-1 的尾部，避免无换行超长输出把内存吃满。
type MaskingWriter struct {
	dst     io.Writer
	secrets [][]byte
	buf     []byte
	mu      sync.Mutex
}

const maxMaskBuffer = 256 * 1024

// NewMaskingWriter 创建日志遮罩 writer；secrets 太短（<4 字节）会被忽略，
// 避免把 "true"、"CI" 之类的普通日志内容大面积替换成 ***。
func NewMaskingWriter(dst io.Writer, secrets []string) *MaskingWriter {
	seen := make(map[string]struct{}, len(secrets))
	out := make([][]byte, 0, len(secrets))
	for _, s := range secrets {
		if len(s) < 4 {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, []byte(s))
	}
	// 长密钥优先替换：短密钥是长密钥前缀时，先替换长的才能完整遮住。
	sort.Slice(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return &MaskingWriter{dst: dst, secrets: out}
}

// Write 实现 io.Writer；返回值始终是 len(p)，日志不因遮罩失败而阻塞构建。
func (w *MaskingWriter) Write(p []byte) (int, error) {
	if w.dst == nil {
		return len(p), nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.secrets) == 0 {
		return w.dst.Write(p)
	}

	w.buf = append(w.buf, p...)
	// 有换行时只处理到最后一个换行，未完成的行留到下次，保证跨 Write 的密钥不漏。
	if idx := bytes.LastIndexByte(w.buf, '\n'); idx >= 0 {
		if err := w.emit(w.buf[:idx+1]); err != nil {
			return len(p), err
		}
		w.buf = append(w.buf[:0], w.buf[idx+1:]...)
	}
	if len(w.buf) > maxMaskBuffer {
		// 保留最长密钥长度-1 的尾部，避免把可能被拆开的密钥写出。
		keep := len(w.secrets[0]) - 1
		if keep < 0 {
			keep = 0
		}
		cut := len(w.buf) - keep
		if cut > 0 {
			if err := w.emit(w.buf[:cut]); err != nil {
				return len(p), err
			}
			w.buf = append(w.buf[:0], w.buf[cut:]...)
		}
	}
	return len(p), nil
}

// Flush 输出最后一段没有换行结尾的日志。BuildService 在构建结束时调用。
func (w *MaskingWriter) Flush() error {
	if w.dst == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.buf) == 0 {
		return nil
	}
	err := w.emit(w.buf)
	w.buf = w.buf[:0]
	return err
}

// MaskSecrets 对完整文本做一次性遮罩，用于读取历史日志时的兜底。
func MaskSecrets(text string, secrets []string) string {
	if text == "" || len(secrets) == 0 {
		return text
	}
	ordered := make([]string, 0, len(secrets))
	seen := make(map[string]struct{}, len(secrets))
	for _, secret := range secrets {
		if len(secret) < 4 {
			continue
		}
		if _, ok := seen[secret]; ok {
			continue
		}
		seen[secret] = struct{}{}
		ordered = append(ordered, secret)
	}
	sort.Slice(ordered, func(i, j int) bool { return len(ordered[i]) > len(ordered[j]) })
	for _, secret := range ordered {
		text = strings.ReplaceAll(text, secret, "***")
	}
	return text
}

func (w *MaskingWriter) emit(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	for _, secret := range w.secrets {
		data = bytes.ReplaceAll(data, secret, []byte("***"))
	}
	_, err := w.dst.Write(data)
	return err
}
