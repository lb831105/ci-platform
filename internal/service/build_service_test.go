package service

import (
	"os"
	"path/filepath"
	"runtime"
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
	// PATH 的键名在 Windows 上是 Path，用 envPath 读取以兼容两种写法。
	pathValue := envPath(env)
	if !strings.HasPrefix(pathValue, filepath.Join(goHome, "bin")+sep) {
		t.Fatalf("PATH must start with go bin: %q", pathValue)
	}
	if !strings.Contains(pathValue, filepath.Join(jdkHome, "bin")) {
		t.Fatalf("PATH missing jdk bin: %q", pathValue)
	}
	// Node（尤其 Windows）可执行文件可能在主目录本身。
	if !strings.Contains(pathValue, filepath.FromSlash("/opt/node")) {
		t.Fatalf("PATH missing node home: %q", pathValue)
	}
	if strings.Contains(pathValue, "go-other") {
		t.Fatalf("only first installation per category should be used: %q", pathValue)
	}
	if !strings.HasSuffix(pathValue, sep+"/usr/bin") {
		t.Fatalf("PATH must keep original entries: %q", pathValue)
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
	// 对齐 Jenkins Publish Over SSH：任务级目录相对服务器默认目录拼接。
	if err != nil || got.RemoteBaseDir != "/srv/app/custom" {
		t.Fatalf("task remote dir should be joined with server default: %+v, %v", got, err)
	}

	// 相对目录同样拼接。
	relative := &model.SshPublishConfig{Enabled: true, ServerID: "s1", RemoteBaseDir: "gomysqldump"}
	got, err = resolveSshConfig(relative, settings)
	if err != nil || got.RemoteBaseDir != "/srv/app/gomysqldump" {
		t.Fatalf("relative remote dir should be joined: %+v, %v", got, err)
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

// Windows 上 PATH 的键名是 Path，且不能同时存在 PATH/Path（子进程取哪个未定义）。
func TestInjectLocalToolHomesUsesPlatformPathKey(t *testing.T) {
	settings := model.Settings{ToolCategories: []model.ToolCategory{
		{ID: "jdk", Installations: []model.ToolInstallation{{Name: "JDK", Home: "/opt/jdk-17"}}},
	}}
	env := map[string]string{"PATH": "/usr/bin", "Path": "/usr/bin"}
	injectLocalToolHomes(settings, env)

	pathKeys := 0
	for key := range env {
		if strings.EqualFold(key, "PATH") {
			pathKeys++
		}
	}
	if pathKeys != 1 {
		t.Fatalf("exactly one PATH-like key expected, got %d (%v)", pathKeys, env)
	}
	wantKey := "PATH"
	if runtime.GOOS == "windows" {
		wantKey = "Path"
	}
	if _, ok := env[wantKey]; !ok {
		t.Fatalf("expected key %q on %s, got %v", wantKey, runtime.GOOS, env)
	}
}

// Git 在 Windows 上的可执行文件在 cmd/ 下，PATH 要包含它。
func TestToolBinDirsPerCategory(t *testing.T) {
	dirs := toolBinDirs("git", filepath.FromSlash("/opt/git"))
	if runtime.GOOS == "windows" {
		if filepath.Base(dirs[0]) != "cmd" {
			t.Fatalf("windows git must put cmd first: %v", dirs)
		}
	} else if filepath.Base(dirs[0]) != "bin" {
		t.Fatalf("linux git must put bin first: %v", dirs)
	}
	nodeDirs := toolBinDirs("nodejs", filepath.FromSlash("/opt/node"))
	if nodeDirs[0] != filepath.FromSlash("/opt/node") {
		t.Fatalf("node home itself must come first: %v", nodeDirs)
	}
}
