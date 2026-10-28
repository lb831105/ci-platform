package executor

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"ci-platform/internal/model"
	"ci-platform/internal/pkg"
)

func TestArtifactCollectorArchivesEachBuild(t *testing.T) {
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, "dist", "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(rel, content string) {
		full := filepath.Join(workspace, filepath.FromSlash(rel))
		if err := pkg.EnsureDir(filepath.Dir(full)); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("app.exe", "binary-v1")
	write("dist/app.js", "js-v1")
	write("dist/assets/logo.txt", "logo")
	write("README.md", "docs")

	buildDir := t.TempDir()
	var out bytes.Buffer
	files, err := NewArtifactCollector().Collect(context.Background(), workspace, buildDir, []string{"app.exe", "dist/**"}, ArtifactLimits{}, false, &out)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(files) != 3 {
		t.Fatalf("files = %d (%v), want 3", len(files), files)
	}
	for rel, want := range map[string]string{
		"app.exe":              "binary-v1",
		"dist/app.js":          "js-v1",
		"dist/assets/logo.txt": "logo",
	} {
		data, readErr := os.ReadFile(filepath.Join(buildDir, model.ArtifactDirName, filepath.FromSlash(rel)))
		if readErr != nil {
			t.Fatalf("read archived %s: %v", rel, readErr)
		}
		if string(data) != want {
			t.Fatalf("archived %s = %q, want %q", rel, data, want)
		}
	}
	if _, err = os.Stat(filepath.Join(buildDir, model.ArtifactDirName, "README.md")); !os.IsNotExist(err) {
		t.Fatalf("README.md must not be archived, err = %v", err)
	}
	var manifest model.ArtifactManifest
	if err = pkg.ReadJSON(filepath.Join(buildDir, model.ArtifactManifestName), &manifest); err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if len(manifest.Files) != 3 || manifest.TotalSize == 0 {
		t.Fatalf("unexpected manifest: %+v", manifest)
	}
	if manifest.Files[0].SHA256 == "" {
		t.Fatal("sha256 must be recorded")
	}
}

func TestArtifactCollectorRejectsInvalidPattern(t *testing.T) {
	workspace := t.TempDir()
	_, err := NewArtifactCollector().Collect(context.Background(), workspace, t.TempDir(), []string{"../secret"}, ArtifactLimits{}, false, &bytes.Buffer{})
	if err == nil {
		t.Fatal("path traversal pattern must be rejected")
	}
}

func TestArtifactCollectorRequiresMatch(t *testing.T) {
	workspace := t.TempDir()
	_, err := NewArtifactCollector().Collect(context.Background(), workspace, t.TempDir(), []string{"missing/*.jar"}, ArtifactLimits{}, false, &bytes.Buffer{})
	if err == nil {
		t.Fatal("no matching artifacts must fail the build")
	}
}

func TestArtifactCollectorEnforcesLimits(t *testing.T) {
	workspace := t.TempDir()
	for _, name := range []string{"a.bin", "b.bin"} {
		if err := os.WriteFile(filepath.Join(workspace, name), []byte("0123456789"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	collector := NewArtifactCollector()
	if _, err := collector.Collect(context.Background(), workspace, t.TempDir(), []string{"*.bin"},
		ArtifactLimits{MaxFiles: 1}, false, &bytes.Buffer{}); err == nil {
		t.Fatal("max files limit must reject the archive")
	}
	if _, err := collector.Collect(context.Background(), workspace, t.TempDir(), []string{"*.bin"},
		ArtifactLimits{MaxTotalBytes: 5}, false, &bytes.Buffer{}); err == nil {
		t.Fatal("max total size limit must reject the archive")
	}
	if _, err := collector.Collect(context.Background(), workspace, t.TempDir(), []string{"*.bin"},
		ArtifactLimits{MaxFiles: 2, MaxTotalBytes: 20}, false, &bytes.Buffer{}); err != nil {
		t.Fatalf("limits should allow this archive: %v", err)
	}
}
