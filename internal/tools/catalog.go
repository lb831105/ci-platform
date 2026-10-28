// Package tools 负责「全局工具配置」里自动安装的工具（JDK / Maven / Gradle / Node.js …）：
// 版本目录、按平台拼接下载地址、下载解压到 data/tools 并给出主目录。
//
// 为什么自己做而不是只记录路径：运维最常踩的坑就是「服务器上没装 node/npm，
// 构建直接 command not found」。Jenkins 的做法是「工具安装」托管下载，
// 这里照搬同样的形态：手动填路径（用服务器已有工具）或选版本自动安装。
package tools

import (
	"fmt"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

// Version 描述某个工具的一个可自动安装版本。
type Version struct {
	Version string `json:"version"`
	// URLs 按 "GOOS/GOARCH" 索引；key 不存在表示该平台不支持自动安装。
	URLs map[string]string `json:"-"`
	// Note 给用户看的提示（例如需要 JDK 11+）。
	Note string `json:"note,omitempty"`
	// StripTop 为 true 表示压缩包顶层只有一个目录，主目录取该目录（大多数发行版如此）。
	StripTop bool `json:"-"`
	// FallbackURLs 是老版本的备用地址（Apache 老版本只在 archive 站上）。
	FallbackURLs map[string]string `json:"-"`
	// SHA256 按平台提供压缩包校验和（hex）。为空表示当前版本未内置校验和，
	// 安装器会跳过校验；生产镜像可在自定义清单中补全，防止下载内容被篡改。
	SHA256 map[string]string `json:"-"`
	// Custom 表示这个版本是按 URL 模板现算出来的（用户手填的补丁版本）。
	Custom bool `json:"custom,omitempty"`
}

// Tool 是一类可自动安装的工具（对应 Jenkins 的「新增 X 安装」）。
type Tool struct {
	Type        string    `json:"type"`
	Title       string    `json:"title"`
	HomeEnv     string    `json:"home_env,omitempty"` // 主目录对应的环境变量名
	Hint        string    `json:"hint,omitempty"`
	Versions    []Version `json:"versions"`
	AutoInstall bool      `json:"auto_install"` // 是否支持自动安装

	// URLTemplates 让「任意补丁版本」也能装：形如
	//   "linux/amd64": "https://nodejs.org/dist/v{version}/node-v{version}-linux-x64.tar.gz"
	// 有了它就不必把每个补丁版本都写进清单（Node 12~24 全量版本上百个）。
	URLTemplates map[string]string `json:"-"`
	// VersionPattern 是手填版本的合法格式（正则）。
	VersionPattern string `json:"-"`
	// VersionHint 告诉用户手填版本长什么样。
	VersionHint string `json:"version_hint,omitempty"`
}

// platformKey 返回当前运行平台的 key（GOOS/GOARCH）。
func platformKey() string {
	return runtime.GOOS + "/" + runtime.GOARCH
}

// temurinJDK 生成一个 Temurin JDK 条目的下载地址。
// Temurin 的版本号形如 17.0.12+7，发布 tag 与文件名都要把 "+" 换成 "_"。
func temurinJDK(major int, version string) Version {
	tag := "jdk-" + strings.ReplaceAll(version, "+", "%2B")
	file := strings.ReplaceAll(version, "+", "_")
	base := fmt.Sprintf("https://github.com/adoptium/temurin%d-binaries/releases/download/%s/OpenJDK%dU-jdk_", major, tag, major)
	return Version{
		Version: version, StripTop: true,
		URLs: map[string]string{
			"windows/amd64": base + "x64_windows_hotspot_" + file + ".zip",
			"linux/amd64":   base + "x64_linux_hotspot_" + file + ".tar.gz",
			"linux/arm64":   base + "aarch64_linux_hotspot_" + file + ".tar.gz",
		},
	}
}

// temurinJDK8 生成 Temurin 8（即 JDK 1.8）条目。
// JDK 8 的版本串是 8u422-b05，发布 tag 是 jdk8u422-b05，文件名是 8u422b05。
func temurinJDK8(version, note string) Version {
	compact := strings.ReplaceAll(version, "-", "") // 8u422-b05 → 8u422b05
	tag := "jdk" + version                          // jdk8u422-b05
	base := "https://github.com/adoptium/temurin8-binaries/releases/download/" + tag + "/OpenJDK8U-jdk_"
	return Version{
		Version: version, StripTop: true, Note: note,
		URLs: map[string]string{
			"windows/amd64": base + "x64_windows_hotspot_" + compact + ".zip",
			"linux/amd64":   base + "x64_linux_hotspot_" + compact + ".tar.gz",
			"linux/arm64":   base + "aarch64_linux_hotspot_" + compact + ".tar.gz",
		},
	}
}

// nodeVersions 生成 Node.js 12~24 每个大版本的代表版本（该大版本最后一个补丁）。
//
// 为什么不逐条列出所有补丁版本：Node 12~24 有上百个补丁版本，
// 全写进下拉等于让人在列表里滚一分钟。这里给出每个大版本的主流补丁，
// 其它补丁版本由 URL 模板 + 「手填版本」支持（18.20.4 这类都能直接装）。
func nodeVersions() []Version {
	// 大版本 → 该线最后一个补丁版本（LTS 标出便于选择）
	table := []struct {
		major string
		patch string
		note  string
	}{
		{"12", "22.12", "Node 12（已 EOL）"},
		{"14", "21.3", "Node 14（已 EOL）"},
		{"16", "20.2", "Node 16（已 EOL）"},
		{"18", "20.5", "Node 18 LTS"},
		{"20", "18.0", "Node 20 LTS"},
		{"22", "11.0", "Node 22 LTS"},
		{"24", "1.0", "Node 24"},
	}
	out := make([]Version, 0, len(table))
	for _, row := range table {
		version := row.major + "." + row.patch
		out = append(out, Version{
			Version: version, StripTop: true, Note: row.note,
			URLs: map[string]string{
				"windows/amd64": fmt.Sprintf("https://nodejs.org/dist/v%s/node-v%s-win-x64.zip", version, version),
				"linux/amd64":   fmt.Sprintf("https://nodejs.org/dist/v%s/node-v%s-linux-x64.tar.gz", version, version),
				"linux/arm64":   fmt.Sprintf("https://nodejs.org/dist/v%s/node-v%s-linux-arm64.tar.gz", version, version),
			},
		})
	}
	return out
}

// catalog 是内置的版本清单。只收录官方直链（TLS）：
// 版本写死而不是「latest」，避免同一份 settings.json 在不同时间装出不同版本。
func catalog() []Tool {
	return []Tool{
		{
			Type: "jdk", Title: "JDK", HomeEnv: "JAVA_HOME", AutoInstall: true,
			Hint: "构建 Java 项目需要 JDK；Maven/Gradle 也会用 JAVA_HOME。老项目选 JDK 8（1.8）",
			Versions: []Version{
				withNote(temurinJDK8("8u422-b05", "JDK 1.8（8u422），老项目/Spring Boot 2.x 常用"), "JDK 1.8（8u422），老项目/Spring Boot 2.x 常用"),
				withNote(temurinJDK(11, "11.0.25+9"), "LTS，Spring Boot 2.7 常用"),
				withNote(temurinJDK(17, "17.0.12+7"), "LTS，Spring Boot 3 常用"),
				withNote(temurinJDK(21, "21.0.4+7"), "LTS，新版 Spring Boot"),
			},
		},
		{
			Type: "maven", Title: "Maven", HomeEnv: "MAVEN_HOME", AutoInstall: true,
			Hint: "Maven 主目录；构建步骤里可直接用 mvn",
			Versions: []Version{
				mavenVersion("3.9.9"), mavenVersion("3.9.6"), mavenVersion("3.8.8"),
				mavenVersion("3.6.3"), mavenVersion("3.5.4"),
			},
		},
		{
			Type: "gradle", Title: "Gradle", HomeEnv: "GRADLE_HOME", AutoInstall: true,
			Hint: "Gradle 主目录；构建步骤里可直接用 gradle",
			Versions: []Version{
				gradleVersion("8.10.2"), gradleVersion("8.5"), gradleVersion("7.6.4"),
				gradleVersion("6.9.4"), gradleVersion("5.6.4"),
			},
		},
		{
			Type: "nodejs", Title: "NodeJS", HomeEnv: "NODE_HOME", AutoInstall: true,
			Hint:           "Node.js 主目录（含 npm/npx）。可选 12~24 各大版本，也可手填任意补丁版本",
			Versions:       nodeVersions(),
			VersionPattern: `^\d+\.\d+\.\d+$`,
			VersionHint:    "任意补丁版本，如 18.20.4、16.20.2",
			URLTemplates: map[string]string{
				"windows/amd64": "https://nodejs.org/dist/v{version}/node-v{version}-win-x64.zip",
				"linux/amd64":   "https://nodejs.org/dist/v{version}/node-v{version}-linux-x64.tar.gz",
				"linux/arm64":   "https://nodejs.org/dist/v{version}/node-v{version}-linux-arm64.tar.gz",
			},
		},
		{
			Type: "golang", Title: "Go", HomeEnv: "GOROOT", AutoInstall: true,
			Hint: "Go 主目录（GOROOT）",
			Versions: []Version{
				goVersion("1.23.2"), goVersion("1.22.8"), goVersion("1.21.13"),
				goVersion("1.20.14"), goVersion("1.19.13"),
			},
		},
		{
			Type: "ant", Title: "Ant", HomeEnv: "ANT_HOME", AutoInstall: true,
			Hint: "Ant 主目录",
			Versions: []Version{
				antVersion("1.10.14"), antVersion("1.10.13"), antVersion("1.9.16"),
			},
		},
		{
			Type: "git", Title: "Git", HomeEnv: "GIT_EXECUTABLE", AutoInstall: false,
			Hint: "平台自带 Git 客户端拉代码；如需在步骤里调用 git，请填写服务器上已有的 Git 主目录",
		},
	}
}

// withNote 给已有版本补一个说明（Temurin 条目由构造函数生成，说明在这里补）。
func withNote(v Version, note string) Version {
	v.Note = note
	return v
}

// mavenVersion 生成 Maven 条目：当前版本用 dlcdn，老版本回退 Apache archive。
func mavenVersion(version string) Version {
	major := strings.SplitN(version, ".", 2)[0]
	paths := []string{
		fmt.Sprintf("https://dlcdn.apache.org/maven/maven-%s/%s/binaries/apache-maven-%s-bin", major, version, version),
		// 老版本已经从 dlcdn 下架，只在 archive 里；两个地址都试由安装器负责。
	}
	return Version{
		Version: version, StripTop: true,
		URLs: map[string]string{
			"windows/amd64": paths[0] + ".zip",
			"windows/arm64": paths[0] + ".zip",
			"linux/amd64":   paths[0] + ".tar.gz",
			"linux/arm64":   paths[0] + ".tar.gz",
		},
		FallbackURLs: map[string]string{
			"windows/amd64": fmt.Sprintf("https://archive.apache.org/dist/maven/maven-%s/%s/binaries/apache-maven-%s-bin.zip", major, version, version),
			"windows/arm64": fmt.Sprintf("https://archive.apache.org/dist/maven/maven-%s/%s/binaries/apache-maven-%s-bin.zip", major, version, version),
			"linux/amd64":   fmt.Sprintf("https://archive.apache.org/dist/maven/maven-%s/%s/binaries/apache-maven-%s-bin.tar.gz", major, version, version),
			"linux/arm64":   fmt.Sprintf("https://archive.apache.org/dist/maven/maven-%s/%s/binaries/apache-maven-%s-bin.tar.gz", major, version, version),
		},
	}
}

func gradleVersion(version string) Version {
	return Version{
		Version: version, StripTop: true,
		URLs: map[string]string{
			"windows/amd64": "https://services.gradle.org/distributions/gradle-" + version + "-bin.zip",
			"windows/arm64": "https://services.gradle.org/distributions/gradle-" + version + "-bin.zip",
			"linux/amd64":   "https://services.gradle.org/distributions/gradle-" + version + "-bin.zip",
			"linux/arm64":   "https://services.gradle.org/distributions/gradle-" + version + "-bin.zip",
		},
	}
}

func goVersion(version string) Version {
	return Version{
		Version: version, StripTop: true,
		URLs: map[string]string{
			"windows/amd64": "https://go.dev/dl/go" + version + ".windows-amd64.zip",
			"linux/amd64":   "https://go.dev/dl/go" + version + ".linux-amd64.tar.gz",
			"linux/arm64":   "https://go.dev/dl/go" + version + ".linux-arm64.tar.gz",
		},
	}
}

// antVersion 生成 Ant 条目：新版本在 dlcdn，老版本在 archive。
func antVersion(version string) Version {
	dlcdn := "https://dlcdn.apache.org/ant/binaries/apache-ant-" + version + "-bin"
	archive := "https://archive.apache.org/dist/ant/binaries/apache-ant-" + version + "-bin"
	return Version{
		Version: version, StripTop: true,
		URLs: map[string]string{
			"windows/amd64": dlcdn + ".zip",
			"linux/amd64":   dlcdn + ".tar.gz",
		},
		FallbackURLs: map[string]string{
			"windows/amd64": archive + ".zip",
			"linux/amd64":   archive + ".tar.gz",
		},
	}
}

// Catalog 返回全部工具类别（含不支持自动安装的类别，供 UI 展示说明）。
func Catalog() []Tool {
	out := catalog()
	for i := range out {
		sort.SliceStable(out[i].Versions, func(a, b int) bool {
			return versionLess(out[i].Versions[b].Version, out[i].Versions[a].Version)
		})
	}
	return out
}

// versionLess 按数字段比较版本，避免 "3.10" 排在 "3.9" 前面。
func versionLess(a, b string) bool {
	split := func(s string) []int {
		fields := strings.FieldsFunc(s, func(r rune) bool {
			return r == '.' || r == '-' || r == '+' || r == '_' || r == 'u' || r == 'b'
		})
		out := make([]int, 0, len(fields))
		for _, f := range fields {
			n, err := strconv.Atoi(f)
			if err != nil {
				return out
			}
			out = append(out, n)
		}
		return out
	}
	left, right := split(a), split(b)
	for i := 0; i < len(left) && i < len(right); i++ {
		if left[i] != right[i] {
			return left[i] < right[i]
		}
	}
	return len(left) < len(right)
}

// FindTool 按类型查类别。
func FindTool(toolType string) (Tool, bool) {
	for _, tool := range catalog() {
		if tool.Type == strings.ToLower(strings.TrimSpace(toolType)) {
			return tool, true
		}
	}
	return Tool{}, false
}

// FindVersion 查某工具的某个版本；模板类工具（如 Node）支持任意符合格式的补丁版本。
func FindVersion(toolType, version string) (Tool, Version, bool) {
	tool, ok := FindTool(toolType)
	if !ok {
		return Tool{}, Version{}, false
	}
	version = normalizeVersion(version)
	if version == "" {
		return tool, Version{}, false
	}
	for _, v := range tool.Versions {
		if v.Version == version {
			return tool, v, true
		}
	}
	// 手填的补丁版本：按模板现算下载地址（Node 12~24 的上百个补丁版本都能装）。
	if len(tool.URLTemplates) > 0 {
		if tool.VersionPattern != "" {
			if re, err := regexp.Compile(tool.VersionPattern); err != nil || !re.MatchString(version) {
				return tool, Version{}, false
			}
		}
		urls := make(map[string]string, len(tool.URLTemplates))
		for key, tmpl := range tool.URLTemplates {
			urls[key] = strings.ReplaceAll(tmpl, "{version}", version)
		}
		return tool, Version{Version: version, URLs: urls, StripTop: true, Custom: true}, true
	}
	return tool, Version{}, false
}

// normalizeVersion 去掉用户可能带上的 v 前缀与首尾空格（v18.20.4 → 18.20.4）。
func normalizeVersion(version string) string {
	version = strings.TrimSpace(version)
	version = strings.TrimPrefix(version, "v")
	return version
}

// testURLs 仅供测试覆盖下载地址（生产环境始终为空）。
var testURLs = map[string]string{}

// setTestURL 覆盖某版本的当前平台下载地址；url 为空表示清除覆盖。
func setTestURL(version, url string) {
	if url == "" {
		delete(testURLs, version)
		return
	}
	testURLs[version] = url
}

// DownloadURL 返回当前平台可用的下载地址。
// mirror 非空时把 scheme+host 换成镜像前缀（内网/国内镜像的逃生口）。
func (v Version) DownloadURL(mirror string) (string, error) {
	if override, ok := testURLs[v.Version]; ok && override != "" {
		return ApplyMirror(override, mirror), nil
	}
	raw, ok := v.URLs[platformKey()]
	if !ok || raw == "" {
		return "", fmt.Errorf("当前平台 %s 没有预置下载地址，请改为手动填写工具主目录", platformKey())
	}
	return ApplyMirror(raw, mirror), nil
}

// FallbackDownloadURL 返回备用下载地址（老版本在 httpd 归档站上）。
// 没有备用地址时返回空串，安装器会直接报错。
func (v Version) FallbackDownloadURL(mirror string) string {
	raw := v.FallbackURLs[platformKey()]
	if raw == "" {
		return ""
	}
	return ApplyMirror(raw, mirror)
}

// ApplyMirror 把 URL 的 scheme://host 替换为镜像前缀；镜像为空时原样返回。
func ApplyMirror(raw, mirror string) string {
	mirror = strings.TrimRight(strings.TrimSpace(mirror), "/")
	if mirror == "" || raw == "" {
		return raw
	}
	idx := strings.Index(raw, "://")
	if idx < 0 {
		return raw
	}
	rest := raw[idx+3:]
	slash := strings.Index(rest, "/")
	if slash < 0 {
		return mirror
	}
	return mirror + rest[slash:]
}
