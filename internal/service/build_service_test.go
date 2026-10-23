package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ci-platform/internal/model"
)

func TestInjectLocalToolHomes(t *testing.T) {
	settings := model.Settings{ToolCategories: []model.ToolCategory{
		{ID: "golang", Installations: []model.ToolInstallation{
			{Name: "Go", Home: "/opt/go"},
			{Name: "Go other", Home: "/opt/go-other"},
		}},
		{ID: "jdk", Installations: []model.ToolInstallation{{Name: "JDK", Home: "/opt/jdk-17"}}},
		{ID: "nodejs", Installations: []model.ToolInstallation{{Name: "Node", Home: "/opt/node"}}},
		{ID: "maven", Installations: []model.ToolInstallation{{Name: "Maven (no home)"}}},
	}}
	env := map[string]string{"PATH": "/usr/bin"}
	injectLocalToolHomes(settings, env)

	goHome := filepath.FromSlash("/opt/go")
	jdkHome := filepath.FromSlash("/opt/jdk-17")
	if env["GOROOT"] != goHome {
		t.Fatalf("GOROOT = %q, want %q", env["GOROOT"], goHome)
	}
	if env["JAVA_HOME"] != jdkHome {
		t.Fatalf("JAVA_HOME = %q, want %q", env["JAVA_HOME"], jdkHome)
	}
	if env["NODE_HOME"] != filepath.FromSlash("/opt/node") {
		t.Fatalf("NODE_HOME = %q", env["NODE_HOME"])
	}
	if _, ok := env["MAVEN_HOME"]; ok {
		t.Fatal("installation without home must not set MAVEN_HOME")
	}
	sep := string(os.PathListSeparator)
	if !strings.HasPrefix(env["PATH"], filepath.Join(goHome, "bin")+sep) {
		t.Fatalf("PATH must start with go bin: %q", env["PATH"])
	}
	if !strings.Contains(env["PATH"], filepath.Join(jdkHome, "bin")) {
		t.Fatalf("PATH missing jdk bin: %q", env["PATH"])
	}
	// Node（尤其 Windows）可执行文件可能在主目录本身。
	if !strings.Contains(env["PATH"], filepath.FromSlash("/opt/node")) {
		t.Fatalf("PATH missing node home: %q", env["PATH"])
	}
	if strings.Contains(env["PATH"], "go-other") {
		t.Fatalf("only first installation per category should be used: %q", env["PATH"])
	}
	if !strings.HasSuffix(env["PATH"], sep+"/usr/bin") {
		t.Fatalf("PATH must keep original entries: %q", env["PATH"])
	}
}

// 任务引用全局 SSH 服务器时，构建前把连接信息合并进来；旧任务直接透传。
func TestResolveSshConfig(t *testing.T) {
	settings := model.Settings{SshServers: []model.SshServer{
		{ID: "s1", Name: "prod", Host: "deploy.example.com", Port: 2222, Username: "deploy", AuthType: "password", Password: "secret", RemoteBaseDir: "/srv/app"},
	}}

	spec := &model.SshPublishConfig{Enabled: true, ServerID: "s1", SourceFiles: []string{"dist/**"}}
	got, err := resolveSshConfig(spec, settings)
	if err != nil {
		t.Fatalf("resolveSshConfig: %v", err)
	}
	if got.Host != "deploy.example.com" || got.Port != 2222 || got.Username != "deploy" || got.Password != "secret" {
		t.Fatalf("unexpected resolved config: %+v", got)
	}
	if got.RemoteBaseDir != "/srv/app" {
		t.Fatalf("remote dir = %q, want server default", got.RemoteBaseDir)
	}

	override := &model.SshPublishConfig{Enabled: true, ServerID: "s1", RemoteBaseDir: "/custom"}
	got, err = resolveSshConfig(override, settings)
	if err != nil || got.RemoteBaseDir != "/custom" {
		t.Fatalf("task remote dir override not applied: %+v, %v", got, err)
	}

	if _, err = resolveSshConfig(&model.SshPublishConfig{ServerID: "missing"}, settings); err == nil {
		t.Fatal("missing server must return an error")
	}

	legacy := &model.SshPublishConfig{Enabled: true, Host: "old", Username: "u", SourceFiles: []string{"a"}}
	got, err = resolveSshConfig(legacy, settings)
	if err != nil || got != legacy {
		t.Fatalf("legacy config should pass through unchanged")
	}
}
