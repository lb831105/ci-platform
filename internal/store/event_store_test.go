package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ci-platform/internal/model"
)

func TestEventStoreEncryptsURLAndSecret(t *testing.T) {
	dir := t.TempDir()
	store := NewEventStore(dir)
	items := []model.EventSubscription{{
		ID: "e1", Name: "ticket", URL: "https://example.com/hook?token=plain-token",
		Secret: "hmac-plain", Events: []string{model.CIEventBuildFinished}, Enabled: true,
	}}
	if err := store.Save(items); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "events.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, plain := range []string{"plain-token", "hmac-plain"} {
		if strings.Contains(string(raw), plain) {
			t.Fatalf("events.json leaked %q: %s", plain, raw)
		}
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || loaded[0].URL != items[0].URL || loaded[0].Secret != items[0].Secret {
		t.Fatalf("roundtrip = %+v", loaded)
	}
	if loaded[0].HasURL || loaded[0].HasSecret {
		t.Fatalf("view flags must not be persisted: %+v", loaded[0])
	}
}
