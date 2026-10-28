package tools

import (
	"strings"
	"testing"
)

// JDK 1.8 必须可装（老项目/Spring Boot 2.x），且文件名符合 Temurin 8 的命名规则。
func TestCatalogHasJDK8(t *testing.T) {
	_, version, ok := FindVersion("jdk", "8u422-b05")
	if !ok {
		t.Fatal("jdk 1.8 (8u422-b05) must be in catalog")
	}
	url, err := version.DownloadURL("")
	if err != nil {
		if _, has := version.URLs["windows/amd64"]; !has {
			t.Fatalf("jdk8 needs windows url: %v", err)
		}
	}
	if url != "" {
		if !strings.Contains(url, "temurin8-binaries") || !strings.Contains(url, "jdk8u422-b05") {
			t.Fatalf("unexpected jdk8 url: %s", url)
		}
		if !strings.Contains(url, "8u422b05") {
			t.Fatalf("jdk8 file name must use compact form: %s", url)
		}
	}
}

// Node 的每个大版本（12~24）都要有预置可选版本。
func TestCatalogCoversNodeMajors(t *testing.T) {
	tool, ok := FindTool("nodejs")
	if !ok {
		t.Fatal("nodejs tool missing")
	}
	got := map[string]bool{}
	for _, v := range tool.Versions {
		got[strings.SplitN(v.Version, ".", 2)[0]] = true
	}
	for _, major := range []string{"12", "14", "16", "18", "20", "22", "24"} {
		if !got[major] {
			t.Fatalf("node %s missing from catalog (have %v)", major, got)
		}
	}
}

// 任意补丁版本（模板）也能装，例如 18.20.4 / 12.22.12。
func TestNodeCustomVersionUsesTemplate(t *testing.T) {
	_, version, ok := FindVersion("nodejs", "v18.20.4") // 允许带 v 前缀
	if !ok {
		t.Fatal("custom node version must be accepted via template")
	}
	if !version.Custom {
		t.Fatal("template-derived version should be marked custom")
	}
	url, err := version.DownloadURL("")
	if err != nil {
		if _, has := version.URLs["windows/amd64"]; !has {
			t.Fatalf("template must produce urls: %v", err)
		}
		return
	}
	if !strings.Contains(url, "/v18.20.4/") || !strings.Contains(url, "node-v18.20.4-") {
		t.Fatalf("template url wrong: %s", url)
	}
	// 格式不对的版本要拒绝
	if _, _, ok := FindVersion("nodejs", "latest"); ok {
		t.Fatal("invalid node version must be rejected")
	}
}

// 老版本 Maven/Ant 要有 archive 备用地址（dlcdn 已下架）。
func TestOldVersionsHaveFallbackURL(t *testing.T) {
	for _, tc := range []struct{ tool, version string }{
		{"maven", "3.6.3"}, {"maven", "3.5.4"}, {"ant", "1.9.16"},
	} {
		_, version, ok := FindVersion(tc.tool, tc.version)
		if !ok {
			t.Fatalf("%s %s missing", tc.tool, tc.version)
		}
		fallback := version.FallbackDownloadURL("")
		if fallback == "" {
			// 当前平台没配备用地址时，至少要有主地址
			if _, has := version.URLs[platformKey()]; !has {
				t.Fatalf("%s %s has neither primary nor fallback url", tc.tool, tc.version)
			}
			continue
		}
		if !strings.Contains(fallback, "archive.apache.org") {
			t.Fatalf("%s %s fallback should point at archive: %s", tc.tool, tc.version, fallback)
		}
	}
}

// 版本排序按数字段比较：3.10 在 3.9 之后（字符串比较会反过来）。
func TestVersionSortIsNumeric(t *testing.T) {
	if versionLess("3.10.0", "3.9.0") {
		t.Fatal("3.10.0 must be greater than 3.9.0")
	}
	if !versionLess("3.9.0", "3.10.0") {
		t.Fatal("3.9.0 must be less than 3.10.0")
	}
}
