package middleware

import (
	"ci-platform/internal/pkg/logger"
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"ci-platform/internal/pkg"
)

// PermChecker 平台级权限检查（由 RBACService 实现）。
type PermChecker interface {
	HasPermission(user, perm string) bool
}

// JobPermChecker 任务级权限检查（由 RBACService 实现）。
type JobPermChecker interface {
	HasJobPermission(user, action, jobName string) bool
}

// JobNameLookup 按任务 ID 返回任务名（由 PipelineService 实现）；
// 任务角色通过任务名与 Pattern 匹配授权。
type JobNameLookup interface {
	JobName(ctx context.Context, id string) (string, error)
}

// AnyPermChecker 判定「任一全局权限 或 任意任务的任一任务权限」。
type AnyPermChecker interface {
	HasAnyOf(user string, globalPerms, jobPerms []string) bool
}

// RequireAnyOf 返回校验「任一权限」的中间件：
// globalPerms 命中即放行；否则看用户是否对任意任务拥有 jobPerms 之一。
// 用于不属于具体任务、但任务配置者必须访问的接口（凭据清单、通知联系人）。
func RequireAnyOf(rbac AnyPermChecker, globalPerms, jobPerms []string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if rbac == nil {
				next.ServeHTTP(w, r)
				return
			}
			user := pkg.UserFromContext(r.Context())
			if !tokenAllowsAny(r.Context(), globalPerms, jobPerms) {
				logger.Debugf("[rbac] deny token scope user=%q path=%s", user, r.URL.Path)
				pkg.Fail(w, pkg.NewError(pkg.CodeForbidden, "API Token 没有该操作所需的权限"))
				return
			}
			if !rbac.HasAnyOf(user, globalPerms, jobPerms) {
				logger.Debugf("[rbac] deny user=%q perms=%v job_perms=%v path=%s",
					user, globalPerms, jobPerms, r.URL.Path)
				pkg.Fail(w, pkg.NewError(pkg.CodeForbidden, "没有该操作所需的权限"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequirePerm 返回校验平台级权限的中间件。rbac 为 nil 时放行（兼容未装配场景）。
func RequirePerm(rbac PermChecker, perm string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if rbac == nil {
				next.ServeHTTP(w, r)
				return
			}
			if !pkg.TokenAllows(r.Context(), perm) {
				logger.Debugf("[rbac] deny token scope user=%q perm=%s path=%s",
					pkg.UserFromContext(r.Context()), perm, r.URL.Path)
				pkg.Fail(w, pkg.NewError(pkg.CodeForbidden, "API Token 没有该操作所需的权限: %s", perm))
				return
			}
			if !rbac.HasPermission(pkg.UserFromContext(r.Context()), perm) {
				logger.Debugf("[rbac] deny user=%q perm=%s path=%s",
					pkg.UserFromContext(r.Context()), perm, r.URL.Path)
				pkg.Fail(w, pkg.NewError(pkg.CodeForbidden, "没有该操作所需的权限: %s", perm))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireJobPerm 返回校验任务级权限的中间件。
// 先按任务 ID 解析任务名，再与任务角色的 Pattern 匹配。
func RequireJobPerm(rbac JobPermChecker, lookup JobNameLookup, action string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if rbac == nil || lookup == nil {
				next.ServeHTTP(w, r)
				return
			}
			name, err := lookup.JobName(r.Context(), chi.URLParam(r, "pid"))
			if err != nil {
				pkg.Fail(w, err)
				return
			}
			if !pkg.TokenAllows(r.Context(), action) {
				logger.Debugf("[rbac] deny token scope user=%q job=%q action=%s path=%s",
					pkg.UserFromContext(r.Context()), name, action, r.URL.Path)
				pkg.Fail(w, pkg.NewError(pkg.CodeForbidden, "API Token 没有任务「%s」所需权限: %s", name, action))
				return
			}
			if !rbac.HasJobPermission(pkg.UserFromContext(r.Context()), action, name) {
				pkg.Fail(w, pkg.NewError(pkg.CodeForbidden, "没有任务「%s」所需权限: %s", name, action))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// tokenAllowsAny 检查 API Token scopes 是否覆盖任一 required 权限。
func tokenAllowsAny(ctx context.Context, globalPerms, jobPerms []string) bool {
	scopes, restricted := pkg.TokenScopesFromContext(ctx)
	if !restricted {
		return true
	}
	for _, scope := range scopes {
		if scope == "*" {
			return true
		}
		for _, perm := range globalPerms {
			if scope == perm {
				return true
			}
		}
		for _, perm := range jobPerms {
			if scope == perm {
				return true
			}
		}
	}
	return false
}
