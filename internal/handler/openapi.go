package handler

import (
	_ "embed"
	"net/http"
)

// OpenAPI 文档以静态 JSON 内嵌，随二进制分发，不依赖外网或前端资源。
// 只覆盖对外稳定契约；内部管理接口（导入/迁移/工具安装）仍以 README 为准。
//
//go:embed openapi.json
var openAPISpec []byte

// OpenAPISpec GET /openapi.json（同时挂 /api/openapi.json）
func OpenAPISpec(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write(openAPISpec)
}
