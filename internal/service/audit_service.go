package service

import (
	"ci-platform/internal/pkg/logger"
	"sync/atomic"

	"ci-platform/internal/model"
)

// AuditRecorder 由 AuditStore 实现：追加与读取审计记录。
type AuditRecorder interface {
	Append(e *model.AuditEntry) error
	Tail(limit int) ([]model.AuditEntry, error)
}

// AuditService 操作审计：收集变更类操作并落盘。
// 记录点统一在审计中间件（覆盖登录/登出/构建触发/任务与配置增删改），
// handler/service 不各自埋点，避免遗漏。
type AuditService struct {
	recorder AuditRecorder
	seq      atomic.Int64 // 单调递增 ID（进程内计数，重启后从文件恢复最大值）
}

// NewAuditService 创建审计服务；启动时从文件恢复最大 ID，保证 ID 单调。
func NewAuditService(recorder AuditRecorder, lastID int64) *AuditService {
	s := &AuditService{recorder: recorder}
	s.seq.Store(lastID)
	return s
}

// Record 记录一条审计日志（带 ID 分配），供非 HTTP 入口使用。
func (s *AuditService) Record(user, action, target string, status int, ip string) {
	s.Post(model.AuditEntry{User: user, Action: action, Target: target, Status: status, IP: ip})
}

// Post 追加一条审计记录（审计中间件入口）；分配单调 ID 后落盘。
func (s *AuditService) Post(e model.AuditEntry) {
	if s.recorder == nil {
		return
	}
	e.ID = s.seq.Add(1)
	e.User = sanitizeAuditUser(e.User)
	if err := s.recorder.Append(&e); err != nil {
		logger.Errorf("[audit] append failed: %v", err)
	}
}

// Entries 返回最近 limit 条审计记录（时间倒序，最新在前）。
func (s *AuditService) Entries(limit int) ([]model.AuditEntry, error) {
	if s.recorder == nil {
		return []model.AuditEntry{}, nil
	}
	return s.recorder.Tail(limit)
}

// sanitizeAuditUser 清理用户名（长度限制，防显示注入）。
func sanitizeAuditUser(user string) string {
	const max = 64
	if len(user) > max {
		user = user[:max]
	}
	return user
}

// LastIDFromEntries 从既有审计记录中恢复最大 ID（启动时调用）。
func LastIDFromEntries(entries []model.AuditEntry) int64 {
	var max int64
	for _, e := range entries {
		if e.ID > max {
			max = e.ID
		}
	}
	return max
}
