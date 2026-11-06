package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"ci-platform/internal/model"
	"ci-platform/internal/pkg"
	"ci-platform/internal/pkg/logger"
)

// EventStore 是事件订阅的持久化能力（store.EventStore 实现）。
type EventStore interface {
	Load() ([]model.EventSubscription, error)
	Save([]model.EventSubscription) error
}

// EventService 管理全局出站事件订阅与投递。
//
// 设计取舍：
//   - 订阅是全局的（不是任务级），适合接工单/发布系统；
//   - URL/Secret 加密落盘，接口只返回 has_url/has_secret；
//   - 投递失败只告警，绝不能影响构建结果；
//   - 支持 HMAC-SHA256 签名，接收方可以验证来源。
type EventService struct {
	store  EventStore
	client *http.Client
	now    func() time.Time

	mu sync.Mutex // 串行化 Update，避免并发 PUT 丢订阅
}

// NewEventService 构造事件订阅服务。
func NewEventService(store EventStore) *EventService {
	return &EventService{
		store:  store,
		client: &http.Client{Timeout: 15 * time.Second},
		now:    time.Now,
	}
}

const maxEventSubscriptions = 20

// List 返回订阅副本；调用方可以安全脱敏。
func (s *EventService) List() ([]model.EventSubscription, error) {
	items, err := s.store.Load()
	if err != nil {
		return nil, err
	}
	out := make([]model.EventSubscription, len(items))
	for i := range items {
		out[i] = items[i]
		out[i].Events = append([]string(nil), items[i].Events...)
	}
	return out, nil
}

// Update 全量替换订阅列表。URL/Secret 留空时按 ID 沿用旧值；
// 新增项没有 URL 直接拒绝，避免创建“永远发不出去”的订阅。
func (s *EventService) Update(in []model.EventSubscription) ([]model.EventSubscription, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(in) > maxEventSubscriptions {
		return nil, pkg.ErrBadRequest("最多只能配置 %d 个事件订阅", maxEventSubscriptions)
	}
	old, err := s.store.Load()
	if err != nil {
		return nil, err
	}
	oldByID := make(map[string]model.EventSubscription, len(old))
	for _, item := range old {
		oldByID[item.ID] = item
	}
	seen := make(map[string]bool, len(in))
	out := make([]model.EventSubscription, 0, len(in))
	for i := range in {
		item := in[i]
		item.Name = strings.TrimSpace(item.Name)
		item.URL = strings.TrimSpace(item.URL)
		item.Secret = strings.TrimSpace(item.Secret)
		if item.ID == "" {
			item.ID = randomID()
		}
		if seen[item.ID] {
			return nil, pkg.ErrBadRequest("事件订阅 ID 重复: %s", item.ID)
		}
		seen[item.ID] = true
		if item.Name == "" {
			return nil, pkg.ErrBadRequest("事件订阅名称不能为空")
		}
		if len([]rune(item.Name)) > 64 {
			return nil, pkg.ErrBadRequest("事件订阅名称最多 64 个字符")
		}
		if item.URL == "" {
			if prev, ok := oldByID[item.ID]; ok {
				item.URL = prev.URL
			}
		}
		if item.URL == "" {
			return nil, pkg.ErrBadRequest("事件订阅 %q 的 URL 不能为空", item.Name)
		}
		if err := ValidateNotifyTargetURL(item.URL); err != nil {
			return nil, notifyURLValidationError("事件订阅", item.URL, err)
		}
		if len([]rune(item.Secret)) > 512 {
			return nil, pkg.ErrBadRequest("事件订阅签名密钥最多 512 个字符")
		}
		if item.ClearSecret {
			item.Secret = ""
		} else if item.Secret == "" {
			if prev, ok := oldByID[item.ID]; ok {
				item.Secret = prev.Secret
			}
		}
		item.Events, err = normalizeEventTypes(item.Events)
		if err != nil {
			return nil, err
		}
		item.HasURL = false
		item.HasSecret = false
		item.ClearSecret = false
		out = append(out, item)
	}
	if err := validateEventIDs(out); err != nil {
		return nil, err
	}
	if err := s.store.Save(out); err != nil {
		return nil, err
	}
	return out, nil
}

func validateEventIDs(items []model.EventSubscription) error {
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		if seen[item.ID] {
			return pkg.ErrBadRequest("事件订阅 ID 重复: %s", item.ID)
		}
		seen[item.ID] = true
	}
	return nil
}

// normalizeEventTypes 去重、校验并排序，返回空表示全部事件。
// 空列表在保存时按「全部事件」处理，避免用户勾选后误清空。
func normalizeEventTypes(events []string) ([]string, error) {
	if len(events) == 0 {
		return append([]string(nil), model.CIEventTypes...), nil
	}
	seen := make(map[string]bool, len(events))
	out := make([]string, 0, len(events))
	for _, raw := range events {
		event := strings.TrimSpace(raw)
		if event == "" || seen[event] {
			continue
		}
		if !model.IsValidCIEvent(event) {
			return nil, pkg.ErrBadRequest("不支持的事件类型: %s", event)
		}
		seen[event] = true
		out = append(out, event)
	}
	if len(out) == 0 {
		return append([]string(nil), model.CIEventTypes...), nil
	}
	sort.Strings(out)
	return out, nil
}

// Dispatch 同步投递；构建调用方应使用 DispatchAsync，避免网络抖动拖慢构建。
func (s *EventService) Dispatch(ctx context.Context, event model.CIEvent) {
	items, err := s.store.Load()
	if err != nil {
		logger.Warnf("[event] load subscriptions failed: %v", err)
		return
	}
	if len(items) == 0 {
		return
	}
	if event.Time.IsZero() {
		event.Time = s.now().UTC()
	}
	if event.ID == "" {
		event.ID = randomID()
	}
	for _, item := range items {
		if !item.Enabled || !eventMatches(item.Events, event.Type) {
			continue
		}
		if err := s.deliver(ctx, item, event); err != nil {
			logger.Warnf("[event] deliver %s to %q failed: %v", event.Type, item.Name, err)
		}
	}
}

// DispatchAsync 异步投递；失败只写日志，不影响构建。
func (s *EventService) DispatchAsync(event model.CIEvent) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		defer cancel()
		s.Dispatch(ctx, event)
	}()
}

// Test 向指定订阅发送一条测试事件；id 为空时向所有启用的订阅发送。
func (s *EventService) Test(ctx context.Context, id string) error {
	items, err := s.store.Load()
	if err != nil {
		return err
	}
	event := model.CIEvent{
		ID:       randomID(),
		Type:     "test",
		Time:     s.now().UTC(),
		Pipeline: "test-pipeline",
		JobName:  "事件订阅测试",
		Build:    "1",
		Status:   "success",
		Trigger:  string(model.TriggerManual),
	}
	var failed []string
	found := false
	for _, item := range items {
		if id != "" && item.ID != id {
			continue
		}
		if id == "" && !item.Enabled {
			continue
		}
		found = true
		if err := s.deliver(ctx, item, event); err != nil {
			failed = append(failed, fmt.Sprintf("%s: %v", item.Name, err))
		}
	}
	if id != "" && !found {
		return pkg.ErrNotFound("事件订阅不存在: %s", id)
	}
	if len(failed) > 0 {
		return pkg.ErrBadRequest("测试事件发送失败：%s", strings.Join(failed, "; "))
	}
	return nil
}

func eventMatches(events []string, event string) bool {
	if len(events) == 0 {
		return true
	}
	for _, item := range events {
		if item == event {
			return true
		}
	}
	return false
}

func (s *EventService) deliver(ctx context.Context, item model.EventSubscription, event model.CIEvent) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * time.Second):
			}
		}
		retry, err := s.post(ctx, item, event, payload)
		if err == nil {
			return nil
		}
		lastErr = err
		if !retry {
			return err
		}
	}
	return lastErr
}

func (s *EventService) post(ctx context.Context, item model.EventSubscription, event model.CIEvent, payload []byte) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, item.URL, bytes.NewReader(payload))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "ci-platform-events/1.0")
	req.Header.Set("X-CI-Event", event.Type)
	req.Header.Set("X-CI-Delivery", event.ID)
	if item.Secret != "" {
		mac := hmac.New(sha256.New, []byte(item.Secret))
		_, _ = mac.Write(payload)
		req.Header.Set("X-CI-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return true, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return false, nil
	}
	retry := resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests
	return retry, fmt.Errorf("返回 %s: %s", resp.Status, strings.TrimSpace(string(body)))
}
