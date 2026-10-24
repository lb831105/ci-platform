package handler

import (
	"net/http"
	"strconv"

	"ci-platform/internal/model"
	"ci-platform/internal/pkg"
	"ci-platform/internal/service"
)

// SystemHandler 暴露「系统管理」相关的只读信息与运维动作。
type SystemHandler struct {
	system *service.SystemService
	auth   *service.AuthService
}

// NewSystemHandler 构造系统管理处理器。
func NewSystemHandler(system *service.SystemService, auth *service.AuthService) *SystemHandler {
	return &SystemHandler{system: system, auth: auth}
}

// settingsView 是系统配置的对外视图：绝不回传密码明文，只告知是否已设置。
type settingsView struct {
	model.Settings
	HasPassword   bool `json:"has_password"`
	HasWebhookURL bool `json:"has_webhook_url"`
}

func toSettingsView(s model.Settings) settingsView {
	has := s.AuthPasswordHash != "" || s.AuthPassword != ""
	s.AuthPassword = ""
	s.AuthPasswordHash = ""
	// SMTP 密码同样不回传，前端留空即沿用。
	s.Email.Password = ""
	// SSH 服务器密码不回传；注意深拷贝切片，避免修改到服务内存中的配置。
	if s.SshServers != nil {
		servers := make([]model.SshServer, len(s.SshServers))
		copy(servers, s.SshServers)
		for i := range servers {
			servers[i].HasPassword = servers[i].Password != ""
			servers[i].Password = ""
		}
		s.SshServers = servers
	}
	// Webhook 通知 URL 含 access_token，同样不回传；has_webhook_url 告知是否已设置。
	hasWebhook := s.WebhookNotify.URL != ""
	s.WebhookNotify.URL = ""
	return settingsView{Settings: s, HasPassword: has, HasWebhookURL: hasWebhook}
}

// Info GET /api/system/info
func (h *SystemHandler) Info(w http.ResponseWriter, r *http.Request) {
	info, err := h.system.Info(r.Context())
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	pkg.OK(w, info)
}

// Load GET /api/system/load
func (h *SystemHandler) Load(w http.ResponseWriter, r *http.Request) {
	stats, err := h.system.Load(r.Context())
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	pkg.OK(w, stats)
}

// Log GET /api/system/log?lines=500
func (h *SystemHandler) Log(w http.ResponseWriter, r *http.Request) {
	lines := 500
	if raw := r.URL.Query().Get("lines"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			lines = n
		}
	}
	pkg.OK(w, map[string]interface{}{"lines": h.system.LogLines(lines)})
}

// ClearLog DELETE /api/system/log
func (h *SystemHandler) ClearLog(w http.ResponseWriter, r *http.Request) {
	h.system.ClearLog()
	pkg.OK(w, nil)
}

// Features GET /api/system/features
func (h *SystemHandler) Features(w http.ResponseWriter, r *http.Request) {
	pkg.OK(w, h.system.Features())
}

// settingsRequest 是系统配置更新请求体。
// 嵌入 Settings 以复用字段，同时显式接收只读视图字段 has_password，
// 避免前端把 GET 的响应原样回传时被 DisallowUnknownFields 拒绝。
type settingsRequest struct {
	model.Settings
	HasPassword   *bool `json:"has_password"`
	HasWebhookURL *bool `json:"has_webhook_url"`
}

// GetSettings GET /api/system/settings
func (h *SystemHandler) GetSettings(w http.ResponseWriter, r *http.Request) {
	pkg.OK(w, toSettingsView(h.system.Settings()))
}

// UpdateSettings PUT /api/system/settings
func (h *SystemHandler) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	var in settingsRequest
	if err := decodeJSON(w, r, &in); err != nil {
		pkg.Fail(w, err)
		return
	}
	updated, err := h.system.UpdateSettings(in.Settings)
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	h.syncAuth(updated)
	pkg.OK(w, toSettingsView(updated))
}

// ReloadSettings POST /api/system/reload
func (h *SystemHandler) ReloadSettings(w http.ResponseWriter, r *http.Request) {
	updated, err := h.system.ReloadSettings()
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	h.syncAuth(updated)
	pkg.OK(w, toSettingsView(updated))
}

// SetQuiet POST /api/system/quiet  body: {"quiet": true}
func (h *SystemHandler) SetQuiet(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Quiet bool `json:"quiet"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		pkg.Fail(w, err)
		return
	}
	settings := h.system.Settings()
	settings.QuietMode = in.Quiet
	updated, err := h.system.UpdateSettings(settings)
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	pkg.OK(w, toSettingsView(updated))
}

// TestEmail POST /api/system/email/test  body: {"to": "a@b.com"}
func (h *SystemHandler) TestEmail(w http.ResponseWriter, r *http.Request) {
	var in struct {
		To string `json:"to"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		pkg.Fail(w, err)
		return
	}
	if err := h.system.SendTestEmail(in.To); err != nil {
		pkg.Fail(w, err)
		return
	}
	pkg.OK(w, nil)
}

// TestWebhookNotify POST /api/system/webhook/test
func (h *SystemHandler) TestWebhookNotify(w http.ResponseWriter, r *http.Request) {
	if err := h.system.SendTestWebhookNotify(); err != nil {
		pkg.Fail(w, err)
		return
	}
	pkg.OK(w, nil)
}

// Audit GET /api/system/audit?limit=200 — 操作审计日志（管理员）。
func (h *SystemHandler) Audit(w http.ResponseWriter, r *http.Request) {
	limit := 200
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 1000 {
			limit = n
		}
	}
	entries, err := h.system.AuditEntries(limit)
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	pkg.OK(w, entries)
}

// GetDataHome GET /api/system/data-home
func (h *SystemHandler) GetDataHome(w http.ResponseWriter, r *http.Request) {
	pkg.OK(w, h.system.DataHome())
}

// UpdateDataHome PUT /api/system/data-home  body: {"data_home": "...", "migrate": true}
func (h *SystemHandler) UpdateDataHome(w http.ResponseWriter, r *http.Request) {
	var in struct {
		DataHome string `json:"data_home"`
		Migrate  bool   `json:"migrate"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		pkg.Fail(w, err)
		return
	}
	info, err := h.system.UpdateDataHome(r.Context(), in.DataHome, in.Migrate)
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	pkg.OK(w, info)
}

// OldData GET /api/system/old-data
func (h *SystemHandler) OldData(w http.ResponseWriter, r *http.Request) {
	report, err := h.system.ScanOldData(r.Context())
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	pkg.OK(w, report)
}

// CleanOldData POST /api/system/old-data/clean
func (h *SystemHandler) CleanOldData(w http.ResponseWriter, r *http.Request) {
	report, err := h.system.CleanOldData(r.Context())
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	pkg.OK(w, report)
}

// syncAuth 把配置中的认证开关与凭据同步到运行时认证服务。
func (h *SystemHandler) syncAuth(s model.Settings) {
	h.auth.SetEnabled(s.AuthEnabled)
	h.auth.SetCredentials(s.AuthUser, s.AuthPasswordHash)
}
