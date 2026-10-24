package model

import "time"

// AuditEntry 操作审计日志：记录谁在何时对什么做了什么。
// 只记录变更类操作（登录/登出/构建触发/任务与配置增删改），
// 查询类请求不记录（量大且无审计价值）。
type AuditEntry struct {
	ID     int64     `json:"id"`     // 单调递增（从 1 开始，进程内计数）
	Time   time.Time `json:"time"`   // 操作时间（UTC）
	User   string    `json:"user"`   // 操作人（未登录为 anonymous）
	Action string    `json:"action"` // 动作：login / logout / create / update / delete / build 等
	Target string    `json:"target"` // 目标资源：METHOD + 路径（如 POST /api/pipelines）
	Status int       `json:"status"` // 业务码（0=成功，其他为失败）
	IP     string    `json:"ip"`     // 客户端 IP（RealIP）
}

// 审计动作常量。
const (
	AuditLogin  = "login"
	AuditLogout = "logout"
)
