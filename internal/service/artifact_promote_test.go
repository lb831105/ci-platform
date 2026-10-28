package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ci-platform/internal/model"
	"ci-platform/internal/pkg"
	"ci-platform/internal/store"
)

func TestArtifactPromoteCopiesAndVerifiesSHA256(t *testing.T) {
	jobsRoot := t.TempDir()
	builds := store.NewBuildStore(jobsRoot)
	ctx := context.Background()

	src := &model.Build{PipelineID: "build-job", Status: model.BuildSuccess}
	if err := builds.CreateNext(ctx, src); err != nil {
		t.Fatal(err)
	}
	dst := &model.Build{PipelineID: "deploy-job", Status: model.BuildSuccess}
	if err := builds.CreateNext(ctx, dst); err != nil {
		t.Fatal(err)
	}
	writeArtifact := func(pipelineID, buildID, rel, content string) model.ArtifactFile {
		root := filepath.Join(builds.BuildDir(pipelineID, buildID), model.ArtifactDirName)
		if err := pkg.EnsureDir(filepath.Dir(filepath.Join(root, filepath.FromSlash(rel)))); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256([]byte(content))
		return model.ArtifactFile{Path: rel, Size: int64(len(content)), SHA256: hex.EncodeToString(sum[:])}
	}
	app := writeArtifact("build-job", src.ID, "dist/app.js", "console.log(1)")
	manifest := model.ArtifactManifest{
		CreatedAt: time.Now().UTC(),
		TotalSize: app.Size,
		Files:     []model.ArtifactFile{app},
	}
	if err := pkg.WriteJSONAtomic(filepath.Join(builds.BuildDir("build-job", src.ID), model.ArtifactManifestName), manifest); err != nil {
		t.Fatal(err)
	}

	svc := NewArtifactService(builds)
	got, err := svc.Promote(ctx, "build-job", src.ID, "deploy-job", "", "", nil, false)
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	wantPath := "promoted/build-job-" + src.ID + "/dist/app.js"
	if len(got.Files) != 1 || got.Files[0].Path != wantPath || got.Files[0].SHA256 != app.SHA256 {
		t.Fatalf("manifest = %+v", got)
	}
	data, err := os.ReadFile(filepath.Join(builds.BuildDir("deploy-job", dst.ID), model.ArtifactDirName, filepath.FromSlash(wantPath)))
	if err != nil || string(data) != "console.log(1)" {
		t.Fatalf("copied data = %q, %v", data, err)
	}

	// 重复晋级默认拒绝；显式 overwrite 才覆盖。
	if _, err := svc.Promote(ctx, "build-job", src.ID, "deploy-job", dst.ID, "", nil, false); err == nil {
		t.Fatal("duplicate promote must require overwrite")
	}
	if _, err := svc.Promote(ctx, "build-job", src.ID, "deploy-job", dst.ID, "release", nil, true); err != nil {
		t.Fatalf("overwrite promote: %v", err)
	}
}

func TestArtifactPromoteDetectsSHAMismatch(t *testing.T) {
	jobsRoot := t.TempDir()
	builds := store.NewBuildStore(jobsRoot)
	ctx := context.Background()
	src := &model.Build{PipelineID: "build-job", Status: model.BuildSuccess}
	dst := &model.Build{PipelineID: "deploy-job", Status: model.BuildSuccess}
	_ = builds.CreateNext(ctx, src)
	_ = builds.CreateNext(ctx, dst)
	root := filepath.Join(builds.BuildDir("build-job", src.ID), model.ArtifactDirName)
	if err := pkg.EnsureDir(root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "app.bin"), []byte("real"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := model.ArtifactManifest{
		CreatedAt: time.Now().UTC(),
		TotalSize: 4,
		Files:     []model.ArtifactFile{{Path: "app.bin", Size: 4, SHA256: "deadbeef"}},
	}
	_ = pkg.WriteJSONAtomic(filepath.Join(builds.BuildDir("build-job", src.ID), model.ArtifactManifestName), manifest)

	svc := NewArtifactService(builds)
	if _, err := svc.Promote(ctx, "build-job", src.ID, "deploy-job", dst.ID, "", nil, false); err == nil {
		t.Fatal("sha mismatch must abort promotion")
	}
	if _, err := os.Stat(filepath.Join(builds.BuildDir("deploy-job", dst.ID), model.ArtifactDirName, "app.bin")); !os.IsNotExist(err) {
		t.Fatalf("failed promotion must not leave target file, err=%v", err)
	}
}
