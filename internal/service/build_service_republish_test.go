package service

import (
	"context"
	"io"
	"path/filepath"
	"testing"
	"time"

	"ci-platform/internal/executor"
	"ci-platform/internal/model"
	"ci-platform/internal/store"
)

type fakeRepublishPublisher struct {
	called   bool
	cfg      *model.SshPublishConfig
	buildDir string
}

func (f *fakeRepublishPublisher) Publish(context.Context, *model.SshPublishConfig, string, io.Writer) error {
	return nil
}

func (f *fakeRepublishPublisher) PublishArtifacts(_ context.Context, cfg *model.SshPublishConfig, buildDir string, _ io.Writer) error {
	f.called = true
	f.cfg = cfg
	f.buildDir = buildDir
	return nil
}

type fakeArtifactReader struct {
	manifest *model.ArtifactManifest
}

type fakeSettingsProvider struct {
	settings model.Settings
}

func (f *fakeSettingsProvider) Current() model.Settings { return f.settings }

func (f *fakeArtifactReader) Manifest(context.Context, string, string) (*model.ArtifactManifest, error) {
	return f.manifest, nil
}

func newRepublishService(t *testing.T) (*BuildService, *fakeRepublishPublisher) {
	t.Helper()
	pipelines := &memPipelineStore{items: map[string]*model.Pipeline{}}
	builds := store.NewBuildStore(t.TempDir())
	publisher := &fakeRepublishPublisher{}
	settings := &fakeSettingsProvider{settings: model.Settings{SshServers: []model.SshServer{
		{ID: "s1", Name: "prod", Host: "10.0.0.9", Port: 22, Username: "deploy", AuthType: "password", Password: "secret", RemoteBaseDir: "/srv/app"},
		{ID: "s2", Name: "stage", Host: "10.0.0.10", Port: 22, Username: "deploy", AuthType: "password", Password: "secret", RemoteBaseDir: "/srv/stage"},
	}}}
	svc := NewBuildService(pipelines, builds, nil, nil, publisher, executor.NewHub(), nil, settings, nil,
		&fakeArtifactReader{manifest: &model.ArtifactManifest{Files: []model.ArtifactFile{{Path: "out.txt"}}}})
	return svc, publisher
}

func TestRepublishPublishesArchivedArtifacts(t *testing.T) {
	svc, publisher := newRepublishService(t)
	// BuildService 依赖通过小写字段装配，这里直接用包内构造器注入 pipeline/build。
	svc.pipelines = &memPipelineStore{items: map[string]*model.Pipeline{
		"p1": {ID: "p1", Name: "demo", SshPublish: &model.SshPublishConfig{
			Enabled: true, ServerID: "s1", SourceFiles: []string{"*"},
		}},
	}}
	builds := store.NewBuildStore(t.TempDir())
	svc.builds = builds
	started := time.Now().UTC()
	if err := builds.Create(context.Background(), &model.Build{
		ID: "1", PipelineID: "p1", Status: model.BuildSuccess, StartedAt: &started,
	}); err != nil {
		t.Fatal(err)
	}
	err := svc.Republish(context.Background(), "p1", "1", "s1", "")
	if err != nil {
		t.Fatalf("republish: %v", err)
	}
	if !publisher.called {
		t.Fatal("publisher must be called")
	}
	if publisher.cfg == nil || publisher.cfg.ServerID != "s1" || !publisher.cfg.Enabled {
		t.Fatalf("unexpected cfg: %+v", publisher.cfg)
	}
	if filepath.Base(publisher.buildDir) != "1" {
		t.Fatalf("buildDir = %q", publisher.buildDir)
	}
}

func TestRepublishRejectsServerOutsideTask(t *testing.T) {
	svc, publisher := newRepublishService(t)
	svc.pipelines = &memPipelineStore{items: map[string]*model.Pipeline{
		"p1": {ID: "p1", Name: "demo", SshPublish: &model.SshPublishConfig{
			Enabled: true, ServerID: "s1", SourceFiles: []string{"*"},
		}},
	}}
	builds := store.NewBuildStore(t.TempDir())
	svc.builds = builds
	started := time.Now().UTC()
	if err := builds.Create(context.Background(), &model.Build{
		ID: "1", PipelineID: "p1", Status: model.BuildSuccess, StartedAt: &started,
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Republish(context.Background(), "p1", "1", "s2", ""); err == nil {
		t.Fatal("server outside task targets must be rejected")
	}
	if publisher.called {
		t.Fatal("publisher must not be called for a disallowed server")
	}
}

func TestRepublishRejectsFailedBuildAndNoArtifacts(t *testing.T) {
	svc, publisher := newRepublishService(t)
	svc.pipelines = &memPipelineStore{items: map[string]*model.Pipeline{
		"p1": {ID: "p1", Name: "demo", SshPublish: &model.SshPublishConfig{Enabled: true, ServerID: "s1"}},
	}}
	builds := store.NewBuildStore(t.TempDir())
	svc.builds = builds
	if err := builds.Create(context.Background(), &model.Build{
		ID: "1", PipelineID: "p1", Status: model.BuildFailed,
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Republish(context.Background(), "p1", "1", "", ""); err == nil {
		t.Fatal("failed build must not be republished")
	}
	if publisher.called {
		t.Fatal("publisher must not be called for a failed build")
	}

	svc.artifactReader = &fakeArtifactReader{manifest: &model.ArtifactManifest{}}
	if err := builds.Update(context.Background(), &model.Build{
		ID: "1", PipelineID: "p1", Status: model.BuildSuccess,
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Republish(context.Background(), "p1", "1", "", ""); err == nil {
		t.Fatal("empty manifest must not be republished")
	}
}
