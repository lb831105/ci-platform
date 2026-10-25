#!/usr/bin/env bash
#
# ci-platform 构建脚本（在 Windows 上通过 Git Bash 运行，亦可直接在 Linux/macOS 运行）
#
# 功能：
#   1. 构建前端（Vite → web/dist）
#   2. 将 web/dist 通过 go:embed 内嵌，交叉编译 linux / windows 可执行文件
#   3. 使用 UPX 压缩产物（未安装 UPX 时自动跳过并提示）
#
# 用法：
#   ./build.sh                          # 默认构建 linux/amd64 + windows/amd64
#   ./build.sh --platforms linux/amd64,windows/amd64,windows/arm64
#   ./build.sh --single                 # 仅构建当前平台，用于本机快速验证
#   ./build.sh --skip-web               # 跳过前端构建（要求 web/dist 已存在）
#   ./build.sh --no-upx                 # 跳过 UPX 压缩
#   ./build.sh -h
#
set -euo pipefail

# ------------------------------- 基础设置 -------------------------------

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

PROJECT_NAME="ci-platform"
PKG="./cmd/server"
OUT_DIR="dist"
WEB_DIR="web"
DEFAULT_PLATFORMS="linux/amd64,windows/amd64"

PLATFORMS="$DEFAULT_PLATFORMS"
SKIP_WEB=0
USE_UPX=1
SINGLE=0
UPX_LEVEL="--best --lzma"

# 固定使用本机 Go 工具链，避免 go.mod 中的 go 指令触发在线下载工具链，
# 从而保证离线与可复现构建。
export GOTOOLCHAIN="${GOTOOLCHAIN:-local}"
export CGO_ENABLED=0

# ------------------------------- 颜色输出 -------------------------------

if [ -t 1 ]; then
	C_RESET="\033[0m"; C_INFO="\033[36m"; C_OK="\033[32m"; C_WARN="\033[33m"; C_ERR="\033[31m"; C_BOLD="\033[1m"
else
	C_RESET=""; C_INFO=""; C_OK=""; C_WARN=""; C_ERR=""; C_BOLD=""
fi

info() { printf "${C_INFO}[INFO]${C_RESET} %s\n" "$*"; }
ok()   { printf "${C_OK}[ OK ]${C_RESET} %s\n" "$*"; }
warn() { printf "${C_WARN}[WARN]${C_RESET} %s\n" "$*"; }
die()  { printf "${C_ERR}[FAIL]${C_RESET} %s\n" "$*" >&2; exit 1; }
step() { printf "\n${C_BOLD}==> %s${C_RESET}\n" "$*"; }

# ------------------------------- 参数解析 -------------------------------

usage() {
	cat <<'EOF'
ci-platform 构建脚本

用法: ./build.sh [选项]

选项:
  --platforms <列表>   目标平台，逗号分隔，格式 os/arch
                       默认: linux/amd64,windows/amd64
  --single             仅构建当前平台（os/arch 取本机），输出到 dist/
  --skip-web           跳过前端构建（要求 web/dist/index.html 已存在）
  --no-upx             跳过 UPX 压缩
  --upx-level <参数>   UPX 压缩级别，默认 "--best --lzma"
  -h, --help           显示本帮助

示例:
  ./build.sh
  ./build.sh --platforms linux/amd64,linux/arm64,windows/amd64
  ./build.sh --single --no-upx
EOF
}

while [ $# -gt 0 ]; do
	case "$1" in
		--platforms)
			[ $# -ge 2 ] || die "--platforms 需要一个参数"
			PLATFORMS="$2"; shift 2 ;;
		--platforms=*) PLATFORMS="${1#*=}"; shift ;;
		--single)      SINGLE=1; shift ;;
		--skip-web)    SKIP_WEB=1; shift ;;
		--no-upx)      USE_UPX=0; shift ;;
		--upx-level)
			[ $# -ge 2 ] || die "--upx-level 需要一个参数"
			UPX_LEVEL="$2"; shift 2 ;;
		--upx-level=*) UPX_LEVEL="${1#*=}"; shift ;;
		-h|--help)     usage; exit 0 ;;
		*)             die "未知参数: $1（使用 -h 查看帮助）" ;;
	esac
done

# ------------------------------- 前置检查 -------------------------------

step "环境检查"

command -v go >/dev/null 2>&1 || die "未找到 go，请先安装 Go 1.22+"
GO_VERSION="$(go version | awk '{print $3}')"
ok "go          ${GO_VERSION}  (GOTOOLCHAIN=${GOTOOLCHAIN}, CGO_ENABLED=${CGO_ENABLED})"

if [ "$SKIP_WEB" -eq 0 ]; then
	command -v node >/dev/null 2>&1 || die "未找到 node，请先安装 Node.js 18+（或使用 --skip-web）"
	command -v npm  >/dev/null 2>&1 || die "未找到 npm，请先安装 Node.js 18+（或使用 --skip-web）"
	ok "node        $(node -v)   npm $(npm -v)"
fi

UPX_BIN=""
if [ "$USE_UPX" -eq 1 ]; then
	if command -v upx >/dev/null 2>&1; then
		UPX_BIN="$(command -v upx)"
		ok "upx         $(upx --version 2>/dev/null | head -1)"
	else
		warn "未找到 upx，将跳过压缩。安装后重试即可获得更小体积（https://github.com/upx/upx/releases）"
		USE_UPX=0
	fi
fi

# 版本信息（用于 -ldflags -X 注入）
GIT_VERSION="dev"
GIT_COMMIT="none"
if command -v git >/dev/null 2>&1 && git rev-parse --git-dir >/dev/null 2>&1; then
	GIT_VERSION="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"
	GIT_COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo none)"
fi
BUILD_TIME="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
ok "version     ${GIT_VERSION} (${GIT_COMMIT}) @ ${BUILD_TIME}"

# ------------------------------- 前端构建 -------------------------------

step "构建前端（Vite → ${WEB_DIR}/dist）"

if [ "$SKIP_WEB" -eq 1 ]; then
	info "已指定 --skip-web，跳过前端构建"
else
	pushd "$WEB_DIR" >/dev/null
	if [ ! -d node_modules ]; then
		if [ -f package-lock.json ]; then
			info "node_modules 缺失，执行 npm ci ..."
			npm ci --no-audit --no-fund
		else
			info "node_modules 缺失，执行 npm install ..."
			npm install --no-audit --no-fund
		fi
	else
		info "依赖已存在，跳过安装"
	fi
	info "执行 npm run build ..."
	npm run build
	popd >/dev/null
fi

[ -f "${WEB_DIR}/dist/index.html" ] || die "未找到 ${WEB_DIR}/dist/index.html。
内嵌编译要求前端产物必须存在，请先执行：
    cd web && npm install && npm run build
或去掉 --skip-web 重新运行本脚本。"

WEB_FILE_COUNT="$(find "${WEB_DIR}/dist" -type f | wc -l | tr -d ' ')"
WEB_SIZE="$(du -sk "${WEB_DIR}/dist" | awk '{print $1}')"
ok "前端产物就绪：${WEB_FILE_COUNT} 个文件，约 ${WEB_SIZE} KB（将被 go:embed 内嵌）"

# ------------------------------- 交叉编译 -------------------------------

# 确定目标平台列表
if [ "$SINGLE" -eq 1 ]; then
	PLATFORMS="$(go env GOOS)/$(go env GOARCH)"
	info "单平台模式：${PLATFORMS}"
fi

# 清理旧产物：Windows 上若目录被资源管理器/终端占用，rm 可能失败，
# 此时降级为只清空目录内容，避免整个构建因清理失败而中断。
if ! rm -rf "$OUT_DIR" 2>/dev/null; then
	warn "无法删除 ${OUT_DIR}（可能被占用），改为清空目录内容"
	mkdir -p "$OUT_DIR"
	find "$OUT_DIR" -mindepth 1 -maxdepth 1 -exec rm -rf {} + 2>/dev/null || true
fi
mkdir -p "$OUT_DIR"

LDFLAGS="-s -w -X main.buildVersion=${GIT_VERSION} -X main.buildCommit=${GIT_COMMIT} -X main.buildDate=${BUILD_TIME}"
BUILD_TARGETS=()

step "交叉编译（内嵌前端资源）"

IFS=',' read -r -a PLATFORM_ARR <<< "$PLATFORMS"
for PLATFORM in "${PLATFORM_ARR[@]}"; do
	PLATFORM="$(echo "$PLATFORM" | tr -d ' ')"
	[ -n "$PLATFORM" ] || continue

	TARGET_OS="${PLATFORM%%/*}"
	TARGET_ARCH="${PLATFORM##*/}"
	[ "$TARGET_OS" != "$PLATFORM" ] || die "平台格式错误: ${PLATFORM}（应为 os/arch，例如 linux/amd64）"

	EXT=""
	[ "$TARGET_OS" = "windows" ] && EXT=".exe"
	OUT_NAME="${PROJECT_NAME}-${TARGET_OS}-${TARGET_ARCH}${EXT}"
	OUT_PATH="${OUT_DIR}/${OUT_NAME}"

	info "编译 ${TARGET_OS}/${TARGET_ARCH} → ${OUT_PATH}"
	GOOS="$TARGET_OS" GOARCH="$TARGET_ARCH" \
		go build -trimpath -ldflags "$LDFLAGS" -o "$OUT_PATH" "$PKG"

	[ -f "$OUT_PATH" ] || die "编译失败，未生成 ${OUT_PATH}"
	BUILD_TARGETS+=("$OUT_PATH")
done

ok "编译完成，共 ${#BUILD_TARGETS[@]} 个产物"

# ------------------------------- UPX 压缩 -------------------------------

# 以字节数返回文件大小
file_size() { wc -c < "$1" | tr -d ' '; }
# 字节 → 人类可读
human_size() {
	awk -v b="$1" 'BEGIN {
		split("B KB MB GB", u, " ");
		i = 1;
		while (b >= 1024 && i < 4) { b /= 1024; i++ }
		printf (i == 1 ? "%d %s" : "%.2f %s"), b, u[i]
	}'
}

declare -a ROWS=()

if [ "$USE_UPX" -eq 1 ]; then
	step "UPX 压缩（级别：${UPX_LEVEL}）"
	for TARGET in "${BUILD_TARGETS[@]}"; do
		BEFORE="$(file_size "$TARGET")"
		info "压缩 $(basename "$TARGET") ..."
		# --no-backup：默认会留下 <file>~ 备份，dist/ 里多一份近 4MB 的冗余文件，
		# 也会让「产物清单」看上去多了一个可疑文件。
		if upx $UPX_LEVEL --no-backup --quiet "$TARGET"; then
			AFTER="$(file_size "$TARGET")"
			SAVED=$(( BEFORE - AFTER ))
			PCT="$(awk -v a="$BEFORE" -v b="$AFTER" 'BEGIN { printf "%.1f", (a>0 ? (a-b)*100/a : 0) }')"
			ok "$(basename "$TARGET"): $(human_size "$BEFORE") → $(human_size "$AFTER")（减少 ${PCT}%）"
			ROWS+=("$(basename "$TARGET")|$(human_size "$BEFORE")|$(human_size "$AFTER")|${PCT}%")
		else
			warn "$(basename "$TARGET") UPX 压缩失败，保留未压缩产物"
			ROWS+=("$(basename "$TARGET")|$(human_size "$BEFORE")|$(human_size "$BEFORE")|0.0%")
		fi
	done
else
	step "跳过 UPX 压缩"
	for TARGET in "${BUILD_TARGETS[@]}"; do
		SIZE="$(file_size "$TARGET")"
		ROWS+=("$(basename "$TARGET")|$(human_size "$SIZE")|$(human_size "$SIZE")|0.0%")
	done
fi

# ------------------------------- 结果汇总 -------------------------------

step "构建结果"

printf "%-44s %12s %12s %8s\n" "产物" "压缩前" "压缩后" "压缩率"
printf "%s\n" "----------------------------------------------------------------------------------"
for ROW in "${ROWS[@]}"; do
	IFS='|' read -r NAME B A P <<< "$ROW"
	printf "%-44s %12s %12s %8s\n" "$NAME" "$B" "$A" "$P"
done

echo
info "产物目录：${SCRIPT_DIR}/${OUT_DIR}"
info "版本信息已内嵌，启动日志首行会打印 version/commit/built。"
echo
printf "${C_BOLD}运行方式${C_RESET}\n"
printf "  Linux:   ./%s/ci-platform-linux-amd64            # 默认监听 :8080，数据目录 ./data\n" "$OUT_DIR"
printf "  Windows: %s\\\\ci-platform-windows-amd64.exe      # 双击或命令行启动\n" "$OUT_DIR"
printf "  自定义:  CI_ADDR=:9000 CI_DATA_HOME=/var/lib/ci ./%s/ci-platform-linux-amd64\n" "$OUT_DIR"
echo
printf "${C_OK}前端已内嵌，部署时只需拷贝该单个可执行文件。${C_RESET}\n"
