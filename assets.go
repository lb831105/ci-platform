// Package assets 负责把前端构建产物（web/dist）内嵌进 Go 二进制。
//
// 之所以放在项目根目录：Go 的 //go:embed 指令只能嵌入「当前包目录及其子目录」下的文件，
// 无法使用 ../ 跳出包目录，因此内嵌入口必须与 web/ 同级。
//
// 该文件只做一件事：解析出可用的前端资源 fs.FS。它不参与路由，也不感知 HTTP。
package assets

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// all: 前缀确保把 web/dist 下所有文件（含以 . 或 _ 开头的）一并嵌入。
//
//go:embed all:web/dist
var embeddedWeb embed.FS

// webRootInEmbed 是内嵌文件系统里前端产物的根目录。
const webRootInEmbed = "web/dist"

// Embedded 返回内嵌前端资源的子文件系统；若未内嵌有效 index.html 则返回错误。
func Embedded() (fs.FS, error) {
	sub, err := fs.Sub(embeddedWeb, webRootInEmbed)
	if err != nil {
		return nil, fmt.Errorf("sub fs %s: %w", webRootInEmbed, err)
	}
	if _, err = fs.Stat(sub, "index.html"); err != nil {
		return nil, fmt.Errorf("embedded %s/index.html missing: %w", webRootInEmbed, err)
	}
	return sub, nil
}

// Resolve 返回最终用于托管的前端资源文件系统，优先级：
//
//  1. 二进制内嵌的 web/dist（生产形态，单文件交付）
//  2. diskFallback 指定的磁盘目录（开发便利：不重新编译即可换前端产物）
//  3. nil（前端完全缺失，由调用方决定如何提示）
func Resolve(diskFallback string) (fs.FS, string) {
	if sub, err := Embedded(); err == nil {
		return sub, "embedded"
	}
	if diskFallback != "" {
		if _, err := os.Stat(filepath.Join(diskFallback, "index.html")); err == nil {
			return os.DirFS(diskFallback), "disk:" + diskFallback
		}
	}
	return nil, "none"
}
