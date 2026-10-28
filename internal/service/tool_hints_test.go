package service

import (
	"strings"
	"testing"

	"ci-platform/internal/model"
)

// 步骤用到 npm/node 但 NodeJS 没配置主目录 → 必须提示（这正是线上踩到的坑）。
func TestMissingToolHintsWarnsAboutNpm(t *testing.T) {
	settings := model.Settings{ToolCategories: []model.ToolCategory{
		{ID: "nodejs", Title: "NodeJS", Installations: []model.ToolInstallation{{Name: "NodeJS"}}},
		{ID: "jdk", Title: "JDK", Installations: []model.ToolInstallation{{Name: "JDK 17", Home: "/opt/jdk-17"}}},
	}}
	steps := []model.Step{{Name: "build", Run: "npm ci && npm run build"}}
	hints := missingToolHints(settings, steps)
	if len(hints) != 1 {
		t.Fatalf("expected one hint, got %v", hints)
	}
	if !strings.Contains(hints[0], "npm") || !strings.Contains(hints[0], "NodeJS") {
		t.Fatalf("hint must mention npm and NodeJS: %s", hints[0])
	}
}

// 已配置主目录的工具不再提示；没在步骤里出现的工具也不提示。
func TestMissingToolHintsSilentWhenConfigured(t *testing.T) {
	settings := model.Settings{ToolCategories: []model.ToolCategory{
		{ID: "nodejs", Title: "NodeJS", Installations: []model.ToolInstallation{{Name: "NodeJS", Home: "/opt/node"}}},
	}}
	if hints := missingToolHints(settings, []model.Step{{Run: "npm ci"}}); len(hints) != 0 {
		t.Fatalf("configured tool must not warn: %v", hints)
	}
	settings.ToolCategories[0].Installations[0].Home = ""
	// 步骤里完全没有调用工具命令 → 不该有提示
	if hints := missingToolHints(settings, []model.Step{{Run: "echo hello && ls -al"}}); len(hints) != 0 {
		t.Fatalf("steps using no tool must not warn: %v", hints)
	}
}

// containsCommand 不能把文件名/子串当成命令。
func TestContainsCommandBoundaries(t *testing.T) {
	cases := []struct {
		script string
		cmd    string
		want   bool
	}{
		{"npm ci", "npm", true},
		{"cd web && npm run build", "npm", true},
		{"./scripts/npm-build.sh", "npm", false},
		{"echo npm", "npm", false}, // 命令名当参数 → 不算调用
		{"mynpmtool", "npm", false},
		{"cd web && npm ci", "npm", true}, // && 之后的第一个词
		{"npm ci", "npm", true},
		{"FOO=npm", "npm", false},            // 变量赋值
		{"sudo npm install", "npm", true},    // 包装命令
		{"echo jar > app.jar", "jar", false}, // 文件名不能触发下载 JDK
	}
	for _, c := range cases {
		if got := containsCommand(c.script, c.cmd); got != c.want {
			t.Fatalf("containsCommand(%q, %q) = %v, want %v", c.script, c.cmd, got, c.want)
		}
	}
}
