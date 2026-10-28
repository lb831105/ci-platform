package tools

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// makeTarGz 构造一个「顶层只有一个目录」的 tar.gz，模拟 JDK/Maven 发行包。
func makeTarGz(t *testing.T, top, script string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	write := func(name string, body []byte, mode int64, typeflag byte) {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(body)), Typeflag: typeflag}); err != nil {
			t.Fatal(err)
		}
		if len(body) > 0 {
			if _, err := tw.Write(body); err != nil {
				t.Fatal(err)
			}
		}
	}
	write(top+"/", nil, 0o755, tar.TypeDir)
	write(top+"/bin/", nil, 0o755, tar.TypeDir)
	write(top+"/bin/tool", []byte(script), 0o755, tar.TypeReg)
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func makeZip(t *testing.T, top, script string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	header := &zip.FileHeader{Name: top + "/bin/tool.cmd", Method: zip.Deflate}
	header.SetMode(0o755)
	w, err := zw.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(script)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// withTestURL 把某个工具的下载地址临时指向本地测试服务器。
func withTestURL(t *testing.T, version, url string) {
	t.Helper()
	// 下载地址按版本号覆盖：测试里用本地 httptest，不联网。
	setTestURL(version, url)
	t.Cleanup(func() { setTestURL(version, "") })
}

func TestEnsureDownloadsAndExtractsTarGz(t *testing.T) {
	payload := makeTarGz(t, "jdk-17.0.12+7", "#!/bin/sh\necho java\n")
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	withTestURL(t, "17.0.12+7", srv.URL+"/jdk.tar.gz")
	root := t.TempDir()
	installer := NewInstaller(root, "", nil)

	home, err := installer.Ensure(context.Background(), "jdk", "17.0.12+7")
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if filepath.Base(home) != "jdk-17.0.12+7" {
		t.Fatalf("home must be the single top dir, got %s", home)
	}
	if _, err := os.Stat(filepath.Join(home, "bin", "tool")); err != nil {
		t.Fatalf("extracted file missing: %v", err)
	}

	// 幂等：第二次不再下载
	if _, err := installer.Ensure(context.Background(), "jdk", "17.0.12+7"); err != nil {
		t.Fatalf("second ensure: %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("expected single download, got %d", got)
	}
}

func TestEnsureExtractsZip(t *testing.T) {
	payload := makeZip(t, "node-v20.18.0-win-x64", "@echo npm")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	withTestURL(t, "20.18.0", srv.URL+"/node.zip")
	installer := NewInstaller(t.TempDir(), "", nil)
	home, err := installer.Ensure(context.Background(), "nodejs", "20.18.0")
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "bin", "tool.cmd")); err != nil {
		t.Fatalf("zip entry missing: %v", err)
	}
}

// 下载失败必须报错且不留下半个工具（否则会被误认为已安装）。
func TestEnsureFailureLeavesNothingInstalled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusBadGateway)
	}))
	defer srv.Close()

	withTestURL(t, "20.99.0", srv.URL+"/node.tar.gz")
	installer := NewInstaller(t.TempDir(), "", nil)
	if _, err := installer.Ensure(context.Background(), "nodejs", "20.99.0"); err == nil {
		t.Fatal("must fail on non-200")
	}
	if _, ok := installer.Home("nodejs", "20.99.0"); ok {
		t.Fatal("failed install must not be marked as installed")
	}
}

// 拒绝 ZIP 里的目录穿越路径。
func TestExtractRejectsZipSlip(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("../../evil.sh")
	_, _ = w.Write([]byte("pwned"))
	_ = zw.Close()

	dir := t.TempDir()
	archive := filepath.Join(dir, "evil.zip")
	if err := os.WriteFile(archive, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := extract(archive, dir); err == nil {
		t.Fatal("zip slip must be rejected")
	}
}

func TestApplyMirror(t *testing.T) {
	raw := "https://nodejs.org/dist/v20.18.0/node-v20.18.0-linux-x64.tar.gz"
	if got := ApplyMirror(raw, ""); got != raw {
		t.Fatalf("empty mirror must not change url, got %s", got)
	}
	want := "https://mirror.example.com/dist/v20.18.0/node-v20.18.0-linux-x64.tar.gz"
	if got := ApplyMirror(raw, "https://mirror.example.com/"); got != want {
		t.Fatalf("mirror rewrite = %s, want %s", got, want)
	}
}

func TestFindVersionAndPlatformURL(t *testing.T) {
	if _, _, ok := FindVersion("jdk", "99"); ok {
		t.Fatal("unknown version must not be found")
	}
	_, version, ok := FindVersion("nodejs", "20.18.0")
	if !ok {
		t.Fatal("nodejs 20.18.0 must exist")
	}
	// 至少 windows/amd64 或 linux/amd64 之一要有地址（构建机常见平台）
	if _, err := version.DownloadURL(""); err != nil {
		if _, w := version.URLs["windows/amd64"]; !w {
			t.Fatalf("expected windows/amd64 url, err=%v", err)
		}
	}
}

// 安装目录名必须跨平台安全：Windows 上 + : * ? " < > | 都不合法。
func TestInstallDirIsCrossPlatformSafe(t *testing.T) {
	installer := NewInstaller(filepath.Join("data", "tools"), "", nil)
	dir := installer.Dir("jdk", "17.0.12+7")
	if strings.ContainsAny(filepath.Base(filepath.Dir(dir)), `*?"<>|:`) {
		t.Fatalf("dir must not contain windows-unsafe chars: %s", dir)
	}
	for _, bad := range []string{"+", ":", "*", "?", "\"", "<", ">", "|"} {
		if strings.Contains(filepath.Base(dir), bad) {
			t.Fatalf("dir %q still contains %q", dir, bad)
		}
	}
	// 版本号的 v 前缀要归一，路径稳定（v18.20.4 与 18.20.4 是同一个目录）
	if installer.Dir("nodejs", "v18.20.4") != installer.Dir("nodejs", "18.20.4") {
		t.Fatal("v-prefixed version must map to the same dir")
	}
}
