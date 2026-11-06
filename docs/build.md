# 构建与验证

[← 返回 README](../README.md)

## 构建脚本 `build.sh`

在 Windows 上通过 Git Bash 运行，也可直接在 Linux / macOS 运行。完整流程：

| 步骤 | 动作 |
|---|---|
| 1. 环境检查 | 校验 `go` / `node` / `npm` / `upx`，任一缺失给出明确提示（UPX 缺失自动降级跳过） |
| 2. 前端构建 | `node_modules` 缺失时自动 `npm ci`（无 lock 则 `npm install`），再执行 `npm run build` |
| 3. 内嵌校验 | 确认 `web/dist/index.html` 存在，否则中止并给出修复命令 |
| 4. 交叉编译 | `CGO_ENABLED=0 GOOS/GOARCH` + `-trimpath -ldflags "-s -w -X main.buildVersion=..."` |
| 5. UPX 压缩 | `upx --best --lzma` 逐产物压缩，打印压缩前后大小与压缩率 |
| 6. 结果汇总 | 输出产物表格与运行方式 |

### 参数

| 参数 | 说明 |
|---|---|
| `--platforms <列表>` | 目标平台，逗号分隔，默认 `linux/amd64,windows/amd64` |
| `--single` | 仅构建当前平台（本机快速验证） |
| `--skip-web` | 跳过前端构建（要求 `web/dist/index.html` 已存在） |
| `--no-upx` | 跳过 UPX 压缩 |
| `--upx-level <参数>` | UPX 级别，默认 `--best --lzma` |
| `-h, --help` | 帮助 |

```bash
# 三平台全量构建，不压缩
./build.sh --platforms linux/amd64,linux/arm64,windows/amd64 --no-upx

# 仅本机验证
./build.sh --single
```

### 实测构建结果

```
产物                                          压缩前    压缩后 压缩率
----------------------------------------------------------------------------------
ci-platform-linux-amd64                          11.62 MB      3.37 MB    71.0%
ci-platform-windows-amd64.exe                    12.10 MB      3.61 MB    70.2%
```

产物已用文件头校验：Linux 为 `ELF`（`7f454c46`），Windows 为 `MZ`（`4d5a9000`）。
经 UPX 压缩后的 Windows 产物已实测可正常启动并完整提供 Web 界面。

### 版本信息注入

脚本会把 `git describe`、短 commit、UTC 构建时间通过 `-ldflags -X` 注入，启动首行即可见：

```
ci-platform version=v1.0.0 commit=a1b2c3d built=2026-10-22T07:28:54Z
web assets source: embedded
ci-platform listening on :8080 (data_home=./data, workers=3, queue=100)
```

---
