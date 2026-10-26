package service

import (
	"regexp"

	"ci-platform/internal/model"
)

// JobScope 是「当前用户能看到哪些任务」的判定器。
//
// 为什么单独抽一层：任务级权限（任务角色 + Pattern）如果只用于接口前的
// 403 校验，用户在列表/概览/全局构建历史里仍然能看到全部任务名，
// 只是点进去被拒。这既泄露了任务清单，也让「角色 ↔ 任务」看起来没挂上钩。
// 所以列表类接口统一用 JobScope 过滤：看不到的任务直接不出现。
type JobScope struct {
	// all 为 true 表示不受限（管理员、认证关闭、或全局角色给了 job.read）。
	all bool
	// patterns 是拥有 job.read 的任务角色的 Pattern，已预先编译。
	patterns []*regexp.Regexp
}

// AllJobs 返回不受限的作用域。
func AllJobs() *JobScope {
	return &JobScope{all: true}
}

// NewJobScope 用任务名正则构造作用域；非法正则忽略（保存时已校验，
// 这里宁可少授权也不 panic）。
func NewJobScope(all bool, patterns []string) *JobScope {
	scope := &JobScope{all: all}
	if all {
		return scope
	}
	for _, raw := range patterns {
		if raw == "" {
			continue
		}
		// 与 Jenkins Role Strategy 一致：忽略大小写、完全匹配任务名。
		compiled, err := regexp.Compile(`(?i)^(?:` + raw + `)$`)
		if err != nil {
			continue
		}
		scope.patterns = append(scope.patterns, compiled)
	}
	return scope
}

// All 判断是否不受限。
func (s *JobScope) All() bool {
	return s == nil || s.all
}

// Allows 判断用户是否能看到指定任务名。
func (s *JobScope) Allows(jobName string) bool {
	if s.All() {
		return true
	}
	for _, re := range s.patterns {
		if re.MatchString(jobName) {
			return true
		}
	}
	return false
}

// Filter 返回作用域内的任务名子集，便于一次性判断。
func (s *JobScope) Filter(names []string) []string {
	if s.All() {
		return names
	}
	out := make([]string, 0, len(names))
	for _, name := range names {
		if s.Allows(name) {
			out = append(out, name)
		}
	}
	return out
}

// HasAnyOf 判断用户是否拥有「任一全局权限」或「对任意任务拥有任一任务权限」。
//
// 用途：配置任务的人需要读凭据清单（不含密钥）与通知联系人，但这两个接口
// 不属于任何一个具体任务，没法用 RequireJobPerm。用「只要对某个任务有配置权
// 就允许」既满足普通用户管自己任务的需求，也不放宽到"人人可看"。
func (s *RBACService) HasAnyOf(user string, globalPerms, jobPerms []string) bool {
	if s.authDisabled() || s.IsAdmin(user) {
		return true
	}
	for _, perm := range globalPerms {
		if s.HasPermission(user, perm) {
			return true
		}
	}
	if len(jobPerms) == 0 {
		return false
	}
	for _, role := range s.rolesFor(user, model.RoleKindGlobal) {
		for _, perm := range jobPerms {
			if role.Has(perm, s.resolveTemplate, 0) {
				return true
			}
		}
	}
	for _, role := range s.rolesFor(user, model.RoleKindItem) {
		for _, perm := range jobPerms {
			if role.Has(perm, s.resolveTemplate, 0) {
				return true
			}
		}
	}
	return false
}
