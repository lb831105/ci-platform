package service

import (
	"strings"
	"testing"

	"ci-platform/internal/model"
)

func toolTestSettings() model.Settings {
	return model.Settings{ToolCategories: []model.ToolCategory{
		{ID: "jdk", Title: "JDK 安装", Installations: []model.ToolInstallation{
			{Name: "JDK 17", InstallMode: model.ToolInstallAuto, Version: "17.0.12+7"},
			{Name: "JDK 8", InstallMode: model.ToolInstallAuto, Version: "8u422-b05"},
		}},
		{ID: "nodejs", Title: "NodeJS 安装", Installations: []model.ToolInstallation{
			{Name: "Node 20", InstallMode: model.ToolInstallAuto, Version: "20.18.0"},
		}},
	}}
}

// 任务选了 JDK 8 → 该类别里 JDK 8 被提到第一位，后续注入必然用它。
func TestApplyToolSelectionsMovesSelectedFirst(t *testing.T) {
	settings := toolTestSettings()
	p := &model.Pipeline{ToolSelections: map[string]string{"jdk": "JDK 8"}}
	got := applyToolSelections(settings, p)

	var jdk model.ToolCategory
	for _, category := range got.ToolCategories {
		if category.ID == "jdk" {
			jdk = category
		}
	}
	if len(jdk.Installations) != 2 || jdk.Installations[0].Name != "JDK 8" {
		t.Fatalf("selected installation must move first: %+v", jdk.Installations)
	}
	// 原来的 settings 不能被改动（构建之间共享同一份配置）
	if settings.ToolCategories[0].Installations[0].Name != "JDK 17" {
		t.Fatal("applyToolSelections must not mutate shared settings")
	}
	// 没选的类别保持原顺序
	for _, category := range got.ToolCategories {
		if category.ID == "nodejs" && category.Installations[0].Name != "Node 20" {
			t.Fatalf("nodejs order must stay: %+v", category.Installations)
		}
	}
}

// 只准备任务真正用到的工具：否则预置的 5 个自动安装会在首次构建里全下载。
func TestNeededToolCategories(t *testing.T) {
	settings := toolTestSettings()

	// 步骤里用了 npm → 只需要 nodejs
	p := &model.Pipeline{Steps: []model.Step{{Run: "npm ci && npm run build"}}}
	needed := neededToolCategories(settings, p)
	if !needed["nodejs"] || needed["jdk"] || needed["maven"] {
		t.Fatalf("only nodejs should be needed, got %v", needed)
	}

	// 显式选了 JDK 8（哪怕步骤里没调用 java）→ 也要准备 jdk
	p = &model.Pipeline{
		Steps:          []model.Step{{Run: "echo build"}},
		ToolSelections: map[string]string{"jdk": "JDK 8"},
	}
	needed = neededToolCategories(settings, p)
	if !needed["jdk"] || needed["nodejs"] {
		t.Fatalf("explicit selection must be prepared, got %v", needed)
	}

	// 两者都没有 → 什么都不准备（不会平白下载）
	p = &model.Pipeline{Steps: []model.Step{{Run: "echo hi"}}}
	if needed := neededToolCategories(settings, p); len(needed) != 0 {
		t.Fatalf("nothing should be needed, got %v", needed)
	}
}

// 保存时校验：选中的安装项必须存在（否则构建会静默退回默认版本）。
func TestValidateToolSelections(t *testing.T) {
	svc := &PipelineService{settings: newSettingsForToolsTest(t)}

	cases := []struct {
		name      string
		selection map[string]string
		wantError string
	}{
		{"ok", map[string]string{"jdk": "JDK 8"}, ""},
		{"unknown installation", map[string]string{"jdk": "JDK 7"}, "没有名为「JDK 7」"},
		{"unknown category", map[string]string{"ruby": "Ruby 3"}, "没有「ruby」这个工具类别"},
		{"empty is default", map[string]string{"jdk": "  "}, ""},
	}
	for _, tc := range cases {
		p := &model.Pipeline{Name: "job", Steps: []model.Step{{Run: "echo hi"}}}
		p.ToolSelections = tc.selection
		_ = normalizeToolSelections(p)
		err := svc.validateToolSelections(p)
		if tc.wantError == "" {
			if err != nil {
				t.Fatalf("%s: unexpected error %v", tc.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.wantError) {
			t.Fatalf("%s: error = %v, want contains %q", tc.name, err, tc.wantError)
		}
	}
}

// newSettingsForToolsTest 构造一个带工具类别默认值的 SettingsService。
func newSettingsForToolsTest(t *testing.T) *SettingsService {
	t.Helper()
	svc, err := NewSettingsService(newMemSettingsStore(), toolTestSettings())
	if err != nil {
		t.Fatalf("init settings: %v", err)
	}
	return svc
}

// memSettingsStore 是只存在内存里的 SettingsStore，避免测试碰磁盘。
type memSettingsStore struct{ data *model.Settings }

func newMemSettingsStore() *memSettingsStore { return &memSettingsStore{} }

func (m *memSettingsStore) Load() (*model.Settings, error) { return m.data, nil }

func (m *memSettingsStore) Save(s *model.Settings) error {
	copied := *s
	m.data = &copied
	return nil
}
