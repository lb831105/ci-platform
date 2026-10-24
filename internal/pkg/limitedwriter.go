package pkg

import "io"

// LimitedWriter 带上限的包装 writer：超过 max 字节后丢弃后续写入（假装成功，
// 不中断构建），并首次超限时向上层写入截断标记。
// 用于构建日志：超长日志会拖慢 ReadLog 与前端渲染，且可能把磁盘写满；
// 截断而非报错，保证构建结果不受日志体积影响。
type LimitedWriter struct {
	w         io.Writer
	max       int64
	written   int64
	truncated bool
	truncMsg  []byte // 首次截断时写入的提示
}

// NewLimitedWriter 包装 w；max <= 0 表示不限制（直接透传）。
// truncMsg 在首次截断时写入一次（可为空）。
func NewLimitedWriter(w io.Writer, max int64, truncMsg []byte) *LimitedWriter {
	return &LimitedWriter{w: w, max: max, truncMsg: truncMsg}
}

// Written 返回已写入（未被丢弃）的字节数。
func (lw *LimitedWriter) Written() int64 { return lw.written }

func (lw *LimitedWriter) Write(p []byte) (int, error) {
	if lw.max <= 0 {
		n, err := lw.w.Write(p)
		lw.written += int64(n)
		return n, err
	}
	if lw.written >= lw.max {
		// 已超限：丢弃，但首次要补写截断标记（在限内预留空间）。
		if !lw.truncated {
			lw.truncated = true
			if len(lw.truncMsg) > 0 {
				msg := lw.truncMsg
				if room := lw.max - lw.written; int64(len(msg)) > room && room > 0 {
					msg = msg[:room]
				}
				_, _ = lw.w.Write(msg)
			}
		}
		return len(p), nil
	}
	room := lw.max - lw.written
	if int64(len(p)) <= room {
		n, err := lw.w.Write(p)
		lw.written += int64(n)
		return n, err
	}
	// 部分写入后超限。
	n, _ := lw.w.Write(p[:room])
	lw.written += int64(n)
	lw.truncated = true
	if len(lw.truncMsg) > 0 {
		_, _ = lw.w.Write(lw.truncMsg)
	}
	// 返回 len(p) 让调用方认为全部写入成功，不中断构建。
	return len(p), nil
}
