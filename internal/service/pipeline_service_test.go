package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"ci-platform/internal/model"
)

// 引用全局 SSH 服务器时：只需发布参数，旧版内联的账号/密码会被清除。
func TestValidateSshPublishByServer(t *testing.T) {
	cfg := &model.SshPublishConfig{
		Enabled:        true,
		ServerID:       "s1",
		Host:           "legacy-host",
		Username:       "legacy-user",
		AuthType:       "password",
		Password:       "legacy-pass",
		PrivateKeyPath: "/legacy/key",
		SourceFiles:    []string{"dist/**", " ", ""},
	}
	if err := validateSshPublish(cfg); err != nil {
		t.Fatalf("server-based config rejected: %v", err)
	}
	if cfg.Host != "" || cfg.Username != "" || cfg.Password != "" || cfg.PrivateKeyPath != "" {
		t.Fatalf("legacy inline fields should be cleared: %+v", cfg)
	}
	if len(cfg.SourceFiles) != 1 || cfg.SourceFiles[0] != "dist/**" {
		t.Fatalf("source files = %v", cfg.SourceFiles)
	}

	noFiles := &model.SshPublishConfig{Enabled: true, ServerID: "s1"}
	if err := validateSshPublish(noFiles); err == nil {
		t.Fatal("server-based config without source_files must be rejected")
	}
}

// 旧版内联配置仍按原有规则校验。
func TestValidateSshPublishLegacy(t *testing.T) {
	cfg := &model.SshPublishConfig{
		Enabled:       true,
		Host:          "h",
		Username:      "u",
		AuthType:      "password",
		Password:      "p",
		RemoteBaseDir: "/opt/app",
		SourceFiles:   []string{"app.jar"},
	}
	if err := validateSshPublish(cfg); err != nil {
		t.Fatalf("legacy config rejected: %v", err)
	}
	if cfg.Port != 22 {
		t.Fatalf("port = %d, want 22", cfg.Port)
	}
}

type memPipelineStore struct {
	items map[string]*model.Pipeline
}

func (m *memPipelineStore) Exists(id string) bool { _, ok := m.items[id]; return ok }

func (m *memPipelineStore) Create(_ context.Context, p *model.Pipeline) error {
	m.items[p.ID] = p
	return nil
}

func (m *memPipelineStore) Get(_ context.Context, id string) (*model.Pipeline, error) {
	return m.items[id], nil
}

func (m *memPipelineStore) List(_ context.Context) ([]*model.Pipeline, error) {
	out := make([]*model.Pipeline, 0, len(m.items))
	for _, p := range m.items {
		out = append(out, p)
	}
	return out, nil
}

func (m *memPipelineStore) Update(_ context.Context, p *model.Pipeline) error {
	copied := *p
	m.items[p.ID] = &copied
	return nil
}

func (m *memPipelineStore) Delete(_ context.Context, id string) error {
	delete(m.items, id)
	return nil
}

// 一次性迁移只把“带 Git 且当前会清空工作区”的任务改为复用。
func TestMigrateWorkspacePolicy(t *testing.T) {
	yes := true
	no := false
	store := &memPipelineStore{items: map[string]*model.Pipeline{
		"git-true":   {ID: "git-true", GitURL: "https://example.com/a.git", DeleteWorkspaceBeforeBuild: &yes},
		"git-false":  {ID: "git-false", GitURL: "https://example.com/b.git", DeleteWorkspaceBeforeBuild: &no},
		"git-nil":    {ID: "git-nil", GitURL: "https://example.com/c.git"},
		"nogit-true": {ID: "nogit-true", DeleteWorkspaceBeforeBuild: &yes},
	}}
	svc := &PipelineService{store: store}
	n, err := svc.MigrateWorkspacePolicy(context.Background())
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if n != 2 {
		t.Fatalf("migrated = %d, want 2", n)
	}
	if store.items["git-true"].ShouldCleanWorkspace() || store.items["git-nil"].ShouldCleanWorkspace() {
		t.Fatal("Git pipelines should reuse workspace after migration")
	}
	if !store.items["nogit-true"].ShouldCleanWorkspace() {
		t.Fatal("non-Git pipeline should keep its explicit setting")
	}
	again, err := svc.MigrateWorkspacePolicy(context.Background())
	if err != nil || again != 0 {
		t.Fatalf("second migration = %d, %v; want 0, nil", again, err)
	}
}

func TestCreateAssignsWorkspaceDirAndRejectsDuplicate(t *testing.T) {
	store := &memPipelineStore{items: map[string]*model.Pipeline{}}
	svc := &PipelineService{store: store, workspace: NewWorkspaceManager(t.TempDir())}
	ctx := context.Background()
	step := []model.Step{{Name: "build", Run: "echo ok"}}

	p1, err := svc.Create(ctx, &model.Pipeline{Name: "demo", Steps: step})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if p1.WorkspaceDir != "demo" {
		t.Fatalf("WorkspaceDir = %q, want demo", p1.WorkspaceDir)
	}
	if _, err = svc.Create(ctx, &model.Pipeline{Name: "demo", Steps: step}); err == nil {
		t.Fatal("duplicate name should be rejected")
	}
}

func TestMigrateWorkspaceDirs(t *testing.T) {
	root := t.TempDir()
	store := &memPipelineStore{items: map[string]*model.Pipeline{
		"1001": {ID: "1001", Name: "my-project", Steps: []model.Step{{Name: "s", Run: "echo"}}},
		"1002": {ID: "1002", Name: "dup", Steps: []model.Step{{Name: "s", Run: "echo"}}},
		"1003": {ID: "1003", Name: "dup", Steps: []model.Step{{Name: "s", Run: "echo"}}},
	}}
	// 旧工作区按 ID 命名。
	for _, id := range []string{"1001", "1002", "1003"} {
		dir := filepath.Join(root, id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "artifact.txt"), []byte(id), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	svc := &PipelineService{store: store, workspace: NewWorkspaceManager(root)}
	n, err := svc.MigrateWorkspaceDirs(context.Background())
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if n != 3 {
		t.Fatalf("migrated = %d, want 3", n)
	}
	if store.items["1001"].WorkspaceDir != "my-project" {
		t.Fatalf("1001 dir = %q", store.items["1001"].WorkspaceDir)
	}
	if store.items["1002"].WorkspaceDir != "dup" {
		t.Fatalf("1002 dir = %q", store.items["1002"].WorkspaceDir)
	}
	if store.items["1003"].WorkspaceDir != "dup-1003" {
		t.Fatalf("1003 dir = %q", store.items["1003"].WorkspaceDir)
	}
	for id, dir := range map[string]string{"1001": "my-project", "1002": "dup", "1003": "dup-1003"} {
		if _, err := os.Stat(filepath.Join(root, dir, "artifact.txt")); err != nil {
			t.Fatalf("%s artifact missing in %s: %v", id, dir, err)
		}
	}
	if again, err := svc.MigrateWorkspaceDirs(context.Background()); err != nil || again != 0 {
		t.Fatalf("second migration = %d, %v; want 0, nil", again, err)
	}
}

func TestUpdateRenamesWorkspaceDir(t *testing.T) {
	root := t.TempDir()
	store := &memPipelineStore{items: map[string]*model.Pipeline{
		"1001": {ID: "1001", Name: "old", WorkspaceDir: "old", Steps: []model.Step{{Name: "s", Run: "echo"}}},
	}}
	oldDir := filepath.Join(root, "old")
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldDir, "artifact.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := &PipelineService{store: store, workspace: NewWorkspaceManager(root)}
	if _, err := svc.Update(context.Background(), "1001", &model.Pipeline{
		Name:  "new",
		Steps: []model.Step{{Name: "s", Run: "echo"}},
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if store.items["1001"].WorkspaceDir != "new" {
		t.Fatalf("WorkspaceDir = %q, want new", store.items["1001"].WorkspaceDir)
	}
	if _, err := os.Stat(filepath.Join(root, "new", "artifact.txt")); err != nil {
		t.Fatalf("artifact missing after rename: %v", err)
	}
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Fatalf("old dir should be gone, err = %v", err)
	}
}

func TestArtifactConfigValidation(t *testing.T) {
	p := &model.Pipeline{
		Name:      "with-artifacts",
		Steps:     []model.Step{{Name: "s", Run: "echo"}},
		Artifacts: model.ArtifactConfig{Enabled: true},
	}
	if err := normalizeAndValidate(p, ""); err == nil {
		t.Fatal("enabled artifacts without patterns must be rejected")
	}
	p.Artifacts.Files = []string{"../secret"}
	if err := normalizeAndValidate(p, ""); err == nil {
		t.Fatal("traversal artifact pattern must be rejected")
	}
	p.Artifacts.Files = []string{" target/*.jar ", "target/*.jar", ""}
	if err := normalizeAndValidate(p, ""); err != nil {
		t.Fatalf("valid artifact config: %v", err)
	}
	if len(p.Artifacts.Files) != 1 || p.Artifacts.Files[0] != "target/*.jar" {
		t.Fatalf("patterns not normalized: %v", p.Artifacts.Files)
	}
}

func TestInferArtifactPatterns(t *testing.T) {
	cases := []struct {
		run  string
		want []string
	}{
		{"go build -o gomysqldump.exe main.go", []string{"gomysqldump.exe"}},
		{"go build -o app .", []string{"app", "app.exe"}},
		{"go build -o 'bin/my app' .", []string{"bin/my app", "bin/my app.exe"}},
		{"jar cf hello.jar Hello.class", []string{"hello.jar"}},
		{"go build -o ../outside .", nil},
		{"mvn -B package", nil},
	}
	for _, c := range cases {
		got := inferArtifactPatterns([]model.Step{{Name: "s", Run: c.run}})
		if len(got) != len(c.want) {
			t.Fatalf("%q = %v, want %v", c.run, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("%q = %v, want %v", c.run, got, c.want)
			}
		}
	}
}

func TestMigrateArtifactConfig(t *testing.T) {
	store := &memPipelineStore{items: map[string]*model.Pipeline{
		"1": {ID: "1", Name: "go-app", Steps: []model.Step{{Name: "build", Run: "go build -o gomysqldump.exe main.go"}}},
		"2": {ID: "2", Name: "manual", Steps: []model.Step{{Name: "build", Run: "echo hi"}}},
		"3": {ID: "3", Name: "already", Steps: []model.Step{{Name: "build", Run: "go build -o other ."}},
			Artifacts: model.ArtifactConfig{Enabled: false, Files: []string{"keep.bin"}}},
	}}
	svc := &PipelineService{store: store}
	n, err := svc.MigrateArtifactConfig(context.Background())
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if n != 1 {
		t.Fatalf("migrated = %d, want 1", n)
	}
	if !store.items["1"].Artifacts.Enabled || store.items["1"].Artifacts.Files[0] != "gomysqldump.exe" {
		t.Fatalf("pipeline 1 artifacts = %+v", store.items["1"].Artifacts)
	}
	if store.items["2"].Artifacts.Enabled {
		t.Fatal("pipeline without explicit output must stay disabled")
	}
	if store.items["3"].Artifacts.Enabled || store.items["3"].Artifacts.Files[0] != "keep.bin" {
		t.Fatal("existing artifact config must be respected")
	}
	if again, err := svc.MigrateArtifactConfig(context.Background()); err != nil || again != 0 {
		t.Fatalf("second migration = %d, %v; want 0, nil", again, err)
	}
}
