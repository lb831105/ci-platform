package executor

import "ci-platform/internal/model"

// RunSpec 描述一次构建步骤的执行参数（本地执行，不涉及容器与镜像）。
type RunSpec struct {
	Steps     []model.Step      // 串行执行的构建步骤
	Workspace string            // 工作区绝对路径（步骤的当前目录）
	Env       map[string]string // 注入的环境变量
	Shell     string            // 执行步骤使用的 shell，默认按操作系统选择
}
