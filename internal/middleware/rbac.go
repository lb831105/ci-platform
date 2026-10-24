package middleware

import (
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

// RequirePerm 返回校验平台级权限的中间件。rbac 为 nil 时放行（兼容未装配场景）。
func RequirePerm(rbac PermChecker, perm string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if rbac == nil {
				next.ServeHTTP(w, r)
				return
			}
			if !rbac.HasPermission(pkg.UserFromContext(r.Context()), perm) {
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
			if !rbac.HasJobPermission(pkg.UserFromContext(r.Context()), action, name) {
				pkg.Fail(w, pkg.NewError(pkg.CodeForbidden, "没有任务「%s」所需权限: %s", name, action))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
