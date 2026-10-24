package handler

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"ci-platform/internal/model"
	"ci-platform/internal/pkg"
	"ci-platform/internal/service"
)

// NotifyHandler 暴露通知相关的辅助接口：
//   - 人员联系方式列表（任务通知配置中按人员选择）
//   - 任务级测试发送
type NotifyHandler struct {
	pipelines *service.PipelineService
	notifier  *service.NotifyService
	rbac      *service.RBACService
}

// NewNotifyHandler 构造通知接口处理器。
func NewNotifyHandler(pipelines *service.PipelineService, notifier *service.NotifyService, rbac *service.RBACService) *NotifyHandler {
	return &NotifyHandler{pipelines: pipelines, notifier: notifier, rbac: rbac}
}

// Contacts GET /api/notify/contacts
// 返回全部平台用户的名称与联系方式，供任务通知配置选择人员。
// 邮箱属于个人信息，仅授予「任务配置」权限的用户可读取。
func (h *NotifyHandler) Contacts(w http.ResponseWriter, r *http.Request) {
	if h.rbac == nil {
		pkg.OK(w, []model.RbacUser{})
		return
	}
	pkg.OK(w, h.rbac.AllContacts())
}

// History GET /api/system/notify?limit=&pipeline=&recipient=&status=
// 通知投递记录（按收件人粒度），用于排查「谁漏收了告警」。
func (h *NotifyHandler) History(w http.ResponseWriter, r *http.Request) {
	if h.notifier == nil {
		pkg.OK(w, []model.NotifyRecord{})
		return
	}
	q := r.URL.Query()
	limit := model.ParsePositiveInt(q.Get("limit"))
	records, err := h.notifier.History(limit, strings.TrimSpace(q.Get("pipeline")),
		strings.TrimSpace(q.Get("recipient")), strings.TrimSpace(q.Get("status")))
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	pkg.OK(w, records)
}

// TestNotify POST /api/pipelines/{pid}/notify/test
// 向该任务当前配置的收件人与群机器人发送一条测试消息（不受事件过滤）。
func (h *NotifyHandler) TestNotify(w http.ResponseWriter, r *http.Request) {
	p, err := h.pipelines.Get(r.Context(), chi.URLParam(r, "pid"))
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	res := h.notifier.SendTest(r.Context(), p)
	if len(res.Errors) > 0 && len(res.Emails) == 0 && len(res.Targets) == 0 {
		// 全部失败：直接返回错误，便于前端提示（错误信息不含含密钥的 URL）。
		pkg.Fail(w, pkg.ErrBadRequest("测试通知发送失败: %s", strings.Join(res.Errors, "；")))
		return
	}
	pkg.OK(w, map[string]interface{}{
		"emails":  res.Emails,
		"targets": res.Targets,
		"errors":  res.Errors,
		"summary": res.Line(),
	})
}
