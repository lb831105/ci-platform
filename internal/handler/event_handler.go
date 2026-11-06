package handler

import (
	"net/http"

	"ci-platform/internal/model"
	"ci-platform/internal/pkg"
	"ci-platform/internal/service"
)

// EventHandler 暴露全局出站事件订阅的配置接口。
type EventHandler struct {
	events *service.EventService
}

// NewEventHandler 构造事件订阅处理器。
func NewEventHandler(events *service.EventService) *EventHandler {
	return &EventHandler{events: events}
}

// eventView 脱敏视图：URL 可能含 token，Secret 用于 HMAC，均不回传。
func eventView(item model.EventSubscription) model.EventSubscription {
	item.HasURL = item.URL != ""
	item.HasSecret = item.Secret != ""
	item.URL = ""
	item.Secret = ""
	item.ClearSecret = false
	return item
}

// List GET /api/system/events
func (h *EventHandler) List(w http.ResponseWriter, r *http.Request) {
	items, err := h.events.List()
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	out := make([]model.EventSubscription, len(items))
	for i := range items {
		out[i] = eventView(items[i])
	}
	pkg.OK(w, out)
}

// Update PUT /api/system/events
// 全量替换订阅列表；URL/Secret 留空时按 ID 沿用旧值，ClearSecret=true 才显式清除。
func (h *EventHandler) Update(w http.ResponseWriter, r *http.Request) {
	var in []model.EventSubscription
	if err := decodeJSON(w, r, &in); err != nil {
		pkg.Fail(w, err)
		return
	}
	items, err := h.events.Update(in)
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	out := make([]model.EventSubscription, len(items))
	for i := range items {
		out[i] = eventView(items[i])
	}
	pkg.OK(w, out)
}

// Test POST /api/system/events/test  body: {"id":"可选，空=全部启用订阅"}
func (h *EventHandler) Test(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID string `json:"id"`
	}
	if r.ContentLength != 0 {
		if err := decodeJSON(w, r, &in); err != nil {
			pkg.Fail(w, err)
			return
		}
	}
	if err := h.events.Test(r.Context(), in.ID); err != nil {
		pkg.Fail(w, err)
		return
	}
	pkg.OK(w, nil)
}
