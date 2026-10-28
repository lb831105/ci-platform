package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"ci-platform/internal/config"
)

func TestCopyDataEntriesCopiesMigrationMarkers(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	for _, name := range []string{".workspace-reuse-v1", ".artifact-config-v1", "credentials.key", "tokens.json"} {
		if err := os.WriteFile(filepath.Join(src, name), []byte("done"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := copyDataEntries(context.Background(), src, dst); err != nil {
		t.Fatalf("copy: %v", err)
	}
	for _, name := range []string{".workspace-reuse-v1", ".artifact-config-v1", "credentials.key", "tokens.json"} {
		if _, err := os.Stat(filepath.Join(dst, name)); err != nil {
			t.Fatalf("marker %s not copied: %v", name, err)
		}
	}
}

func TestUpdateDataHomeRejectsNestedTargets(t *testing.T) {
	current := t.TempDir()
	cfg := &config.Config{DataHome: current, ConfigFile: filepath.Join(t.TempDir(), "ci-platform.json")}
	svc := &SystemService{cfg: cfg}
	if _, err := svc.UpdateDataHome(context.Background(), filepath.Join(current, "nested"), true); err == nil {
		t.Fatal("nested data home must be rejected")
	}
	if _, err := svc.UpdateDataHome(context.Background(), filepath.Dir(current), true); err == nil {
		t.Fatal("parent data home must be rejected")
	}
}
