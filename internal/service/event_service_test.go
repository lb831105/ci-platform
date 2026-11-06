package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"ci-platform/internal/model"
)

type memEventStore struct {
	mu    sync.Mutex
	items []model.EventSubscription
}

func (m *memEventStore) Load() ([]model.EventSubscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]model.EventSubscription(nil), m.items...), nil
}

func (m *memEventStore) Save(items []model.EventSubscription) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items = append([]model.EventSubscription(nil), items...)
	return nil
}

func TestEventServiceDispatchSignsAndFilters(t *testing.T) {
	var got int
	var body model.CIEvent
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got++
		if r.Header.Get("X-CI-Event") != model.CIEventBuildFinished {
			t.Errorf("event header = %q", r.Header.Get("X-CI-Event"))
		}
		payload, _ := io.ReadAll(r.Body)
		mac := hmac.New(sha256.New, []byte("s3cret"))
		_, _ = mac.Write(payload)
		want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
		if r.Header.Get("X-CI-Signature") != want {
			t.Errorf("signature = %q want %q", r.Header.Get("X-CI-Signature"), want)
		}
		if err := json.Unmarshal(payload, &body); err != nil {
			t.Errorf("payload: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	store := &memEventStore{items: []model.EventSubscription{{
		ID: "e1", Name: "ticket", URL: srv.URL, Secret: "s3cret",
		Events: []string{model.CIEventBuildFinished}, Enabled: true,
	}}}
	svc := NewEventService(store)
	svc.Dispatch(context.Background(), model.CIEvent{
		Type: model.CIEventBuildFinished, Pipeline: "p1", JobName: "demo", Build: "7", Status: "success",
	})
	if got != 1 {
		t.Fatalf("deliveries = %d", got)
	}
	if body.JobName != "demo" || body.Build != "7" || body.Status != "success" {
		t.Fatalf("payload = %+v", body)
	}
	// 不匹配的事件不应投递。
	svc.Dispatch(context.Background(), model.CIEvent{Type: model.CIEventBuildStarted})
	if got != 1 {
		t.Fatalf("unmatched event delivered, got=%d", got)
	}
}

func TestEventServiceRejectsPrivateURLAndMergesSecrets(t *testing.T) {
	store := &memEventStore{items: []model.EventSubscription{{
		ID: "e1", Name: "old", URL: "https://example.com/hook", Secret: "keep", Events: []string{model.CIEventBuildFinished}, Enabled: true,
	}}}
	svc := NewEventService(store)
	if _, err := svc.Update([]model.EventSubscription{{Name: "bad", URL: "http://127.0.0.1:8080/hook"}}); err == nil {
		t.Fatal("loopback URL must be rejected")
	}
	items, err := svc.Update([]model.EventSubscription{{
		ID: "e1", Name: "old", URL: "", Secret: "", Events: []string{model.CIEventBuildFinished}, Enabled: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if items[0].URL != "https://example.com/hook" || items[0].Secret != "keep" {
		t.Fatalf("blank fields should keep old values: %+v", items[0])
	}
	items, err = svc.Update([]model.EventSubscription{{
		ID: "e1", Name: "old", URL: "", Secret: "", ClearSecret: true, Events: []string{model.CIEventBuildFinished}, Enabled: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if items[0].Secret != "" {
		t.Fatalf("clear_secret should clear secret: %+v", items[0])
	}
}
