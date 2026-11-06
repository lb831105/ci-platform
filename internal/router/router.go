package router

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/go-chi/chi/v5"

	"ci-platform/internal/config"
	"ci-platform/internal/handler"
	"ci-platform/internal/middleware"
	"ci-platform/internal/model"
	"ci-platform/internal/pkg"
	"ci-platform/internal/service"
)

// Deps 汇总路由所需的全部依赖，避免 router 直接依赖具体 service。
type Deps struct {
	Config *config.Config

	Pipeline    *handler.PipelineHandler
	Build       *handler.BuildHandler
	Webhook     *handler.WebhookHandler
	WebSocket   *handler.WSHandler
	Auth        *handler.AuthHandler
	System      *handler.SystemHandler
	Credential  *handler.CredentialHandler
	RBAC        *handler.RbacHandler
	View        *handler.ViewHandler
	Notify      *handler.NotifyHandler
	Profile     *handler.ProfileHandler
	Token       *handler.TokenHandler
	Import      *handler.ImportHandler
	Portability *handler.PortabilityHandler
	Tools       *handler.ToolsHandler
	Event       *handler.EventHandler

	// AuthService 同时供中间件校验会话。为 nil 或未启用时接口不鉴权。
	AuthService *service.AuthService

	// RBAC 与 PipelineService 供权限中间件做角色/任务级检查。
	RBACService     *service.RBACService
	PipelineService *service.PipelineService

	// AuditService 供审计中间件记录变更类操作；为 nil 时不记录。
	AuditService *service.AuditService

	// WebAssets 是前端静态资源文件系统（内嵌或磁盘均可）。为 nil 时将返回明确的提示页。
	WebAssets fs.FS

	// Readiness 是额外的就绪检查（如凭据密钥可读）；为 nil 时只检查数据目录。
	Readiness func(context.Context) error
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
	// Prometheus 指标：只暴露聚合值，不含任务名/日志/密钥。
	r.Get("/metrics", d.Pipeline.Metrics)
	// OpenAPI 契约：随二进制内嵌，便于脚本/CLI/第三方集成查阅。
	r.Get("/openapi.json", handler.OpenAPISpec)
	r.Get("/api/openapi.json", handler.OpenAPISpec)
	r.Get("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if d.Config == nil || !pkg.IsDir(d.Config.DataHome) {
			http.Error(w, "data home is not ready", http.StatusServiceUnavailable)
			return
		}
		if d.Readiness != nil {
			if err := d.Readiness(r.Context()); err != nil {
				http.Error(w, "dependency is not ready: "+err.Error(), http.StatusServiceUnavailable)
				return
			}
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok"))
	})

	spa := spaHandler(d.WebAssets)
	apiMiss := apiNotFound()

	// 权限检查：全局权限与任务级权限（任务角色按任务名 Pattern 匹配授权）。
	perm := func(p string) func(http.Handler) http.Handler {
		return middleware.RequirePerm(d.RBACService, p)
	}
	jobPerm := func(action string) func(http.Handler) http.Handler {
		return middleware.RequireJobPerm(d.RBACService, d.PipelineService, action)
	}

	r.Route("/api", func(r chi.Router) {
		// 公开接口：登录、登录态探测、Webhook（Webhook 用自身密钥鉴权）。
		r.Get("/auth/status", d.Auth.Status)
		// 登录单独挂审计（在 Auth 之外；用户名从响应体解析，密码等请求体绝不落盘）。
		if d.AuditService != nil {
			r.With(middleware.Audit(d.AuditService)).Post("/auth/login", d.Auth.Login)
		} else {
			r.Post("/auth/login", d.Auth.Login)
		}
		r.Post("/webhook/{pipeline_id}", d.Webhook.Handle)

		// 需要登录的接口。
		r.Group(func(r chi.Router) {
			r.Use(middleware.Auth(d.AuthService))
			// 审计：记录变更类操作（必须在 Auth 之后取当前用户；登录在上方单独挂）。
			if d.AuditService != nil {
				r.Use(middleware.Audit(d.AuditService))
			}

			r.Get("/auth/me", d.Auth.Me)
			r.Post("/auth/logout", d.Auth.Logout)
			r.Post("/auth/password", d.Auth.ChangePassword)

			// 个人资料：自助维护联系方式与邮箱验证码（任何已登录用户，无需 Administer）。
			if d.Profile != nil {
				r.Get("/profile", d.Profile.Get)
				r.Put("/profile/contact", d.Profile.UpdateContact)
				r.Post("/profile/email/verify", d.Profile.RequestEmailVerify)
				r.Post("/profile/email/confirm", d.Profile.ConfirmEmail)
			}
			// API Token：脚本/外部系统用，权限继承当前用户；原始 secret 只在创建时返回一次。
			if d.Token != nil {
				r.Get("/profile/tokens", d.Token.List)
				r.Post("/profile/tokens", d.Token.Create)
				r.Delete("/profile/tokens/{id}", d.Token.Delete)
			}

			// 平台内用户与角色管理：仅 Administer。
			r.Route("/security", func(r chi.Router) {
				r.Use(perm(model.PermOverallAdminister))
				r.Get("/rbac", d.RBAC.Get)
				r.Get("/rbac/effective", d.RBAC.Effective)
				r.Put("/rbac/roles", d.RBAC.PutRoles)
				r.Put("/rbac/assignments", d.RBAC.PutAssignments)
				r.Get("/users", d.RBAC.Users)
				r.Post("/users", d.RBAC.AddUser)
				r.Post("/users/delete", d.RBAC.DeleteUser)
				r.Put("/users/password", d.RBAC.ResetUserPassword)
				r.Put("/users/contact", d.RBAC.SetUserContact)
			})

			// 视图：给任务归类（列表视图 + 我的视图）。
			r.Route("/views", func(r chi.Router) {
				r.With(perm(model.PermViewRead)).Get("/", d.View.List)
				r.With(perm(model.PermViewRead)).Get("/{name}/jobs", d.View.Jobs)
				r.With(perm(model.PermViewCreate)).Post("/", d.View.Create)
				r.With(perm(model.PermViewUpdate)).Put("/{name}", d.View.Update)
				r.With(perm(model.PermViewDelete)).Delete("/{name}", d.View.Delete)
			})

			// 总览/全局列表：登录即可访问，内容由任务角色的 Pattern 过滤。
			// 不能用 overall.read 卡：只被授予任务角色的用户拿不到 overall.read，
			// 结果就是"授权了却连任务列表都打不开"，任务级权限形同虚设。
			r.Get("/overview", d.Pipeline.Overview)
			// 首页仪表盘：统计范围按当前用户可见任务过滤。
			r.Get("/dashboard", d.Pipeline.Dashboard)
			r.Get("/builds", d.Pipeline.AllBuilds)

			r.Route("/system", func(r chi.Router) {
				r.With(perm(model.PermSystemRead)).Get("/info", d.System.Info)
				r.With(perm(model.PermSystemRead)).Get("/load", d.System.Load)
				r.With(perm(model.PermSystemRead)).Get("/log", d.System.Log)
				r.With(perm(model.PermSystemManage)).Delete("/log", d.System.ClearLog)
				r.With(perm(model.PermSystemRead)).Get("/features", d.System.Features)
				// 全局工具配置的自动安装：版本清单只读，下载需要系统管理权限。
				if d.Tools != nil {
					r.With(perm(model.PermSystemRead)).Get("/tools/catalog", d.Tools.Catalog)
					r.With(perm(model.PermSystemManage)).Post("/tools/install", d.Tools.Install)
					r.With(perm(model.PermSystemRead)).Get("/tools/tasks", d.Tools.Tasks)
				}
				r.With(perm(model.PermSystemRead)).Get("/settings", d.System.GetSettings)
				r.With(perm(model.PermSystemManage)).Put("/settings", d.System.UpdateSettings)
				r.With(perm(model.PermSystemManage)).Post("/reload", d.System.ReloadSettings)
				r.With(perm(model.PermSystemManage)).Post("/quiet", d.System.SetQuiet)
				r.With(perm(model.PermSystemManage)).Post("/email/test", d.System.TestEmail)
				r.With(perm(model.PermSystemManage)).Post("/webhook/test", d.System.TestWebhookNotify)
				// 全局出站事件订阅：配置系统管理，Webhook URL/Secret 不回传。
				if d.Event != nil {
					r.With(perm(model.PermSystemRead)).Get("/events", d.Event.List)
					r.With(perm(model.PermSystemManage)).Put("/events", d.Event.Update)
					r.With(perm(model.PermSystemManage)).Post("/events/test", d.Event.Test)
				}
				r.With(perm(model.PermSystemRead)).Get("/audit", d.System.Audit)
				// 通知投递记录：按任务/收件人/状态过滤，排查「谁漏收了告警」。
				if d.Notify != nil {
					r.With(perm(model.PermSystemRead)).Get("/notify", d.Notify.History)
				}
				r.With(perm(model.PermSystemRead)).Get("/old-data", d.System.OldData)
				r.With(perm(model.PermSystemManage)).Post("/old-data/clean", d.System.CleanOldData)
				r.With(perm(model.PermSystemRead)).Get("/data-home", d.System.GetDataHome)
				r.With(perm(model.PermSystemManage)).Put("/data-home", d.System.UpdateDataHome)
				// 从其它 CI 导入任务（当前支持 Jenkins config.xml）：管理员操作，
				// 会创建任务与工作区，因此用系统管理权限而不是任务权限。
				if d.Import != nil {
					r.With(perm(model.PermSystemManage)).Post("/import/jenkins", d.Import.Jenkins)
				}
				// 导入平台自有 JSON 配置（导出文档或原生 config.json）。
				if d.Portability != nil {
					r.With(perm(model.PermSystemManage)).Post("/import/config", d.Portability.ImportConfig)
				}
			})

			r.Route("/credentials", func(r chi.Router) {
				// 凭据清单（不含密钥）：任务配置者也要能从下拉里选凭据，
				// 因此除 credential.view 外，对任意任务有 job.configure 的人也可读。
				r.With(middleware.RequireAnyOf(d.RBACService, []string{model.PermCredentialView}, []string{model.PermJobConfigure})).
					Get("/", d.Credential.List)
				r.With(perm(model.PermCredentialCreate)).Post("/", d.Credential.Create)
				r.With(perm(model.PermCredentialView)).Get("/{cid}", d.Credential.Get)
				r.With(perm(model.PermCredentialUpdate)).Put("/{cid}", d.Credential.Update)
				r.With(perm(model.PermCredentialDelete)).Delete("/{cid}", d.Credential.Delete)
			})

			// 通知选人：对任意任务有配置权即可（邮箱属于个人信息，仍需身份）。
			r.With(middleware.RequireAnyOf(d.RBACService, nil, []string{model.PermJobConfigure})).
				Get("/notify/contacts", d.Notify.Contacts)

			r.Route("/pipelines", func(r chi.Router) {
				// 任务列表同理：登录即可，内容按 JobScope 过滤（无任何 job.read 则返回空列表）。
				r.Get("/", d.Pipeline.List)
				r.With(perm(model.PermJobCreate)).Post("/", d.Pipeline.Create)

				r.Route("/{pid}", func(r chi.Router) {
					r.With(jobPerm(model.PermJobRead)).Get("/", d.Pipeline.Get)
					// 导出任务配置（对齐 Jenkins 的 job/{name}/config.xml）：返回裸 JSON 文档，
					// 可直接作为「导入配置」接口的输入，便于实例间搬迁。
					if d.Portability != nil {
						r.With(jobPerm(model.PermJobRead)).Get("/config.json", d.Portability.ExportConfig)
					}
					r.With(jobPerm(model.PermJobConfigure)).Put("/", d.Pipeline.Update)
					r.With(jobPerm(model.PermJobDelete)).Delete("/", d.Pipeline.Delete)
					r.With(jobPerm(model.PermJobBuild)).Post("/build", d.Pipeline.TriggerBuild)
					r.With(jobPerm(model.PermJobBuild)).Post("/builds/{bid}/cancel", d.Build.Cancel)
					// Re-run：密码参数从服务端加密快照还原，前端不接触明文。
					r.With(jobPerm(model.PermJobBuild)).Post("/builds/{bid}/rerun", d.Build.Rerun)
					// 发布审批：审批人必须拥有 job.approve，不能复用触发/配置权限。
					r.With(jobPerm(model.PermJobApprove)).Post("/builds/{bid}/approve", d.Build.Approve)
					r.With(jobPerm(model.PermJobApprove)).Post("/builds/{bid}/reject", d.Build.Reject)
					r.With(jobPerm(model.PermJobConfigure)).Post("/rename", d.Pipeline.Rename)
					r.With(jobPerm(model.PermJobConfigure)).Post("/notify/test", d.Notify.TestNotify)

					r.With(jobPerm(model.PermJobRead)).Get("/builds", d.Build.List)
					r.With(jobPerm(model.PermJobRead)).Get("/builds/{bid}", d.Build.Get)
					// 构建页备注与「永久保留」：写操作，按任务配置权限控制。
					r.With(jobPerm(model.PermJobConfigure)).Put("/builds/{bid}", d.Build.Update)
					r.With(jobPerm(model.PermJobRead)).Get("/builds/{bid}/log", d.Build.Log)
					r.With(jobPerm(model.PermJobRead)).Get("/builds/{bid}/log/download", d.Build.DownloadLog)
					r.With(jobPerm(model.PermJobRead)).Get("/builds/{bid}/artifacts", d.Build.Artifacts)
					r.With(jobPerm(model.PermJobRead)).Get("/builds/{bid}/artifacts/download", d.Build.DownloadArtifact)
					r.With(jobPerm(model.PermJobRead)).Get("/builds/{bid}/artifacts.zip", d.Build.DownloadArtifactsZip)
					// 跨任务产物晋级：目标任务需 job.build；源任务读权限在 handler 内校验。
					r.With(jobPerm(model.PermJobBuild)).Post("/builds/{bid}/artifacts/promote", d.Build.PromoteArtifacts)
					r.With(jobPerm(model.PermJobReplay)).Post("/builds/{bid}/republish", d.Build.Republish)
					// 删除构建（含 data/jobs/{pid}/builds/{bid} 目录）。
					r.With(jobPerm(model.PermJobDelete)).Delete("/builds/{bid}", d.Build.Delete)
				})
			})
		})

		// /api 下未命中的路径：返回统一 JSON 体，而不是 chi 默认的纯文本
		// "404 page not found"。前端 request 层因此能照常解析并给出可读提示。
		r.NotFound(apiMiss)
	})

	// 实时日志流（WebSocket 通过 token 查询参数鉴权 + 任务读权限）。
	r.With(middleware.Auth(d.AuthService)).
		With(middleware.RequireJobPerm(d.RBACService, d.PipelineService, model.PermJobRead)).
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
