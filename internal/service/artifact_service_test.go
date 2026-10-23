package service

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ci-platform/internal/model"
	"ci-platform/internal/pkg"
	"ci-platform/internal/store"
)

func TestArtifactServiceListDownloadAndZip(t *testing.T) {
	jobsRoot := t.TempDir()
	builds := store.NewBuildStore(jobsRoot)
	b := &model.Build{PipelineID: "1", Status: model.BuildRunning}
	if err := builds.CreateNext(context.Background(), b); err != nil {
		t.Fatalf("create build: %v", err)
	}
	buildDir := builds.BuildDir("1", b.ID)
	root := filepath.Join(buildDir, model.ArtifactDirName)
	if err := pkg.EnsureDir(filepath.Join(root, "dist")); err != nil {
		t.Fatal(err)
	}
	write := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("app.exe", "v1")
	write("dist/app.js", "js")
	manifest := model.ArtifactManifest{CreatedAt: time.Now().UTC(), TotalSize: 5, Files: []model.ArtifactFile{
		{Path: "app.exe", Size: 2},
		{Path: "dist/app.js", Size: 2},
	}}
	if err := pkg.WriteJSONAtomic(filepath.Join(buildDir, model.ArtifactManifestName), manifest); err != nil {
		t.Fatal(err)
	}

	svc := NewArtifactService(builds)
	got, err := svc.Manifest(context.Background(), "1", b.ID)
	if err != nil || len(got.Files) != 2 {
		t.Fatalf("manifest = %+v, %v", got, err)
	}
	file, artifact, err := svc.Open(context.Background(), "1", b.ID, "dist/app.js")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	data, _ := io.ReadAll(file)
	_ = file.Close()
	if string(data) != "js" || artifact.Path != "dist/app.js" {
		t.Fatalf("unexpected artifact: %q %+v", data, artifact)
	}
	if _, _, err = svc.Open(context.Background(), "1", b.ID, "../config.json"); err == nil {
		t.Fatal("traversal path must be rejected")
	}
	if _, _, err = svc.Open(context.Background(), "1", b.ID, "missing.txt"); err == nil {
		t.Fatal("unknown artifact must be rejected")
	}

	var buf bytes.Buffer
	if err = svc.WriteZip(context.Background(), "1", b.ID, got, &buf); err != nil {
		t.Fatalf("zip: %v", err)
	}
	reader, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("read zip: %v", err)
	}
	if len(reader.File) != 2 {
		t.Fatalf("zip entries = %d, want 2", len(reader.File))
	}
}

func TestArtifactServiceRejectsUnsafeBuildKey(t *testing.T) {
	svc := NewArtifactService(store.NewBuildStore(t.TempDir()))
	if _, err := svc.Manifest(context.Background(), "..", "1"); err == nil {
		t.Fatal("unsafe pipeline id must be rejected")
	}
}
