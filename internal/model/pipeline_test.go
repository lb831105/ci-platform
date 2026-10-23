package model

import "testing"

// 默认复用工作区：只有显式勾选“构建前清空”时才清理。
func TestShouldCleanWorkspaceDefaultFalse(t *testing.T) {
	p := &Pipeline{}
	if p.ShouldCleanWorkspace() {
		t.Fatal("default should reuse workspace")
	}
	yes := true
	p.DeleteWorkspaceBeforeBuild = &yes
	if !p.ShouldCleanWorkspace() {
		t.Fatal("explicit true should clean workspace")
	}
	no := false
	p.DeleteWorkspaceBeforeBuild = &no
	if p.ShouldCleanWorkspace() {
		t.Fatal("explicit false should reuse workspace")
	}
}
