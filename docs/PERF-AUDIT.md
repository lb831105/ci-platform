# 性能审计（面向多用户）

> 审计方式：真实二进制 + 真实文件存储 + 造数压测，不是纸面推演。
> 造数：**200 个任务 × 40 次构建 = 8000 个 `build.json`**（另含 8000 个 `log.txt`，data 目录 17 MB）。
> 环境：Windows 10 / 单进程 / 默认 `-workers 3`。Linux ext4 的绝对数字会快 3–5 倍，
> 但下文所有问题都是 **O(任务数 × 构建数) 的全盘扫描**，结论不随平台变化。

---

## 1. 结论

**瓶颈不在构建执行，而在只读 API：三个接口每请求都把全部构建记录从磁盘重读一遍，没有任何索引或缓存。**

| 接口 | 单请求耗时 | 并发表现 | 应答体 |
|---|---|---|---|
| `GET /api/overview`（任务列表，前端每 3 秒轮询） | **1856 ms** | 16 线程 → **3.5 rps**，p95 **5.0 s** | 224 KB |
| `GET /api/system/info` | **1693 ms** | — | 0.7 KB |
| `GET /api/system/load` | **2972 ms** | — | 0.3 KB |
| `GET /api/builds`（全局构建历史） | **1676 ms** | 8 线程 → 3.2 rps | **2238 KB** |
| `GET /api/pipelines/job-0001/builds`（单任务） | 9.8 ms | — | 10 KB |
| `GET /api/pipelines/job-0001`（带 jobPerm） | 1.7 ms | — | 0.6 KB |
| `GET /api/pipelines`（200 任务配置，无构建） | 46 ms | — | 122 KB |

换算成人：任务列表页在有构建运行时每 3 秒刷新一次，**约 20 个同时在线用户就能把 overview 打到饱和**（3.5 rps ≈ 10 人/3s 的刷新量），此时 p95 已经 5 秒。构建数增长后会更糟：这套开销随 `任务数 × 构建数` 线性增长，且**构建保留策略默认不限制**（见 4.1），等于随时间单调恶化。

对比：**单任务级接口（构建历史、日志、任务详情）都在 10 ms 以内，完全没问题**。问题集中在"跨任务聚合"这一层。

---

## 2. 根因（按严重度）

### P0-1 `overview` / `builds` / `system.info` / `system.load` 都靠「逐任务逐构建读文件」

| 位置 | 行为 |
|---|---|
| `internal/service/build_service.go:257 Summaries` | 对每个任务调用 `builds.ListByPipeline` → `os.ReadDir` + **逐个 `build.json` 反序列化** |
| `internal/service/build_service.go:269 AllBuilds` | 同样的全扫，再拼成一个大数组 |
| `internal/service/system_service.go:65 Info` | **只为了数 running 数量**，就调用了 `AllBuilds`（读 8000 个文件算三个整数） |
| `internal/service/system_service.go:115 Load` | `AllBuilds` + 两次 `dirUsage`（递归 walk 全部 16k 文件） |

实测拆解（8000 构建）：`AllBuilds` ≈ 1.6 s；`dirUsage` 两次 ≈ 1.3 s；`Summaries` ≈ 1.86 s。
**没有任何缓存层**：`store` 层是纯文件读写，`service` 层每次调用都重新走一遍。

修复方向（不改存储格式，也不引入数据库）：
1. **每任务构建索引** `data/jobs/{id}/builds/index.json`：写入 `build.json` 时同步维护（任务级只有 1 次额外小文件写）。
2. **进程内缓存**：`map[pipelineID]summary`，写构建时失效/更新单条 —— overview 变成纯内存 O(任务数)，**1.86 s → 个位数毫秒**。
3. **计数走索引**：`Info`/`Load` 的 running/success/failed 直接用索引维护的计数器，不再扫全量。

### P0-2 `system/load` 的目录体积统计是同步递归遍历

`internal/service/system_service.go:468 dirUsage` → `dirSize` 对 `data/jobs` 与 `workspace` 递归 `Walk`。
16k 文件 ≈ 1.3 s，且**每次请求都算**（系统配置页还会 `info + load + settings` 三连发）。

修复：后台定时（5 分钟）统计并缓存结果，接口只读缓存；首次启动显示「计算中」。→ **2.97 s → ~2 ms**。

### P0-3 `/api/builds` 无分页、无上限

8000 条构建 → 2.2 MB JSON、1.68 s，前端 `el-table` 会把全部行渲染出来。
构建数到 5 万时：约 14 MB 应答、10 s+，浏览器直接假死。

修复：`?limit=&cursor=`（默认 100，上限 500）+ 前端分页/虚拟滚动；跨任务历史默认只查最近 N 天。

### P1-1 `/api/pipelines` 每请求读 N 个 `config.json`

200 任务 46 ms（0.23 ms/任务），1000 任务 ≈ 230 ms。任务列表、overview、builds 都会先调它。
修复：进程内缓存 + 写时失效（配置变更极少，缓存命中率接近 100%）。

### P1-2 日志接口整文件读进内存再 JSON 包装

`BuildStore.ReadLog` 用 `os.ReadFile` 读全文件 → `string` → JSON 序列化 → 无 gzip、无 Range。
实测 20.4 MB 日志 → 应答 20.8 MB，**每次打开构建日志都要传整份**。
修复：默认返回尾部 N 行（如 5000 行）+ `?tail=` 参数；支持 `Range` 断点续读；开启 gzip（日志压缩率通常 5–10 倍）。

### P1-3 RBAC 每次权限检查都编译正则

`internal/service/rbac_service.go:158 matchJobPattern`：`regexp.Compile` 在**每次** `HasJobPermission` 调用里执行，
且 `rolesFor` 每次复制角色切片。管理员因短路不受影响，**普通用户每个请求都要付这个成本**（任务角色越多越慢）。
修复：按 Pattern 缓存编译结果（`sync.Map`，保存角色时清空）；`rolesFor` 返回索引而非复制。

### P1-4 审计写盘是同步 + fsync，且全局串行

`internal/store/audit_store.go:34 Append` 每条约一次 `f.Sync()`，全局 `mu`。
实测并发登录（每个都写审计）→ **124 rps 上限**。所有写操作（保存任务、触发构建、改配置）都走这条路。
修复：改成「后台批量写 + 定期 Sync」（审计价值仍保留，可配置 `audit_sync` 严格模式），或按天/按大小分文件降低 contention。
风险提示：这是**功能正确性 vs 吞吐**的取舍，改造时保留"严格模式"开关。

### P2（当前不致命，但要知道）

| 位置 | 问题 | 影响 |
|---|---|---|
| `BuildService.Recover` / `PruneAll` | 启动与每小时全量扫描 | 10 万构建时启动慢 30 s+ |
| `BuildStore.nextBuildIDLocked` | 全局 `mu` 内 `ReadDir` | 跨任务并发创建编号会短暂排队 |
| `Handler.Overview` 的 `sanitizePipeline` | 每任务深拷贝 SSH 发布列表 | 200 任务下可忽略 |
| `settings.Current()` | 每次返回整个 Settings 浅拷贝 | 可忽略 |
| WS 日志 | 每观看者一个 goroutine + 缓冲，慢消费者丢帧 | 设计正确，未发现问题 |
| 产物 zip | 流式写出 | 正确，未发现问题 |
| 调度器 `Enqueue` | 非阻塞 + 容量校验 | 正确；但 `-workers 3 / queue 100` 是**真实并发上限**，需容量规划 |

---

## 3. 扩展性推算（现状，overview 单请求）

| 任务数 | 构建/任务 | 构建总数 | overview | 结论 |
|---|---|---|---|---|
| 50 | 20 | 1 000 | ~230 ms | 可用 |
| 200 | 40 | 8 000 | **1.86 s** | 已不可接受 |
| 200 | 200 | 40 000 | ~9 s | 页面基本打不开 |
| 500 | 100 | 50 000 | ~11 s | 同上，且 `/api/builds` 约 14 MB |

修复后（索引 + 内存缓存 + 分页）预估：8000 构建 → overview **< 10 ms**，`/api/builds` 一页 **< 20 ms**；
同一台机器可支撑 **数百 rps** 的列表请求（100 用户 × 3 秒轮询 = 33 rps，余量充足）。

---

## 4. 放大器：两个默认配置让问题随时间恶化

### 4.1 构建保留策略默认「不限制」

`settings.build_retention_max_per_job = 0` 且 `PruneAll` 在没有全局策略时直接 return。
→ 构建数只增不减，把 P0-1 的线性开销变成"永远在涨"。
建议：新装默认 `200 条 / 90 天`（可在界面改回 0），并在系统配置页显示当前构建总数与"建议开启保留策略"。

### 4.2 前端轮询策略与服务端开销相乘

`web/src/views/PipelineListView.vue`、`BuildHistoryView`、`BuildLogView` 都是 2–3 秒轮询（有运行中构建时才轮询，这点是对的）。
但**轮询成本 × 在线人数**：现状 3.5 rps 饱和 → 20 人即打满。
修复后服务端压力消失，但仍建议中期改成 **SSE/WebSocket 推送构建状态变化**（平台已有 WS 基建），把 N 人轮询变成 1 条广播。

---

## 5. 建议的修复顺序（含验收指标）

| 步骤 | 内容 | 工作量 | 验收 |
|---|---|---|---|
| **A（必做）** | 构建索引（每任务 `index.json` + 进程内缓存 + 写时更新）；`Info`/`Load` 改用计数器 | 3–4 人日 | 8000 构建下 overview < 10 ms；16 线程并发 > 200 rps |
| **B（必做）** | `/api/builds` 分页（limit/cursor）+ 前端分页；`/api/pipelines` 加缓存 | 1–2 人日 | 应答 < 200 KB；5 万构建时 P95 < 50 ms |
| **C（必做）** | `dirUsage` 后台定时 + 缓存；日志接口 `tail`/gzip | 1–2 人日 | `/api/system/load` < 10 ms；20 MB 日志首屏 < 300 KB 传输 |
| **D（建议）** | RBAC 正则缓存；审计异步批量（保留严格模式） | 1 人日 | 权限检查 < 50 µs；写并发 124 → 1000+ rps |
| **E（中期）** | SSE/WS 推送构建状态，替代列表轮询 | 2–3 人日 | 100 人在线下后端 CPU < 5% |

以上 A+B+C 是"很多人用"的最低门槛，约 **6–8 人日**；完成后同一台机器从"20 人"提升到"数百人量级"。

---

## 6. 复现方法（建议常备）

```bash
# 造数：200 任务 × 40 构建
python seed.py .tmp-perf/data 200 40
# 起服务
go build -o bin/ci-server-perf.exe ./cmd/server
./bin/ci-server-perf.exe -port 18341 -data-home .tmp-perf/data
# 打点（单请求 + 并发）
python bench.py
```

关键点：**别用真实构建造数**（太慢），直接写 `config.json` / `builds/{n}/build.json` 即可，
存储层就是文件，这样能在一分钟内复现任何规模。

---

## 7. 顺带确认没问题的部分

- 单任务构建历史 / 日志 / 详情：10 ms 内（`os.ReadDir` + 少量文件）
- 产物打包下载：`archive/zip` 流式写出，不占内存
- 日志实时推送：`Hub` 快照广播 + 慢消费者丢帧，不阻塞构建
- 调度器：非阻塞入队、同任务串行、无泄漏 goroutine
- 审计轮转：达阈值即 rename，不会无限增长
