# 运行机制（Operations）

> **上位约束**：[`DECISIONS.md`](./DECISIONS.md)（决策记录，最高约束）。
> **表结构**：[`DATA-MODEL.md`](./DATA-MODEL.md)（**唯一真源**，本文不重复整表）。
> **接口契约**：[`API.md`](./API.md)（对外 REST / SSE / agent 内部契约）。
>
> 本文回答「系统跑起来之后，各种机制到底怎么运作」，是实现的**行为规范**。
> 包路径以 `ARCHITECTURE.md` 为准；本文在需要时给出建议包名（如 `internal/upload`）。

## 命名与路径约定（读本文前必读）

| 维度 | 约定 | 依据 |
|---|---|---|
| **内部 API 前缀** | **`/api/web/v1/**`** | D80 |
| Lsky 兼容层前缀 | `/api/v1/**`（外部冻结契约，与内部 API **天然隔离**，不冲突） | D52 / D80 |
| **数据库表名 / 列名** | **PascalCase**（`Uploads` / `UserUID` / `CreatedAt`） | D81 |
| **API JSON 字段** | **PascalCase**，缩写词全大写（`JobUID` / `StorageUID` / `URL`） | D81 |
| 枚举**取值**（字符串） | 保持小写点号风格（`upload` / `user.create` / `succeeded`） | D81.3 |
| 环境变量 | 仍 `UPPER_SNAKE_CASE`（`PICGO_WEB_LISTEN` …） | D81.3 |
| **配置键** | 仍 **`dot.lowerCamel`**（`upload.concurrency` / `mail.host` …），存进 `SystemSettings.Key` | D81.3 |
| picgo 侧字段名 | 一律原样（`picBed` / `_configName` / `IImgInfo` 的 `fileName` 等） | D81.3 |
| **前端归属** | **内置 SPA**（`go:embed web/dist`）+ 主题按 `manifest.Pages` 接管声明的路径；首次只接管 `/` | D94 |
| **资源前缀** | 主题用 **`/theme-assets/**`**；内置 SPA 用 **`/assets/**`**（**不得混用**） | D99.2 |
| Lsky 兼容层 JSON | 仍 snake_case（`strategy_id` / `capacity`），包络 `{status, message, data}` | D81.3 |

> 本文正文中的表名列名**一律 PascalCase**；配置键与枚举取值按上表保持原样。

---

## 0. 机制总览

| 机制 | 归属 | 主要表 | 决策 |
|---|---|---|---|
| 上传队列（Job / JobItem） | Go | `Jobs` `JobItems` | D35–D39 |
| 启动恢复 / 优雅关闭 | Go + agent | `Jobs` `JobItems` | D41 |
| 存储配额 | Go | `Users` `Albums` | D20 D21 D72 |
| 上传限流（**默认禁用**） | Go | `Uploads`（计数来源） | D40 D73 |
| 操作日志 | Go | `OperationLogs` | D45 D74 |
| 邮件 + 邮件日志 | Go | `EmailLogs` `OperationLogs` | D29 |
| 存储驱动同步 | Go ↔ agent | `StorageConfigs` `StorageSecrets` | D22 D64 D65 |
| 主题系统（前端页面接管） | Go | `ThemeConfigs`（配置值） | D94–D99 |
| 删除（含远端） | Go → agent | `Uploads` `UploadResults` `OperationLogs` | D46 D47 |
| PicGo-Core 补丁 | agent | — | D48–D51 |
| 任务日志 / 可观测性 | Go + agent | `JobLogs` | D17 D45 |

**统一原则**：Go 侧是**唯一决策者与唯一写库者**；agent 是无状态执行器（除 `config.json` 与 `node_modules`）；
所有副作用（进度、日志、状态）都由 Go 落库后再经 SSE 推给浏览器。

---

## 1. 上传队列

### 1.1 两层结构（D36）

```
POST /api/web/v1/uploads     一次 HTTP 请求 = 1 个 Job（含 N 个文件）
        │                    D38：一个批次只能选一个存储驱动（StorageUID）
        ▼
   Jobs             status: queued → running → succeeded | failed
        │ 拆成 N 行
        ▼
   JobItems         每个文件一行，并发限制作用在这一层
        │ 1:1
        ▼
   Uploads          图片元数据（热点表，供图库列表查询）
        +
   UploadResults    完整上传返回值（大 JSON，仅删除远端时需要）
```

| 层 | 表 | 关键列 | 说明 |
|---|---|---|---|
| 批次 | `Jobs` | `UID` `Kind` `Status` `Progress` `UserUID` `StorageUID` `TotalItems` `SucceededItems` `FailedItems` `SkippedItems` | `StorageUID` 在 Job 层（D38），`JobItems` 不重复存 |
| 单文件 | `JobItems` | `JobUID` `Seq` `UploadUID` `Status` `Attempts` `Error` | `Seq` 保证前端展示顺序稳定 |
| 元数据 | `Uploads` | `UID` `Status` `URL` `Size` `JobUID` … | `Status` = `pending` / `success` / `failed` |
| 原始返回值 | `UploadResults` | `UploadUID` `RawOutput` `FilePath` | `RawOutput` 是完整 `IImgInfo` JSON（D47 必需） |

**两套状态命名的映射**（避免混用）：

| `JobItems.Status` | 对应 `Uploads.Status` | 含义 |
|---|---|---|
| `queued` | `pending` | 已受理，等待 worker |
| `running` | `pending` | 正在上传 |
| `succeeded` | `success` | 成功，`URL` 已写入 |
| `failed` | `failed` | 最终失败（重试已用尽），`Error` 已写入 |

> `Jobs.SkippedItems` 是**预留字段**（D77「宁可先留着不用」）。
> 本项目已取消内容去重（D66），当前不存在 `skipped` 的产生者，恒为 `0`。
> **不要为它编造逻辑。**

### 1.2 状态机

**Job**

```
                 ┌──────────────────────────────────────┐
                 │                                      │ 有 item failed
   [入队] ──► queued ──► running ──┬──► succeeded ──────┼──► 结束
                                   │   (全部 item 成功)   │
                                   └──► failed ──────────┘
                                        (≥1 item 失败)
```

- 只有 4 个状态，**没有 `partial`**（D37）。部分成功时 Job = `failed`，
  但 `Jobs.Result` 里带完整统计 `{total, succeeded, failed, skipped}`，
  **成功项的 URL 照常回传，数据不丢**。

**JobItem**

```
   queued ──► running ──┬──► succeeded
     ▲                  │
     │                  └──► failed ──(attempts <= upload.retryTimes)──► queued（重试）
     └────────────────────────────────────────────────────────────────────┘
```

- `Attempts` 在每次进入 `running` 前 +1，**重试不清零**。
- 重试上限判定：`Attempts > upload.retryTimes` 时不再重试 → 终态 `failed`。
  （默认 `retryTimes = 1` → 最多 2 次执行：首次 + 1 次重试。）

### 1.3 请求受理顺序（重要）

```
POST /api/web/v1/uploads
 ① 鉴权                       未登录 → 40102
 ② 参数校验                   扩展名白名单 / 单文件大小 / 文件数 > 0 → 40001
 ③ 解析 StorageUID            未配置可用存储 → 40001
 ④ 队列容量校验               在队 Job 数 ≥ upload.queueMaxLength → 42901
 ⑤ 配额校验（D20）            仅非 admin；超限 → 40302
 ⑥ 上传限流（D73）            仅非 admin 且 enabled=true；超限 → 42901（或仅记日志）
 ⑦ 落盘暂存 + 计算 SHA256     写入 data/uploads/…
 ⑧ 建记录                     1 个 Job + N 个 JobItems + N 个 Uploads(pending)
 ⑨ 返回 {JobUID, Items[]}     异步执行，进度经 SSE 推送
```

- ④ 放在 ⑦ 之前：**不落盘就能拒绝**，避免磁盘被灌满。
- ⑤⑥ 的判定**都在受理时一次性完成**（整批一起校验），不做逐文件中途拦截——
  否则会出现「删了一半配额用完」的尴尬状态。
- ⑥ 的计数口径见 §4.3。

### 1.4 并发控制（D35）

```
worker 数量 = upload.concurrency        （默认 1）
限制作用层级 = JobItem                  （D36：粒度最合适）
```

- **`concurrency = 1` 时严格串行**：一个文件完整跑完（含重试）才取下一个。
  前端按「第 k / 共 N 张」展示，不是转圈。
- `concurrency > 1` 时无需改业务代码，但**依赖 picgo-core 补丁**
  （P1–P4，见 §10 与 `PICGO-INTEGRATION.md`）。
- worker 池是**全局唯一**的（跨用户、跨批次共享），不是每请求一个池。
  否则并发限制形同虚设。
- worker 取任务顺序：`Jobs.CreatedAt` 升序 → `JobItems.Seq` 升序（FIFO，避免大批次饿死小批次）。

### 1.5 重试与超时

| 配置键 | 默认 | 行为 |
|---|---|---|
| `upload.retryTimes` | `1` | 终态失败前的重试次数 |
| `upload.retryBackoffMs` | `2000` | 退避基数，**指数增长**：`backoff × 2^(Attempts-1)` |
| `upload.itemTimeoutSeconds` | `300` | 单文件超时 |

- 退避期间 item 回到 `queued`，但**带一个可重试时间戳**（内存队列的延迟槽），
  不占用 worker，避免退避把并发槽位堵住。
- 超时处理：Go 侧 `context.WithTimeout` 放弃等待 → item 标记失败（可重试）。
  ⚠️ **agent 侧的上传不会因此中断**（HTTP 断开无法取消 picgo 的进行中上传）。
  因此超时后 agent 可能仍在跑并最终成功；这种情况会在 agent 日志里留痕，
  但**以 Go 侧判定为准**（用户已收到失败）。实现时在 Go 日志中打 `WARN` 记录该超时 item，
  便于事后排查「图床上有图但系统说失败」。
- ⚠️ **失败判定不看 HTTP 状态码**：agent 的 `POST /api/upload` 在失败时
  **返回 HTTP 200 + `Code = "ERR_PICGO"`**（见 `API.md` §13.5），
  以便 Go 逐项记录失败原因而不引发重试语义混乱。
  Go 侧必须：`HTTP 200 && Code == "OK" && Data.URL != ""` 才能判为成功；
  其余情况（含网络错误、超时、`ERR_PICGO`）统一走失败分支。

### 1.6 进度计算

**主口径：已完成 item 数 / 总 item 数**

```
done          = Jobs.SucceededItems + Jobs.FailedItems
fraction      = 当前 running item 的单文件进度（0 / 0.3 / 0.6 / 1.0）
progress(%)   = floor((done + fraction) / TotalItems * 100)
```

**为什么这样算（D39）**：agent 的 `/api/upload` 设计为**单文件 + 同步**，
所以 picgo 的 `uploadProgress` 事件（只有 `0 / 30 / 60 / 100 / -1` 五档）
就变成了**这个文件自己的进度**，而不是整批的。

| `uploadProgress` | 含义 | 折算 `fraction` |
|---|---|---|
| `0` | 开始 transform | `0.0` |
| `30` | transformer 完成 | `0.3` |
| `60` | 进入 uploader | `0.6` |
| `100` | 上传成功 | `1.0`（随即被 done 计数取代） |
| `-1` | 失败 | 不计入 fraction，直接走失败路径 |

- 因为严格串行时同时只有一个 `running` item，「当前文件进度」无歧义。
- `concurrency > 1` 时事件无法按文件归属（picgo 的事件挂在根 `EventEmitter` 上），
  此时**只依据 `done / total` 计算**，不插值——进度条会呈阶梯状，属于已知取舍。
  （补丁的 `contextData` 能让事件归属到 Job，但仍无法细分到文件。）
- 前端展示：整批进度条 + 每张缩略图上的单文件状态标签；
  SSE 事件 `upload.progress` 的 payload 见 `API.md` §8。

### 1.7 队列容量

- 判定：`在队 Job 数（queued + running） >= upload.queueMaxLength` → 拒绝新请求。
- 返回 `42901`，消息区分两种来源（便于前端提示）：
  - 队列满：「当前上传队列已满，请稍后重试」
  - 限流：「上传过于频繁」
- `queueMaxLength` 默认 1000，按 **Job 数** 计（不是文件数）——
  因为一个 Job 可能含 100 个文件，按文件数计会被单个大请求打穿。

---

## 2. 启动恢复与优雅关闭（D41）

### 2.1 启动恢复

进程重启时，DB 里会残留「上次未跑完」的记录。启动顺序：

```
1. 加载配置 → 连接数据库 → 执行迁移（DATA-MODEL.md §9）
2. 确保 / 探测 agent（§2.3）
3. 存储配置 reconcile（§7.3，幂等）
4. 恢复任务（下面 5 条）
5. 启动 worker 池（并发 = upload.concurrency）
6. 启动 HTTP 监听 + SSE
```

**恢复规则**

| 原状态 | 处理 |
|---|---|
| `JobItems.Status IN (queued, running)` | → `queued`；`Attempts` **保留**（不重置，防止无限重试）；`StartedAt` 清零 |
| `Jobs.Status IN (queued, running)` | → `running`，参与调度；`StartedAt` 保留原值或补写当前时间 |
| `Uploads.Status = pending` | 保持 `pending`（等其 item 重新跑） |
| `JobItems` 有 `UploadUID` 但 `Uploads` 行不存在 | 该 item 直接 `failed`，`Error = "源文件已清理"` |
| `UploadResults.FilePath` 指向的文件已不存在 | 该 item 直接 `failed`，`Error = "源文件已清理"` |
| 不属于任何 `Jobs` 行的 `JobItems` | 视为脏数据，标记 `failed`（不屏蔽启动） |

- **为什么重置 running 而不是 failed**：进程被杀时无法判断上传是否已成功，
  重置重试是唯一安全选择（代价是可能重复上传一张图；这是 D66 取消去重后
  我们接受的代价——图床会多一个孤儿文件，但不影响正确性）。
- 恢复完成后，若某 Job 的**全部 item 已终态**，直接结算 Job 状态并写 `OperationLogs`。

### 2.2 优雅关闭

收到 `SIGTERM` / `SIGINT` 时（`internal/upload` 监听 `signal.NotifyContext`）：

```
① 置 shuttingDown = true
   → POST /api/web/v1/uploads 立即返回 503（Code 50001，消息「服务正在重启，请稍后重试」）
   → 其余读接口照常（列表、详情、SSE 不断）

② 通过 SSE 广播 system.notice 事件（前端弹「服务正在重启」）
   事件：system.notice，data = { Level: "warn", Message: "服务正在重启，请稍后重试" }
   （契约见 API.md §8）

③ 等待正在 running 的 item 完成，最多 upload.shutdownGraceSeconds（默认 30）
   → 不取新任务，只等当前批
   → 等待期间照常推送进度

④ 超时仍有 running item
   → 将其重置为 queued（Attempts 保留），落库
   → 同时把未开始的 queued item 保持 queued

⑤ 结算：所有 item 已终态的 Job 更新状态 + 写 OperationLogs
   未完成的 Job 保持 running（下次启动按 §2.1 恢复）

⑥ 调用 agent POST /api/shutdown 优雅退出，超时则 SIGKILL

⑦ 关闭 DB 连接池 → 退出
```

- **不丢数据**：所有状态在 ④⑤ 都落库，⑤ 之后即使被强杀也不影响下次恢复。
- 前端体验：SSE 断开后前端展示「连接已断开，正在重连」，并轮询 `GET /healthz`
  直到恢复；恢复后自动刷新任务列表。

### 2.3 agent 的生命周期与健康探测（D7）

| 场景 | 行为 |
|---|---|
| `PICGO_WEB_AGENT_AUTOSTART=true`（默认） | Go 启动时以子进程方式拉起 agent，注入 `PICGO_AGENT_CONFIG_PATH` 与 `PICGO_WEB_AGENT_TOKEN`；Go 退出时一并关闭 |
| `PICGO_WEB_AGENT_AUTOSTART=false` | Go 不拉起，直接连 `PICGO_WEB_AGENT_URL`（独立部署 / 多实例复用 agent） |
| 健康探测 | `GET /healthz`，**2s 超时**；正常间隔 10s，异常期间间隔 2s |
| 探测失败 | 标记 `agentStatus = down`；**退避重启**：1s → 2s → 4s → 8s → 16s（最多 5 次） |
| 5 次仍失败 | 停止自动重启，`agentStatus = down` 并记 `WARN`；后台提供「手动重启内核」按钮 |
| agent 恢复 | 探测成功后：`agentStatus = up` → **重新执行存储 reconcile（§7.3）** → 清理 agent 侧陈旧 Job 缓存 |
| agent 不可用时的接口 | 依赖 agent 的请求返回 `503` + `Code 50002`；纯 DB 接口（图库列表、日志、设置）**照常可用** |

**端口**：Go `8080`（对外唯一入口）、agent `127.0.0.1:36678`、
picgo 内置 server `36677`（**默认不启用**）。

**降级矩阵**（前端据此禁用按钮）：

| 功能 | agent down 时 |
|---|---|
| 图库浏览 / 搜索 / 编辑元数据 / 复制外链 | ✅ 可用 |
| 上传 | ❌ 503（队列不接收，直接拒绝，**不排进队列**） |
| 远端删除 | ❌ 503，但可走「仅删本地记录」 |
| 存储配置增删改查 | 🟡 元数据可改（DB 写入成功），但同步到 agent 失败 → 标记配置 `Metadata` 内 `SyncPending=true`，恢复后 reconcile 补齐 |
| 插件管理 | ❌ 503 |
| 操作日志 / 邮件 | ✅ 可用 |

> `SyncPending` 放在 `StorageConfigs.Metadata` JSON 内，**不新增列**（D77）。
> （`Metadata` / `Detail` 这类**会经 API 暴露**的 JSON 列，其内部键也用 PascalCase，与 `Capabilities` 同理。）

---

## 3. 配额（D20 / D21 / D72）

### 3.1 口径

| 概念 | 列 | 语义 |
|---|---|---|
| 配额上限 | `Users.CapacityBytes` | **`0` = 不限额** |
| 已用量 | `Users.UsedBytes` | 该用户**当前持有**的图片体积合计（字节） |
| 单图体积 | `Uploads.Size` | 上传成功后由 agent 返回值写入 |

### 3.2 校验时机

- **受理时一次性校验**（§1.3 第 ⑤ 步）：
  ```
  capacity = Users.CapacityBytes
  if capacity == 0            → 放行（不限额）
  if UsedBytes + Σ(本批文件大小) > capacity → 40302「存储空间不足」
  ```

  > `40302` 是**配额不足专属码**（`API.md` §0.3），与 `40301`「权限不足」区分。
  > `DECISIONS.md` D20 早期写的 `40301` 已修正为 `40302`。
- 校验用的是**上传前的文件字节大小**（客户端声明的 Content-Length / 落盘后实测大小），
  因为此时还没有图床返回的最终大小。
- **管理员（`Role = admin`）跳过**（D20 + 附录）。
- 上传成功后按 **agent 返回的 `Size`** 累加 `UsedBytes`（以真实上传值为准；
  若 agent 未返回 `Size`，回退用本地文件大小）。

### 3.3 退还（D72）

| 触发 | 行为 |
|---|---|
| 删除图片记录 | `UsedBytes -= Uploads.Size` |
| 是否真删远端 | **无关**。远端删除失败或驱动不支持删除时，配额照退 |
| 结果为负 | 归零 + 写一条 `WARN` 级操作日志（在 `Detail` 里标注 `{Negative:true}`） |

**为什么与远端无关**：`UsedBytes` 的语义是「用户当前持有多少张图」，
不是「图床真实占用多少字节」。图床空间统计属于图床自己的事（D2/D66 的同一逻辑）。

### 3.4 新建用户的默认配额（D21）

```
创建用户时：
  if user.unlimitedCapacity == true → CapacityBytes = 0
  else                              → CapacityBytes = user.defaultCapacityBytes（默认 5 GiB）
  Status                            = user.defaultStatus（默认 active）
```

- 修改 `user.defaultCapacityBytes` **只影响此后新建的用户**，不回溯（已在 DATA-MODEL.md 注明）。
- 管理员可在用户详情页**逐个**覆盖 `CapacityBytes`（写 `OperationLogs` 类型 `user.update`）。

### 3.5 冗余计数的一致性

被冗余维护的计数：

| 列 | 增加时机 | 减少时机 |
|---|---|---|
| `Users.UsedBytes` | 上传成功（`+= Size`） | 删除记录（`-= Size`） |
| `Albums.ImageCount` | 上传成功且归属该相册 | 删除记录 / 移出相册 |

**维护要求**

1. **只允许 service 层改**：通过 `internal/repository` 暴露的专用方法
   （如 `AddUsedBytes(tx, userUID, delta)`），禁止在 handler 或别的 service 里裸写。
2. **与业务写入同事务**：上传成功时「更新 `Uploads` + 累加 `UsedBytes` + `ImageCount++`」
   必须在**同一个 DB 事务**内，避免漂移。
3. **移动相册**（`POST /api/web/v1/albums/{uid}/move-uploads`）：
   同一事务内「源相册 `-1`、目标相册 `+1`」，`UsedBytes` **不变**。
4. 漂移可能来源：进程在事务外被杀、人工改库、历史数据迁移。

**对账（重算）**

```
SQL（两方言通用，GORM 表达即可；注意 PgSQL 需给标识符加双引号，D81.4）：
  SELECT "UserUID", COALESCE(SUM("Size"), 0) FROM "Uploads"
   WHERE "Status" = 'success' GROUP BY "UserUID"
  → 与 Users.UsedBytes 逐条比对

  SELECT "AlbumUID", COUNT(*) FROM "Uploads"
   WHERE "AlbumUID" <> '' GROUP BY "AlbumUID"
  → 与 Albums.ImageCount 逐条比对
```

| 方式 | 说明 |
|---|---|
| **随每日维护任务静默对账**（唯一方式） | 与日志清理同一个定时器（§5.5）。**正常时静默**（不写日志，避免噪音）；**发现偏差才写一条日志**（`system.recount`，`Detail` 记录修正条数与差值） |

> ⚠️ **不提供手动对账端点**。曾考虑过 `POST /system/maintenance/recount`，
> 已裁决**不做**（与 D82「不做备份/恢复」同一取向：运维动作走定时任务，不暴露成 API）。
> 若将来确需手动触发，必须先在 `API.md` 登记端点，再实现。

> 对账过程中发现的负值一律归零（D72 边界）。

---

## 4. 上传限流（D40 / D73）

### 4.1 规则

| 维度 | 值 |
|---|---|
| 统计对象 | **只按张数**（不做流量维度） |
| 作用域 | 按登录用户（`Uploads.UserUID`）。本项目**没有游客上传**，不需要按 IP |
| 管理员 | **直接跳过**（与跳配额一致） |
| 默认状态 | **禁用**（`upload.rateLimit.enabled = false`） |

### 4.2 配置键（DATA-MODEL.md §7.4「上传」）

| 键 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `upload.rateLimit.enabled` | bool | **`false`** | 总开关 |
| `upload.rateLimit.perHour` | int | `100` | 每用户每小时最多张数 |
| `upload.rateLimit.perDay` | int | `500` | 每用户每天最多张数 |
| `upload.rateLimit.action` | string | `reject` | `reject` 拒绝 / `log` 仅记录不拦 |

- **全部存数据库**（`SystemSettings`），管理员在「系统设置 → 上传」里改，
  **改完立即生效、不需要重启**：
  `SettingsService` 的 `onChanged` 回调把新阈值推给内存限流器（atomic 替换，无锁读）。

### 4.3 计数来源：**直接用 `Uploads` 表，不额外建表**

```
perHour 用量 = count("Uploads" WHERE "UserUID" = ? AND "CreatedAt" >= now-3600)
perDay  用量 = count("Uploads" WHERE "UserUID" = ? AND "CreatedAt" >= now-86400)
```

- 复用已有索引 `Uploads(UserUID, CreatedAt)`（DATA-MODEL.md §10），
  两个 count 都是索引范围查询，无需新索引。
- **计数口径 = 受理的张数**（即 §1.3 第 ⑧ 步落库的 `Uploads` 行），**不是成功数**。
  理由：限流目的是限制**提交频率**；若只算成功，用户疯狂提交失败请求即可绕过。
- 因为 `Uploads` 行在受理时就以 `pending` 落库，所以计数天然包含 pending / failed。

### 4.4 超限行为

```
if !admin && enabled:
    hitHour = perHour 用量 + 本批张数 > perHour
    hitDay  = perDay  用量 + 本批张数 > perDay
    if hitHour || hitDay:
        if action == "reject":
            写 OperationLogs(upload, failed, Error="限流：每小时/每天上限")
            return 42901（消息注明命中的维度与阈值）
        else:  # action == "log"
            写 OperationLogs(upload, failed, Detail={RateLimited:true, Action:"log", Proceeded:true})
            继续放行
```

- 返回 `42901`（与队列满同码，靠消息区分，见 §1.7）。
- **`log` 模式也要写日志**——这是它的全部意义（观察真实用量，为开启 `reject` 做准备）。

---

## 5. 操作日志（D45 / D74）

### 5.1 定位：与任务日志的区别（**务必不要混用**）

| | `OperationLogs` | `Jobs` / `JobItems` / `JobLogs` |
|---|---|---|
| 是什么 | **审计级结果记录** | **任务执行过程** |
| 粒度 | 一次操作 **1 条** | 1 个批次 1 个 Job，含 N 个 item，可含上百行 `JobLogs` |
| 内容 | 谁、何时、对什么、成功/失败、失败原因 | 实时进度、逐行 stdout（npm 输出、上传日志） |
| 保留 | **180 天**（`log.retentionDays`） | 7 天（`log.jobRetentionDays`） |
| 展示页 | 「操作日志」 | 「任务」面板 |
| 典型问题 | 「昨天谁删了这张图？」 | 「这个插件装到哪一步了？」 |

**结论**：不要把上传进度写进 `OperationLogs`；也不要把审计结论塞进 `JobLogs`。

### 5.2 字段（以 DATA-MODEL.md §6.1 为准）

`UID` / `Type` / `Status` / `UserUID` / `Username` / `TargetType` / `TargetUID` /
`Detail`(JSON) / `Error` / `ClientIP` / `UserAgent` / `CreatedAt`。

> 写入统一走 helper：`internal/oplog` 的 `Record(ctx, Entry)`。
> **best-effort**：写日志失败**不得**导致业务失败，但要 `slog.Error` 记录。

### 5.3 类型清单（含触发点与建议的 `Detail`）

> **`Type` 的取值是字符串枚举，保持小写点号风格不变**（D81.3）。

| `Type` | 触发点 | `Status` | `TargetType` / `TargetUID` | `Detail` 建议内容 |
|---|---|---|---|---|
| `upload` | 上传批次结束（成功或失败各一条） | success/failed | `job` / JobUID | `{StorageUID, Total, Succeeded, Failed, Bytes}` |
| `image.delete` | 删除图片（单张） | success/failed | `upload` / UploadUID | `{DeleteRemote, RemoteDeleted, Reason, StorageUID, Size}` |
| `image.update` | 重命名 / 移动相册 | success/failed | `upload` / UploadUID | 改名 `{AliasName}`；移动 `{AlbumUIDFrom, AlbumUIDTo}` |
| `mail.send` | 每次发信（成功/失败各一条） | success/failed | `email` / EmailLogUID | `{to, template, subject}` |
| `user.create` | 管理员建号 / 邮件邀请建号 | success/failed | `user` / UserUID | `{Email, Role, CapacityBytes, Invite}` |
| `user.delete` | 账号注销 | success/failed | `user` / UserUID | `{Email, DeletedUploads, FreedBytes}` |
| `user.update` | 改配额 / 状态 / 密码 / 角色 | success/failed | `user` / UserUID | `{Fields, Before, After}`（**不含密码明文**） |
| `storage.create` | 新建存储配置 | success/failed | `storage` / StorageUID | `{Name, Type, PicgoConfigName}` |
| `storage.update` | 修改配置 / 凭据 / 启用状态 / 设为默认 / 连通性测试 | success/failed | `storage` / StorageUID | `{Fields, Before, After}`（**不含密钥**）；测试时 `{Action:"test", Ok, LatencyMs}` |
| `storage.delete` | 删除存储配置 | success/failed | `storage` / StorageUID | `{Name, Type, WasDefault}` |
| `plugin.install` | 安装插件 | success/failed | `plugin` / 包名 | `{Names, Version, Registry}` |
| `plugin.uninstall` | 卸载插件 | success/failed | `plugin` / 包名 | `{Names}` |
| `plugin.update` | 更新插件 | success/failed | `plugin` / 包名 | `{Names, From, To}` |
| `auth.login` | 登录成功 | success | `user` / UserUID | `{Method}` |
| `auth.failed` | 登录失败 | failed | `user` / 空或已存在 UserUID | `{Email, Reason}`（**不记密码**） |
| `auth.logout` | 登出 | success | `user` / UserUID | `{}` |
| `setting.update` | 修改系统设置 | success/failed | `setting` / 空 | `{Keys, Before, After}`（**密钥类键不记值，只记 `{Changed:true}`**） |
| `system.log.cleanup` | 日志清理任务自身（§5.5） | success/failed | `system` / 空 | `{DeletedOperationLogs, DeletedJobLogs, RetentionDays}` |
| `system.recount` | 配额对账发现偏差（§3.5） | success/failed | `system` / 空 | `{FixedUsers, FixedAlbums, MaxDelta}` |
| `theme.install` | 安装主题（zip 上传成功） | success/failed | `theme` / ThemeID | `{Name, Version, PackageBytes, Overwrite}` |
| `theme.uninstall` | 卸载主题 | success/failed | `theme` / ThemeID | `{Name, Version}` |
| `theme.activate` | 启用 / 切换主题 | success/failed | `theme` / ThemeID | `{Previous, Active, Pages}` |
| `theme.rescan` | 重新扫描主题目录 | success/failed | `theme` / 空 | `{Found, Valid, Invalid}` |
| `theme.settings.update` | 修改主题配置 | success/failed | `theme` / ThemeID | `{Keys, Count}`（**不记值内容**，可能含敏感串） |
| `theme.settings.clear` | 清理某主题的全部配置 | success/failed | `theme` / ThemeID | `{Keys, DeletedCount}` |
| `theme.error` | 主题损坏 / 回退内嵌 / `Pages` 非法 / 扫描失败 | failed | `theme` / ThemeID | `{Reason, Fallback, Path}` |

> 类型是**字符串列**，新增类型**不需要迁移**（D77）。上表即当前的权威清单，
> 与 DATA-MODEL.md §6.1 的表一致；扩展时**两处都要加**。

### 5.4 过滤与搜索

| 查询参数 | 匹配列 | 说明 |
|---|---|---|
| `Type` | `Type` | 精确匹配，支持多值（`Type=upload,image.delete`） |
| `Status` | `Status` | `success` / `failed` |
| `Keyword` | `Username` / `TargetUID` / `Detail` / `Error` / `UID` | **LIKE 模糊匹配**（关键词搜索） |
| `UserUID` | `UserUID` | 管理员按操作者过滤 |
| `From` / `To` | `CreatedAt` | 时间范围（Unix 秒） |
| `Page` / `PageSize` | — | 分页，默认 20，上限 100 |

- 排序：`CreatedAt DESC`（可切 `ASC`）。
- 索引依据：`OperationLogs(Type, CreatedAt)`、`(UserUID)`、`(CreatedAt)`、
  `(TargetType, TargetUID)`（DATA-MODEL.md §10）。
- `Detail` / `Error` 是 TEXT，模糊匹配在大表上会退化为扫描；
  **自用场景数据量小可接受**，若将来变慢再考虑全文索引（PgSQL）或倒排表（属于扩展项）。

### 5.5 保留与清理（D74）

**默认保留 180 天**（`log.retentionDays`，**`0` = 永久保留**）；
任务执行日志默认保留 7 天（`log.jobRetentionDays`）。

```
每日 03:00（本地时区）执行：
  1. retention = log.retentionDays
     - retention == 0 → 跳过（OperationLogs 永久保留）
     - retention <  0 → 视为非法值，按 0 处理并写 WARN
  2. 删除 OperationLogs WHERE CreatedAt < now - retention*86400
     注意：一次清理只删「严格超期」的行，且分批（每批 1000 条）
  3. 删除 JobLogs       WHERE CreatedAt < now - log.jobRetentionDays*86400
  4. 顺带清理已终态且超期的 Jobs / JobItems（保留与 JobLogs 同期）
  5. 顺带执行配额对账（§3.5）；有偏差才写日志（正常时静默）
  6. 写一条 OperationLogs：Type = system.log.cleanup，Detail = 删除条数统计
```

- **清理动作本身也写日志**（D74），这样「某天的日志为什么少了」有据可查。
- 配置变更立即生效（`onChanged` 重设定时器）。
- SQLite 注意：大批删除建议分批（每批 1000 条）执行，避免长事务把写锁占住（§12）。

---

## 6. 邮件（D29）

### 6.1 用途

| 模板 | 触发 | 说明 |
|---|---|---|
| `invite` | 管理员新建用户时勾选「发送邀请邮件」 | **没有自助注册**（D25），所以这是「拉人进来」的主要方式。邮件含一次性设置密码链接 |
| `reset_password` | 用户点「忘记密码」 | 含一次性重置链接 |
| `test` | 管理员在后台点「发送测试邮件」 | 仅验证 SMTP 配置是否可用 |

> 邮件功能有总开关 `mail.enabled`（默认 `false`）。**未配置 SMTP 时不得报错崩溃**：
> 前台提示「邮件服务未启用」，管理员用「重置密码」外的路径（后台直接设密码）兜底。

### 6.2 SMTP 配置键（DATA-MODEL.md §7.4「邮件」）

`mail.enabled` / `mail.host` / `mail.port`（默认 465）/ `mail.encryption`（`ssl`｜`starttls`｜`none`）/
`mail.username` / `mail.password`（**secret，AES-256-GCM 加密存储**，D19）/
`mail.fromAddress` / `mail.fromName`。

- 读取 `mail.password` 时经 `internal/crypto` 解密；**接口返回永远是掩码 + `HasValue`**。
- 加密方式与 `StorageSecrets` 一致（`KeyVersion` 支持主密钥轮换）。

### 6.3 发送与记录（硬性要求）

**每次发信都必须留下两条记录：**

```
发送流程：
  1. 发信前先落 EmailLogs 一行
  2. 同步发送（超时 10s）
  3. 回填 EmailLogs.Status = success|failed，failed 时写 Error
  4. 写 OperationLogs：Type = mail.send，TargetUID = EmailLogs.UID
```

| 表 | 列（DATA-MODEL 真源） | 说明 |
|---|---|---|
| `EmailLogs` | `UID` `ToAddress` `Subject` `Template` `Status` `Error` `RelatedUserUID` `CreatedAt` | **不存邮件正文**（用户明确要求） |
| `OperationLogs` | 见 §5.3 `mail.send` 行 | 供统一审计检索 |

> **不存正文**的含义：不存 HTML/template 渲染结果、不存正文快照。
> `Template`（模板名）+ `Subject`（主题）+ 收件人足以回答「发了什么邮件」。

### 6.4 失败处理与重试策略

| 失败类型 | 判定 | 处理 |
|---|---|---|
| 配置类（认证失败、主机不可达、端口错） | 4xx / 连接拒绝 / DNS 失败 | **不重试**，直接 `failed`；后台提示「SMTP 配置可能有误」 |
| 瞬时类（5xx、网络超时、连接重置） | 5xx / timeout | **立即重试 1 次**（间隔 2s）；仍失败则 `failed` |
| 收件人无效 | 5xx 中含收件人拒绝 | 不重试，`Error` 写明确原因 |

- **不引入邮件队列**（自用场景，量极小）。同步发送 + 10s 超时即可；
  超时时间必须 < 前端请求超时，避免用户看到假死。
- 「找回密码」接口对**不存在**的邮箱返回**成功**（防账号枚举），
  但仍写 `auth.*` / `mail.send` 日志（`Detail.NotFound = true`）。
- **一次性令牌（邀请 / 重置）的存放**：⬜ **仍未决**（见文末偏差 §7）。
  最小实现是把令牌哈希与过期时间写入 `UserSettings`（键 `auth.inviteToken` /
  `auth.resetToken`，JSON 值）。
  > 若后续要实现为独立表，按 D78 新建表而不是塞进 `Users`。

---

## 7. 存储驱动同步（D22 / D64 / D65）

### 7.1 真相源与投影

```
   DB（真相源）                        agent / picgo-core（投影）
   StorageConfigs  ──────────────────►  config.json
     UID（= agent 返回的 _id）              picBed.<type>.*
     Name  ──映射──► _configName      uploader.<type>.configList[]
     Type                                   picBed.uploader / picBed.current
     Enabled / IsDefault                    picgoPlugins.*
     Capabilities（仅缓存，不写 agent）
   StorageSecrets  ──────────────────►   解密后仅存于内存
     EncryptedPayload                    （除 config.json 的 uploader 配置外不落盘）

   PathTemplate / FileTemplate  ─────► 不上投影：每次上传时随请求下发
                                          （POST /api/upload 的 PathTemplate/FileTemplate）
```

**两个容易搞错的点**

1. **`UID` 不是 Go 生成的**：新建配置时先调 agent 的 `POST /api/uploaders/configs`，
   由 picgo 的 `uploaderConfig.createOrUpdate` 生成 `_id`，**Go 把该 `_id` 作为
   `StorageConfigs.UID` 落库**（D65）。因此「先落 DB 再推 agent」的顺序会把 UID 搞乱，
   正确顺序是**先创造 agent 侧配置、拿到 `_id`、再写 DB**。
2. **魔法路径/文件名模板不写进 picgo config**：它们是**按次上传参数**
   （`POST /api/upload` 的 `PathTemplate` / `FileTemplate`，见 `API.md` §13.5），
   因为它们是「这张图存哪里」而不是「这个驱动怎么配」。放在配置里会导致
   改模板要同步整个 agent，且并发下会互串。

**键级合并（D22）—— 绝不可整体重建 `config.json`：**

```
✅ 只覆盖我们管的键：picBed.* / uploader.* / picgoPlugins.* / settings.*
❌ 不得删除或重写其他键（插件私有键）
```

**为什么**：插件会往 picgo config 里写自己的状态。已核实的例子：
`picgo-plugin-github-plus` 会写 `uploaded: [...]`（它自己的图片账本）与
`[PluginName].lastSync`。若整体重建 `config.json`，这些状态会被抹掉，
导致插件行为异常（例如它的「同步删除」功能失效）。

**只允许用 agent 的 PATCH 语义**（点路径合并），配合白名单式的键前缀校验：
Go 侧构造 patch 时只允许出现上述前缀；遇到其他前缀直接拒绝并 `WARN`
（防止将来代码写错把插件键覆盖掉）。

> 注意：这里的 `picBed` / `uploader` / `picgoPlugins` 是 **picgo 的键名**，
> 不属于 D81 管辖范围，**必须原样保留**（D81.3 第 5 条）。

### 7.2 操作 → agent 端点映射

| Go 侧操作 | agent 端点（以 `API.md` §13 契约为准） | 说明 |
|---|---|---|
| 新建存储配置 | `POST /api/uploaders/configs` `{Type, ConfigName, Config, Activate:false}` | `Config` 由 Go 解密后拼装（仅在内存中短暂出现）；**用返回的 `_id` 作为 `StorageConfigs.UID` 落库**（D65） |
| 修改配置元数据/凭据 | 同上（`createOrUpdate` 语义，按 `ConfigName` 命中则更新） | 幂等；`ConfigName` 变更会**新建**一条而非改名，见下方注意 |
| 枚举 agent 侧已有配置 | `GET /api/uploaders/configs?type=<type>` | 用于 reconcile 的**差异检测**；返回未脱敏，**绝不可原样回传前端** |
| 删除配置 | `DELETE /api/uploaders/configs?type=&configName=` | 删前先确认不是唯一可用配置 |
| 设为默认 | `POST /api/uploader/use` `{Type, ConfigName}` | 同时更新 `StorageConfigs.IsDefault`（同事务内先清后置） |
| 探测驱动能力 | `GET /api/uploaders` | 返回各驱动 `Config` 字段 schema 与 `Capabilities`，Go 侧缓存进 `StorageConfigs.Capabilities`（§7.4） |
| 连通性测试 | Go 侧 `POST /api/web/v1/storage/configs/{uid}/test` → agent `POST /api/uploaders/test` | 返回 `{Ok, Message, LatencyMs}`（`API.md` §13.3 已定义该原语）。测试结果写 `OperationLogs`（`Type=storage.update`，`Detail.action="test"`） |
| 读取 picgo 配置（排障） | `GET /api/config` | **仅 admin**，且返回时脱敏 |
| 切换 transformer | `PATCH /api/config` `{Patch:{"picBed.transformer": "path"}}` | 由 `picgo.transformer` 设置变更触发 |
| 远端删除 | `POST /api/delete` `{UploaderType, Items:[raw]}` | 见 §9；**HTTP 200 + `Supported`/`RemoteDeleted` 判定**，不看状态码 |
| 单文件上传 | `POST /api/upload` | 见 §1；**失败时 HTTP 200 + `Code="ERR_PICGO"`**，不看状态码 |

> ⚠️ **「改名」不是一个原子操作**：`createOrUpdate` 按 `ConfigName` 命中则更新、
> 未命中则**新建**。所以若允许改 `PicgoConfigName`，会多出一条配置（旧名残留），
> 且 `UID` 随之改变，需同步迁移 `Uploads.StorageUID` 等引用。
>
> **裁决：`StorageConfigs.PicgoConfigName` 创建后只读**（见文末偏差 §10）。
> `Name` 仅作展示，可自由改，不触碰 agent。

### 7.3 启动 reconcile（幂等，可在 agent 重启后重复执行）

```
reconcile():
  1. 取 DB：StorageConfigs WHERE Enabled = true ORDER BY UpdatedAt ASC
  2. 对每一条：
     a. 解密 StorageSecrets.EncryptedPayload
     b. 若该 UID 在 agent 侧不存在
        （GET /api/uploaders/configs?type=<type> 的 configs[]._id 里找不到）
        → 说明 agent 的 config.json 被重建/被抹过，重新 POST 创建（用同一 ConfigName）
        → 拿到新 _id，**若与 DB 的 UID 不一致，需回写 DB 并迁移引用**（罕见路径，需告警）
     c. 若已存在 → POST /api/uploaders/configs 幂等更新（createOrUpdate 语义）
     d. ❗ 不要在这里同步 PathTemplate / FileTemplate：它们是按次上传参数（§7.1）
  3. 取 IsDefault = true 的那条 → POST /api/uploader/use {Type, ConfigName}
     - 若 DB 中无 IsDefault，则退化取 UpdatedAt 最新的一条 Enabled 配置
     - 若一条 Enabled 配置都没有 → 不调用 use，记 WARN（上传将返回 40001）
  4. 清理 agent 侧「DB 已不存在」的配置：
     GET /api/uploaders/configs?type=<每个 DB 出现过的 type>
     若有 DB 里查不到的 ConfigName → DELETE
     （注意：只清理「DB 里出现过的 type」，不要遍历所有类型，
       避免误删用户通过 CLI 手工添加的配置——那属于「DB 未纳管」，保留并标记）
  5. 更新每条配置的 Capabilities（§7.4）
  6. 把每条配置的 SyncPending（Metadata 内）清除
```

**触发时机**：① 进程启动；② agent 重生/恢复后；③ 每次配置增删改激活之后（增量为准，
但实现上直接跑完整 reconcile 更简单，因为幂等且配置量极小）。

**元数据与凭据分离（D78）**

- `StorageConfigs`：只有元数据（UID/Name/Type/PicgoConfigName/Enabled/IsDefault/模板/能力）。
  **任何列表/详情接口都不联合 `StorageSecrets`**。
- `StorageSecrets`：只有密文（`EncryptedPayload` + `KeyVersion`）。
- 更新凭据走**独立入口**（`PUT /api/web/v1/storage/configs/{uid}/secrets`），
  避免「改个显示名却要求重传 token」。
- 审计日志里**永远不写密钥值**，只写「凭据已更新」布尔标记（§5.3 备注）。

### 7.4 Capabilities 探测与缓存

| 时机 | 行为 |
|---|---|
| 新建配置成功后 | 探测并写入 `StorageConfigs.Capabilities` |
| `GET /api/web/v1/storage/drivers`（前端打开存储页） | 只读缓存，不触发探测 |
| 手动「刷新能力」按钮 | 强制重新探测 |
| 每次 reconcile 第 5 步 | 顺带刷新（agent 版本或插件变化后能力可能变） |
| 探测失败 | `Capabilities` 保持上一次的值；若从未成功则为空 → 前端显示「能力未知」，**不阻塞上传** |

`Capabilities` 结构见 DATA-MODEL.md §3.1（**JSON 内的键用 PascalCase**）。
关键两个标志的用途：

| 标志 | 用途 | 缺失时的降级 |
|---|---|---|
| `SupportsPathTemplate` | 决定魔法路径是「真路径」还是「文件名前缀」（D44） | 视为 `false` → 路径降级为文件名前缀，并在 UI 提示 |
| `SupportsRemoteDelete` | 决定删除时是否调 agent 删除（D47） | 视为 `false` → 只删本地记录，UI 标记「该驱动不支持远端删除」 |

**能力探测必须基于运行时读取，禁止在代码里 `switch` 驱动类型名**（D77.3）。

---

## 8. 主题系统（D94 / D95 / D96 / D97 / D98 / D99）

> **一句话**：前端**主体内置**（`go:embed web/dist`）；「主题」是一组可替换的前端代码，
> **只为它在 `manifest.Pages` 里注册的路径提供渲染**；未注册的路径一律走内置 SPA。**默认主题只注册首页 `/`**。

### 8.1 职责边界（**读这一节就能理解全貌**）

| 项 | 内容 |
|---|---|
| **前端主体** | **内置 SPA**：`go:embed web/dist` 编进二进制。图库、上传、相册、任务、日志、设置、**登录页**、**后台**全部由它渲染 |
| **「主题」** | 一组可替换的**前端代码**（`index.html` + `assets/`），作者可用任意技术栈 |
| **注册机制** | 默认主题只注册 `["/"]` → **只有首页 `/` 走主题**；其它业务页面（`/upload`、`/gallery`…）**可被任何主题注册**，无需改 Go |
| **扩展预留** | 接管范围写在 `manifest.Pages` 里，**Go 的分发逻辑一次性写通用**。将来某主题声明 `["/", "/gallery"]`，`/gallery` 自动改走主题——**Go 零改动**（D77 原则） |

**永久保留路径（主题无法接管，manifest 声明了也会在校验时被拒）**

| 类别 | 路径 | 原因 |
|---|---|---|
| **认证页** | `/login`、`/first-login`、`/forgot-password`、`/reset-password`、`/logout` | 主题是第三方代码。若允许接管登录页，它能伪造登录框把密码 POST 到自己的服务器——**受害的是没选过主题的普通用户**，而装主题的是管理员 |
| **后台** | `/admin/**` | 同上：管理界面必须可信 |
| 系统路径 | `/api/**`、`/healthz`、`/theme-assets/**`、`/assets/**`、`/themes/**`、`/favicon.ico` | 非页面路由 |

> 保留列表写死在**代码里**，**不是配置项**。这是安全默认值，不是「暂不支持」。

### 8.2 磁盘布局与真相源

```
<dataDir>/themes/<ThemeID>/
├── manifest.json       # 元数据 + 配置 schema + 接管范围 Pages（D98）
├── index.html           # 入口
├── assets/              # 主题自己的 js / css / 图片（带内容哈希）
└── screenshot.png       # 可选，后台主题列表预览图
```

| 项 | 规则 |
|---|---|
| **数据真相源** | **`<dataDir>/themes/`**（即 `$PICGO_WEB_DATA_DIR/themes`，默认 `./data/themes`）——运行时只读这一处 |
| **内嵌兜底** | 二进制内嵌一份默认主题（`go:embed` 压缩归档）。主题缺失 / 损坏 / `Pages` 非法 → **直接用内嵌那份服务**，首页**永不白屏** |
| **不建表** | 主题列表**扫描文件系统**读 `manifest.json` 得到，**没有 Themes 表**（通常只有 1~3 个主题，扫描成本可忽略） |
| 当前主题 | `SystemSettings` 的 `theme.active`（默认 `default`） |
| 主题配置 | **独立表 `ThemeConfigs`**（§8.8），**不在 `SystemSettings`** |

### 8.3 启动 seed（**升级不覆盖用户主题**）

```
启动时：
  1. 确保 <dataDir>/themes/ 存在（缺则 MkdirAll）
  2. 若该目录「为空」（没有任何子目录）
       → 把内嵌的默认主题解压一份到 <dataDir>/themes/default/
       → INFO 日志 "seeded embedded default theme"
     若非空 → **什么都不做**（绝不覆盖用户放进去的主题）
  3. 继续 §8.4 的扫描
```

**运维含义**：升级只需重跑容器 / 替换二进制；用户自己的主题留在 `data/` 卷里**不受影响**。

### 8.4 扫描与校验

```
扫描():
  1. 遍历 <dataDir>/themes/*/（只处理目录，忽略普通文件与 . 开头项）
  2. 读该目录的 manifest.json（大小 ≤ theme.maxManifestBytes，默认 1 MiB）
  3. 运行 validateThemeManifest（下表 7 项）
  4. 合法 → 进列表
     不合法 → **不进列表**，写 OperationLogs（Type = theme.error，Detail 含 Reason 与 Path）
```

| # | 校验项 | 失败后果 |
|---|---|---|
| 1 | `ID` 非空、匹配 `^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`、**与目录名一致** | 主题不合法 |
| 2 | `Name` 至少一种语言非空（支持多语言对象 `{"zh-CN": "...", "en": "..."}`） | 主题不合法 |
| 3 | `Configuration.Type` ∈ {空, `managed`}（`raw` / `redirect` **不实现**） | 主题不合法 |
| 4 | `Items[].Key` 非空、唯一、匹配 `^[A-Za-z][A-Za-z0-9_]{0,63}$` | 主题不合法 |
| 5 | `index.html` 存在 | 主题不合法 |
| 6 | `manifest.json` ≤ `theme.maxManifestBytes` | 主题不合法 |
| 7 | **`Pages` 合法**（见下表） | 主题不合法 |

**`Pages` 的语义（最易错的一处，务必看清）**

| 写法 | 含义 |
|---|---|
| 不写 `Pages` | 等价于 `["/"]` |
| `["/"]` | **精确匹配 `/` 这一个路径**（**不是**「接管全部」） |
| `["/", "/gallery"]` | `/` 与 `/gallery` 前缀下的路径都走主题 |
| `["/*"]` | 通配：接管**所有非保留业务页面**（等于整站主题化；语法允许，默认主题不这么做） |
| 匹配方式 | **路径前缀匹配，最长匹配优先**（`/gallery` 比 `/` 更具体，先命中 `/gallery`） |
| 合法性 | 每项须以 `/` 开头；不含 `..`；只允许 `/*` 这一种通配；**不得命中保留路径或认证页保留列表**（§8.1） |

### 8.5 请求分发（运行时，D99.1）

```
收到请求 path（以下流程只对「非保留路径」执行）
  1. 是保留路径？            → 交给对应处理器（API / healthz / theme-assets / assets / themes / favicon）
  2. 是认证页保留列表？      → 内置 SPA 的 index.html（**主题无法接管**）
  3. 命中当前主题的 Pages？  （最长前缀匹配）
       ├─ 是 → 主题的 index.html
       └─ 否 ↓
  4. /admin/** ?             → 内置 SPA 的 index.html（**主题无法接管**）
  5. 其他                    → 内置 SPA 的 index.html（SPA 回退）
```

> **只有第 3 步与主题相关**，且只依赖 `manifest.Pages` + `theme.active`。
> 首次实现的默认主题 `Pages = ["/"]` → 实际效果：**只有 `/` 走主题**。

### 8.6 资源托管与缓存

| 路径 | 归属 | 缓存头 |
|---|---|---|
| `GET /`（被主题接管时） | **当前主题**的 `index.html` | `Cache-Control: no-cache` |
| **`GET /theme-assets/**`** | **当前主题**的 `assets/` | `public, max-age=31536000, immutable` |
| **`GET /assets/**`** | **内置 SPA** 的 `assets/`（embed） | `public, max-age=31536000, immutable` |
| `GET /favicon.ico` | 优先当前主题的 `assets/favicon.ico`，缺失回退内置 | `max-age=86400` |
| `GET /themes/**` | **404**（不暴露主题目录 / `manifest.json` / 源码） | — |
| 其他（SPA 回退） | **内置 SPA** 的 `index.html` | `Cache-Control: no-cache` |
| `/api/**`、`/healthz` 未匹配 | `40401` JSON（**不参与** SPA 回退） | — |

**三条硬约束**（违反会出难查的怪问题）

1. 主题引用自己的资源**必须**走 `/theme-assets/...`（构建时配 `base: '/theme-assets/'`）。
2. `/assets/**` **永远**指向内置 SPA —— 主题**不得**占用，否则会覆盖登录页与后台的资源。
3. `/theme-assets/**` 路径规范化后必须落在**当前主题的 `assets/`** 内，否则 `40401`（防 `..` 穿越）；不提供目录列表。

> ⚠️ **主题缺文件时不回退内嵌默认主题的同名文件**：
> 主题**有效** → 缺文件就返回 `404`（便于排查，也避免「破主题被喂了别的主题的代码」）；
> 主题**无效** → 才整体回退内嵌默认主题。

### 8.7 兜底与降级（**永不白屏**）

| 情形 | 行为 |
|---|---|
| `theme.active` 指向的主题目录不存在 | 用**内嵌默认主题**服务（其 `Pages = ["/"]`）；写 `theme.error` |
| 主题目录存在但 manifest 不合法（§8.4 任一项） | 同上 |
| 主题的 `Pages` 非法 | 同上 |
| 主题合法、但缺某个 `assets/` 文件 | 该请求 `404`（**不**整体回退，见 §8.6 注意） |
| **内嵌默认主题本身** | 编译期保证存在，是兜底锚点，**不可卸载** |

**最强的兜底能力**：因为兜底来自二进制本身，即使**删掉整个 `data/themes/`** 也不会白屏——
首页会以内嵌默认主题渲染，而 `/login` 与 `/admin/**` 是内置 SPA，**始终可用**，
管理员可进后台「主题」页重新 seed / 上传 / 切换。

### 8.8 主题配置（`ThemeConfigs` 表）

主题的配置项由**主题自己在 manifest 里声明**（`Configuration.Items`），**Go 侧不硬编码键名**。

| 项 | 规则 |
|---|---|
| 存储 | **独立表 `ThemeConfigs`**（每行一键；见 DATA-MODEL.md §3.3），**不在 `SystemSettings`** |
| 键集合 | 来自该主题 manifest 的 `Configuration.Items` |
| **读取顺序** | **DB 值 → manifest 的 `Default` → 类型零值**（三级兜底，与 D18 一致） |
| 写入 | 只接受**该主题声明过的键**；类型按 `Type` 校验；未声明的键 → `40001`（防脏写） |
| `Source` | API 返回 `db`（有 DB 行）或 `default`（用 manifest 默认值），前端据此显示来源徽章 |
| **换主题** | 旧主题的值**保留**（`ThemeID` 不同），切回来仍生效 |
| **卸载主题** | **不自动删值**；另有显式清理（`DELETE /api/web/v1/themes/{ThemeID}/settings`） |
| 并发 | 每键独立行 → 两个管理员改**不同键**互不覆盖 |

**`theme.active` 是唯一例外**：它是「当前用哪个主题」的站点级选择，存 `SystemSettings`
（不属于任何主题的 schema，也就不随主题装卸而变）。

**哪些主题信息不进库**：`ID` / `Name` / `Description` / `Author` / `Version` / `URL` / `Preview` /
`Pages` / `MinAppVersion` / `Configuration.*` 全部**以 manifest 文件为真相源**，**不落库**。

### 8.9 主题安装（zip 上传）的 9 条安全校验（D96）

与 `API.md` §10 的 `POST /api/web/v1/themes/install` 一一对应：

| # | 校验 |
|---|---|
| 1 | 仅 `.zip`；压缩包 ≤ `theme.maxPackageBytes`（默认 64 MiB） |
| 2 | **防 Zip Slip**：逐个 entry 用 **`filepath.Rel`** 判定规范化后的目标必须落在目标目录内，拒绝 `..` 与绝对路径 |
| 3 | **拒绝符号链接 entry**（`Mode()&os.ModeSymlink != 0`） |
| 4 | 单文件 ≤ `theme.maxFileBytes`（128 MiB）、总计 ≤ `theme.maxExtractBytes`（512 MiB）、文件数 ≤ `theme.maxFiles`（10000） |
| 5 | **强制权限位**：目录 `0755`、文件 `0644`，**忽略 zip 里声明的 mode**（防 setuid / 可执行位） |
| 6 | manifest 七项校验（§8.4），含 `Pages` |
| 7 | 目标目录已存在且 `Overwrite=false` → `40901` |
| 8 | **原子性**：先解压到 `<dataDir>/themes/.tmp-<随机>/`，全部校验通过后 `rename`；失败则删临时目录 |
| 9 | 写 `OperationLogs`（`theme.install`，`Detail` 含 ID / 版本 / 包大小） |

**比 Komari 更严的两处**（其源码用 `strings.HasPrefix` 判路径、用 zip 声明的 mode 落权限）：
本实现改用 **`filepath.Rel`**（前缀比较在 base 本身、盘符、UNC、Windows 大小写等边界会误判），
并**强制权限位 + 拒绝 symlink entry**。

**两条不可卸载规则**

| 情形 | 结果 |
|---|---|
| 卸载**当前启用**的主题 | `40901`（先切到别的主题） |
| 卸载 `default` | `40901`（它是兜底锚点） |

### 8.10 切换与生效时机

| 动作 | 端点 | 生效时机 |
|---|---|---|
| 启用 / 切换 | `PUT /api/web/v1/themes/active` | **立即生效**（无需重启）；**已打开的页面需刷新** |
| 重新扫描 | `POST /api/web/v1/themes/rescan` | 立即（列表刷新） |
| 修改主题配置 | `PUT /api/web/v1/themes/{ThemeID}/settings` | 立即；主题页面下次加载读到新值 |
| 卸载 | `DELETE /api/web/v1/themes/{ThemeID}` | 立即 |

> 缓存策略使然：主题的 `index.html` 是 `no-cache`，所以**刷新即可看到新主题**，不需要清浏览器缓存；
> 而 `assets/` 是 `immutable`，靠内容哈希区分，换主题后哈希不同，不会读到旧资源。

### 8.11 运维要点

| 事项 | 说明 |
|---|---|
| **备份** | 备份 `data/` 目录即包含全部主题（D82 不做导出/导入） |
| **新增 / 替换主题** | 把目录放进 `data/themes/` → 后台点「重新扫描」→ 启用。**无需重启、无需改配置、无需重编译** |
| **修复损坏主题** | 删掉坏目录 + 重新扫描；期间首页以内嵌默认主题渲染，可从容修复 |
| **变更审计** | 全部主题操作写 `OperationLogs`（类型见 §5.3） |
| **安全告知** | **主题 = 服务器上的任意前端代码**（影响它接管的页面）。只安装可信主题；仅 admin 可操作 |
| **升级** | 只重跑容器/换二进制；`data/themes/` 里的用户主题**不会被覆盖**（§8.3） |

---

## 9. 删除流程（D46 / D47）

### 9.1 单张删除的完整步骤

```
DELETE /api/web/v1/uploads/{uid}?DeleteRemote=true|false

① 鉴权与权限
   - 本人 → 放行
   - admin → 放行
   - 其他 → 40301
② 读取 Uploads 行（含 Size / StorageUID / UserUID / AlbumUID）
   并读取 UploadResults.RawOutput + FilePath
   ⚠️ 必须在删除两张表之前读完 —— 删完就拿不到了（D47 的硬性前提）
③ 远端删除（仅当 DeleteRemote=true）
   - 查 StorageConfigs.Capabilities.SupportsRemoteDelete
     · false → 跳过，不调 agent（Supported=false, RemoteDeleted=false）
   - agent up → POST /api/delete {UploaderType, Items:[RawOutput]}
     · 响应：{Supported, RemoteDeleted, Message}（HTTP 200）
       - Supported=false              → 该驱动不支持，Reason="该驱动不支持远端删除"
       - true + RemoteDeleted=false   → 插件实现了但失败，Reason=Message
       - true + RemoteDeleted=true    → 远端已删
     · agent 侧实现：构造 guiApi shim → picgo.emit('remove', items, shim)
       → 从 shim 捕获的 showNotification 文案反推成功/失败（emit 无返回值、插件 async 无人 await）
   - agent down → 503/50002；前端可选择「仅删本地记录」重试
   - 远端失败 → RemoteDeleted=false, Reason=<原始错误>
④ 删本地行（单事务）
   - DELETE UploadResults WHERE UploadUID = ?
   - DELETE Uploads         WHERE UID = ?
   - Users.UsedBytes     -= Size（不为负，见 §3.3）
   - Albums.ImageCount   -= 1（若 AlbumUID 非空）
⑤ 若 UploadResults.FilePath 存在且 upload.keepLocalCopy=false → 删除本地暂存文件
   （删除失败只记 WARN，不影响结果）
⑥ 写 OperationLogs：Type = image.delete
   Detail = {DeleteRemote, RemoteDeleted, Reason, StorageUID, Size, AlbumUID}
```

### 9.2 关键取舍

| 取舍 | 决定 | 理由 |
|---|---|---|
| 远端失败是否回滚本地删除 | **不回滚**（降级为「仅删本地」） | 用户意图是「不在我的图库里看到它」；远端残留只是图床上的脏数据，可人工清理。回滚会让用户反复删不掉 |
| 是否先删远端再删本地 | **先远端，后本地** | 顺序反了的话，远端删除需要的 `RawOutput` 已经没了 |
| 配额是否退 | **退**（D72） | 与远端是否成功无关 |
| 是否软删除 | **不做**（D46） | 无回收站，硬删除 |
| 远端删除是否看 `Supported` | **先查能力缓存，再依响应** | `Capabilities.SupportsRemoteDelete=false` 时**不调 agent**（省一次无意义的往返）；即使为 `true`，仍以 agent 返回的 `Supported` 为准（可能因插件变更而不一致） |

### 9.3 批量删除

- `POST /api/web/v1/uploads/batch-delete`：body 含 `Uids[]` 与 `DeleteRemote`。
- **逐条走 §9.1 的同一实现**（同一个 service 方法），一条失败不影响其他条。
- 响应返回逐条结果：`{Uid, Ok, RemoteDeleted, Reason}`；
  整体 HTTP 为 200，**逐条结果用 `Data` 表达**（不要因为部分失败就返回 5xx）。
- 写日志策略：**每张一条 `image.delete`**（便于按图追溯），
  不额外写汇总条目——避免同一次操作在日志里出现两类记录。

### 9.4 账号注销（D34）

```
DELETE /api/web/v1/users/{uid}（admin）

① 校验：不能删自己；不能删最后一个 admin；目标不存在 → 40401
② 遍历该用户全部 Uploads（分页，每批 100）
   对每一张走 §9.1 的删除流程（含配额退还、单条 image.delete 日志）
   → 这就是「必须走同一条流程」的含义：不允许写一条 DELETE FROM "Uploads"
     否则配额与相册计数会漂移，日志也无从追溯
③ 删除用户侧数据（顺序无关，建议如下）：
   UserSettings → APITokens → RefreshTokens → OAuthIdentities
   → UserProfiles → LoginAttempts（按 Email 清理）
④ 删除 Users 行
⑤ 写 OperationLogs：Type = user.delete，
   Detail = {Email, DeletedUploads, FreedBytes}
```

- 大批量注销应**异步化**（返回 `JobUID`，复用 `Jobs` 表，`Kind = "user.purge"`），
  否则请求会超时。任务面板可见进度。
- 注销**不删**该用户邮箱相关的 `EmailLogs`（历史发信记录保留，`RelatedUserUID`
  变成悬空引用 —— 允许，因为不建外键约束，D77）。

---

## 10. PicGo-Core 补丁对运行的影响（D48–D51）

> 完整补丁说明见 [`PICGO-INTEGRATION.md`](./PICGO-INTEGRATION.md)。
> 本节只讲**它如何影响运行机制**。

### 10.1 当前状态与**依赖来源**

- fork 仓库 `Github-me:YeqingKy/PicGo-Core`，分支 **`PicGo-Web`**，基线上游 `dev` @ v3.0.2。
- 补丁**已落地**（提交 `6419c2f`），含 4 项改动：
  P1 `UploadOptions.uploader`（按次指定图床）、P2 per-context 配置覆盖、
  P3 `Lifecycle.step` 实例字段 → 局部变量（修并发串扰）、P4 `UploadOptions.contextData`。
- **自维护，不提交上游**（D48）；上游更新时以 `dev` 为基线 rebase（D51）。
- **本项目从 npm 安装它**：`@yeqingky/picgo-core`（fork 的版本线独立，从 `1.0.0` 起）。
  不再使用本地 `file:../../PicGo-Core` 依赖，也不需要 vendor tarball。

**补丁状态在运行时也被探测**（因为这直接决定并发是否安全）：

| 层 | 位置 | 失败时的行为 |
|---|---|---|
| 构建期 | `deploy/docker/Dockerfile` | 断言 dist 含 `contextData` → **构建失败** |
| 运行时 | `picgo-agent/src/picgo/patch.ts` → `/healthz.Patches` | 缺失时 `agent.StatusHolder.PatchesComplete() = false` |

`UploadService.concurrency()` 会读它：**补丁不全就把并发强制降到 1**，
并写一条 WARN + 系统通知（`system.notice`）。

```
agent 未就绪          → 保守 false → 并发 1
agent 就绪但补丁缺失   → false      → 并发 1 + WARN + 通知
agent 就绪且补丁齐备   → true       → 并发 = upload.concurrency
agent 中途掉线        → 保守 false → RefreshConcurrency() 降回 1
```

`probeAgentLoop` 在 agent 状态**变化时**回调 `UploadService.RefreshConcurrency()`，
所以启动时因 agent 未就绪而保守取的 1，会在 agent 就绪后自动升到配置值
（**不需要重启**）。

单测：`internal/service/upload_w5_test.go` 的
`TestConcurrencyForcedToOneWithoutPatch`（降级 + 恢复）、`TestConcurrencyClamped`（上下限）。

### 10.2 两条运行路径

| 条件 | agent 的做法 | 是否依赖补丁 |
|---|---|---|
| **`upload.concurrency = 1`（默认）** | 上传前用 `picgo.setConfig()` 写入 `picBed.uploader` / `picBed.current` / `picBed.<type>`（**只改内存、不落盘**），再 `picgo.upload([path])` | ❌ **不依赖** |
| **`upload.concurrency > 1`** | `picgo.upload([path], { uploader: { Type, ConfigName }, contextData: { JobUID, Seq } })` —— 每个上传自带独立覆盖表，互不干扰 | ✅ **依赖 P1+P2** |

- **`setConfig` 不落盘** 是 `concurrency = 1` 路径的安全前提：`config.json` 不会被上传动作改动，
  遵守 D22 的键级合并原则（且 `setConfig` 表达的是「本次运行时的内存态」，
  与 D8 的描述一致）。
- 两条路径**共用同一套业务代码**：Go 侧只改 `upload.concurrency` 配置值，
  不需要任何代码分支（`ARCHITECTURE.md` 的 agent 实现在内部按并发度选择调用形式）。

### 10.3 `contextData` 的用途：事件归属

picgo 的生命周期事件（`uploadProgress` / `finished` / `failed` / `beforeUpload` …）
**挂在根 `EventEmitter` 上**，与具体上传无关。因此：

```
concurrency = 1 → 串行，同时只有一个上传在跑，事件天然无歧义
concurrency > 1 → 事件会混在一起，无法判断「这次 finished 属于哪个文件」
```

补丁 P4 让每次 `upload()` 可以携带 `contextData`，并在生命周期回调里通过
`ctx.contextData` 取回。agent 的做法：

```ts
// 发起上传时带上归属信息
await picgo.upload([path], {
  uploader: { type, configName },
  contextData: { JobUID, Seq }
})
// 事件回调里取回，用于归属到具体 Job/Item
picgo.on('finished', ctx => { const { JobUID, Seq } = ctx.contextData ?? {}; ... })
```

| 影响 | 说明 |
|---|---|
| **进度事件** | 仍**无法**细分到文件：`uploadProgress` 只有 5 档且不带 `contextData`。并发时按 `done / total` 计算（§1.6 已说明） |
| **成功/失败事件** | 可归属到具体 Job/Item，用于日志与状态更新 |
| **Go 侧的判定** | 主判据仍是 `POST /api/upload` 的**同步返回值**（§1.5）；事件只作为补充信息与日志来源，不作为状态机依据 |

### 10.4 补丁缺失时的降级（必须实现）

agent 启动时**探测**一次补丁是否存在（例如检查 `upload.length >= 2` 或试探性传入
`uploader` 选项并捕获「未知选项被忽略」的行为），结果决定运行策略：

```
agent 启动 → detectUploadTargetSupport()
  ├── 支持（补丁存在）→ 允许 concurrency > 1；按 §10.2 走两条路径
  └── 不支持（补丁缺失，例如误用了 npm 上的原版 picgo）
        ├── 强制把有效并发降为 1（覆盖 upload.concurrency 的配置值）
        ├── 写一条 WARN 日志：
        │     "picgo uploader-target patch not detected; forcing concurrency=1"
        ├── 通过 system.notice 提示管理员（一次即可，不重复刷屏）
        └── 上传仍可正常工作（走 setConfig 串行路径）
```

- **降级的核心价值**：即使有人误把依赖换成了未打补丁的 `picgo`，
  系统**不会静默串床**（把 A 批次的图传到 B 驱动的桶里），而是自动退回串行——
  正确性优先于吞吐。
- UI 表现：存储/内核页显示「当前内核为串行模式（未检测到 uploader-target 补丁）」，
  并把「并发度」设置项置灰。

### 10.5 与配置的交互

| 配置键 | 与补丁的关系 |
|---|---|
| `upload.concurrency` | **唯一**受补丁影响的键。`= 1` 完全不需要补丁；`> 1` 需要 |
| `upload.retryTimes` / `retryBackoffMs` / `itemTimeoutSeconds` | 无关，纯粹是 Go 侧队列行为 |
| `picgo.npmRegistry` / `npmProxy` | 无关（只影响插件安装） |

> 因此「是否打补丁」在运维上表现为「`upload.concurrency` 能不能调到大于 1」，
> 而不是一个独立的开关。**默认 1，开箱即用。**

---

## 11. 日志与可观测性

### 11.1 Go 侧结构化日志（D17）

- 统一 `log/slog`，**JSON Handler**（便于容器收集）。
- 字段约定：

| 字段 | 说明 |
|---|---|
| `time` | RFC3339 |
| `level` | `debug` / `info` / `warn` / `error` |
| `msg` | 人类可读描述（英文，避免编码问题） |
| `RequestID` | 每请求生成（ULID），贯穿 middleware → handler → service |
| `UserUID` | 已登录用户 |
| `JobUID` / `Seq` | 任务相关日志带上 |
| `AgentOK` | agent 调用日志带上 |

> 日志是**结构化输出**（会被容器采集与检索），沿用项目统一的 PascalCase 键名，
> 与 `Detail` / `Metadata` / `Capabilities` 这些 JSON 载体保持一致，避免同一套概念出现两种拼写。

- 级别使用准则：

| 级别 | 用在哪 |
|---|---|
| `debug` | 每次 agent HTTP 调用、SQL 慢查询、reconcile 的逐步动作 |
| `info` | 启动/关闭、迁移版本、Job 结算、SSE 连接建立、限流命中（`action=log`）、补丁探测结果 |
| `warn` | 超时后 agent 仍可能成功、远端删除失败、配额对账发现偏差、agent 重启、配置 SyncPending、**补丁缺失导致降级为串行** |
| `error` | agent 连续探测失败、迁移失败、DB 错误、日志写入失败 |

- **脱敏硬约束**：日志中**绝不**出现
  `StorageSecrets` 明文、SMTP 密码、`oauth.github.clientSecret`、
  JWT/refresh token 原文、API Token 明文。只允许打印 UID 与前缀（`pcw_abc1****`）。
  实现建议：`internal/crypto` 提供 `Mask(string) string`，并在日志 helper 里统一调用。

### 11.2 picgo 日志（只读）

- 来源：`<picgo baseDir>/picgo.log`（`baseDir` = `config.json` 所在目录，
  由 `picgo.configPath` 决定，默认 `<dataDir>/picgo/config.json`）。
- 读取方式：agent 提供 `GET /api/logs?tail=N`（只读尾部，**不提供写入与清空**）。
- Go 侧接口：`GET /api/web/v1/picgo/logs?tail=200`（admin）。
- 前端展示：「存储/内核」页的「内核日志」抽屉，等宽字体、支持关键词高亮；
  **不做实时推送**（按需拉取 + 手动刷新即可，避免无谓的连接占用）。
- 文件轮转由 picgo-core 自己管理；我们不做清理。

### 11.3 任务日志与任务事件的实时推送

**Go → 浏览器**的事件集已由 `API.md` §8.1 固定：

| 事件 | 触发 | 归属字段（PascalCase） |
|---|---|---|
| `job.started` | Job 从 `queued` 转 `running` | `JobUID` |
| `upload.progress` | 单文件进度变化（0..100） | `JobUID` + `UploadUID` + `Seq` |
| `upload.finished` | 单文件成功 | 同上 |
| `upload.failed` | 单文件最终失败（已耗尽重试） | 同上 + `Attempts` |
| `job.log` | 逐行日志（npm 输出、上传过程） | `JobUID` + `Seq` |
| `job.finished` | Job 结束（含 `SucceededItems`/`FailedItems` 等统计） | `JobUID` |
| `system.notice` | 服务端提示（优雅重启、agent 重启、配置 SyncPending、补丁降级） | `Level` + `Message` |
| `ping` | 25 秒保活 | — |

> ❗ **事件字段名是 `JobUID` / `UploadUID`**（不是 `jobId` / `uploadId`），
> 与 `Jobs.UID` / `Uploads.UID` 一致，并遵循 D81 的 PascalCase 与缩写全大写规则。
> 精确字段以 `API.md` §8 为准。

**日志链路**：

```
agent 执行过程中把行推给它自己的 SSE（job.log）
        │
        ▼
Go 订阅 agent SSE（internal/agent 的常驻 reader）
        │
        ├──► 落库 JobLogs（JobUID, Seq, Line, CreatedAt）
        │      Seq 在 Go 侧按 Job 递增，保证顺序
        │
        └──► 广播给浏览器 SSE（GET /api/web/v1/events 的 job.log 事件）
```

- **SSE 断线不丢**：`JobLogs` 已落库，前端重连后可用
  `GET /api/web/v1/jobs/{uid}/logs?afterSeq=<Seq>` 补齐（见 `API.md` §8）。
- **投递范围**：只推给任务所属者与管理员（按 `Jobs.UserUID` 过滤），
  避免越权看到他人上传进度（`API.md` §8.1）。
- 行数保护：单 Job 的 `JobLogs` 上限建议 5000 行（超出后丢弃后续并记一条
  `[log truncated]`），避免 npm 输出把表撑爆。
- 保留期：`log.jobRetentionDays`（默认 7 天），与 `OperationLogs` 分开（D78 生命周期不同）。

### 11.4 前端两个页面的分工

| | 「任务」面板 | 「操作日志」页 |
|---|---|---|
| 数据源 | `GET /api/web/v1/jobs` + SSE | `GET /api/web/v1/logs` |
| 展示 | 进行中/最近批次，带进度条、逐行日志抽屉 | 审计流水，带类型徽章、成功/失败、可搜索 |
| 排序 | 进行中优先，然后 `CreatedAt DESC` | `CreatedAt DESC` |
| 操作 | 重试失败项、清理已完成任务 | 只看（只读视图） |
| 邮件 | 不展示 | 邮件日志单独 Tab（`GET /api/web/v1/logs/emails`） |

> 常见混淆点：用户上传失败时，**两个页面都会有记录**——
> 「任务」里看到 `failed` 的 Job 与具体报错行，「操作日志」里看到一条 `upload` / `failed`。
> 这**不是重复**，而是刻意的分工（§5.1）。前端应在两个页面互相给出跳转链接。

### 11.5 健康与自检

| 端点 | 内容 |
|---|---|
| `GET /healthz`（免鉴权） | `{Status, Version, Agent, Uptime}`；`Agent` ∈ `up`/`down`。**不查数据库**，只反映进程存活与 agent 探测结果 |
| `GET /api/web/v1/system/info` | 版本、agent 状态、默认存储、存储用量统计、picgo 版本与配置路径、插件数 |
| `GET /api/web/v1/system/stats` | 图库统计（总数/总体积/今日/成功/失败） |

---

## 12. 运维速查表

| 现象 | 可能原因 | 排查步骤 |
|---|---|---|
| **上传一直 `queued`，进度不动** | ① worker 未启动 ② `upload.concurrency` 被设为 0 ③ 前置 item 卡在 `running`（超时判定失效） ④ 队列被大 Job 占住 | 1. 看 Go 日志有无 `worker pool started`（§1.4）<br>2. 查 `SystemSettings` 的 `upload.concurrency`，**必须 ≥ 1**（0 会让池空转）<br>3. 查 `JobItems WHERE Status='running'`，看 `StartedAt` 是否超过 `itemTimeoutSeconds`；若是则说明超时判定没生效 → 检查 context 传递<br>4. 查 `Jobs WHERE Status='running'` 的 `TotalItems`，确认不是单个大批次<br>5. 临时手段：重启进程，会按 §2.1 把所有 `running` 重置为 `queued` |
| **上传报 503 / `50002`** | agent 未拉起或已崩溃 | 1. `curl 127.0.0.1:36678/healthz`（带 `X-Agent-Token`）<br>2. 查 Go 日志的 agent 探测记录与重启次数（§2.3）<br>3. 达 5 次退避上限后不再自动重启 → 用后台「手动重启内核」<br>4. 检查 `PICGO_WEB_AGENT_AUTOSTART`、`PICGO_WEB_AGENT_URL`、`PICGO_WEB_AGENT_TOKEN` 是否一致（token 不匹配会一直 401）<br>5. 独立部署时检查 agent 是否监听在 `127.0.0.1` 而非容器外地址 |
| **插件装了但「不生效」** | ① 未启用（`picgoPlugins[name]=false`）② 进程未重启（require 缓存）③ 插件是 `GuiOnly`（Web 端本就不可用）④ 插件依赖缺失 | 1. 查 `GET /api/web/v1/plugins`，看 `Enabled` 与 `GuiOnly` 标志<br>2. 若 `GuiOnly=true` → 该能力仅桌面端可用，属**预期行为**<br>3. 卸装后 agent 会自行重启（设计如此）；若没重启，手动重启内核<br>4. 若插件自带的 uploader 没出现在存储页 → 检查该插件是否真的注册了 uploader（`GET /api/uploaders`），部分插件只提供 `transformer` 或 `guiMenu`<br>5. 看 agent 侧 `picgo.log` 是否有 `Cannot find module`（依赖缺失） |
| **配额数字不对** | 冗余计数漂移（§3.5） | 1. 对账 SQL（§3.5）比对 `Users.UsedBytes` vs `SUM("Uploads"."Size")`<br>2. 若偏差大 → 查是否有绕过 service 层的删除、或进程在事务外被杀<br>3. **等每日维护任务**自动对账（§5.5 第 5 步）修正；**没有手动对账端点**（已裁决不做）<br>4. 注意：图片在**其他用户**名下时不计入本用户配额（D33 图片各自私有）<br>5. `CapacityBytes = 0` 是「不限额」，不是「0 字节」，别把它当异常 |
| **邮件发不出** | ① `mail.enabled=false` ② SMTP 配置错 ③ 端口/加密方式不匹配 ④ 被邮件服务商限流 | 1. 查 `SystemSettings` 的 `mail.enabled`<br>2. 后台点「发送测试邮件」，看返回与 `EmailLogs.Error` 字段<br>3. 465 端口配 `ssl`，587 端口配 `starttls`（**最常见的错配**）<br>4. 检查发件域名的 SPF/DKIM（进垃圾箱不算「失败」，日志会是 success）<br>5. 若报认证失败 → 多数邮箱需要「应用专用密码」而非登录密码 |
| **SQLite 报 `database is locked`** | 并发写冲突 | 1. 确认 DSN 带了 `_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)`（DATA-MODEL.md §0.1）<br>2. 检查是否有长事务：大批删除（§5.5 需分批）、账号注销应走异步 Job<br>3. 检查是否被外部工具（如 DB 浏览器）以写模式打开<br>4. 若确实高并发写入 → 该场景建议切 PostgreSQL（D11/D12），本项目默认 SQLite 面向自用 |
| **PostgreSQL 报 `relation "users" does not exist`（或任何表名找不到）** | **手写 SQL 未给标识符加双引号**。PgSQL 会把未加引号的 `Users` 折成小写 `users`，而实际表名是 `Users`（D81.4） | 1. 定位报错的那条原生 SQL（迁移脚本、DBA 临时查询、对账 SQL）<br>2. **给所有表名与列名加双引号**：`SELECT * FROM "Uploads" WHERE "UserUID" = $1`<br>3. 注意 GORM 生成的 SQL **总是带引号**，因此只有**手写 SQL** 会踩此坑<br>4. SQLite 对 ASCII 大小写不敏感，所以本地开发不会暴露该问题——**必须在 PgSQL 环境下测迁移**<br>5. 迁移脚本中的索引创建同样要加引号（见 DATA-MODEL.md §9.1 示例） |
| **首页显示的不是我选的页面 / 首页白屏** | `theme.active` 指向的主题**缺失、损坏或 `Pages` 非法**，已回退到内嵌默认主题（或主题目录被删） | 1. 查 `SystemSettings` 的 `theme.active`，确认它不是空值<br>2. 确认 `<dataDir>/themes/<theme.active>/` 存在，且含 `manifest.json`、`index.html`、`assets/`<br>3. 检查 `manifest.json` 的 **`ID` 是否与目录名一致**、`Name` 是否非空、`Pages` 是否合法（**不得声明 `/login`、`/admin/**` 等保留路径**）<br>4. 查 `OperationLogs` 中 `Type = theme.error` 的 `Detail.Reason` / `Detail.Path`（§8.4）<br>5. 应急：把 `theme.active` 切回 `default`，或直接删掉坏主题目录后点「重新扫描」<br>6. 注意：**即使 `data/themes/` 整个被删也不会白屏**——首页会以内嵌默认主题渲染，`/login` 与 `/admin/**` 始终可用（§8.7） |
| **删除图片后图床上文件还在** | 该驱动插件未实现 `remove`，或删除时 agent 不可用 | 1. 查该存储的 `Capabilities.SupportsRemoteDelete`<br>2. 若为 `false` → **预期行为**（D47），UI 应已提示「该驱动不支持远端删除」<br>3. 若为 `true` 但仍残留 → 查 `OperationLogs` 中该条的 `Detail.Reason`<br>4. 常见原因：插件删除需要额外字段（如 GitHub 的 `sha`），而 `UploadResults.RawOutput` 缺失（历史数据）→ 无法删除，只能手工清理 |
| **存储配置改了但上传还是用旧的** | reconcile 未执行（agent 不可用时配置只落了 DB，标记 `SyncPending`） | 1. 查 `StorageConfigs.Metadata` 里的 `SyncPending`<br>2. 确认 agent 状态为 `up`，然后触发 reconcile（重启或等自动触发）<br>3. 用 `GET /api/web/v1/picgo/config`（脱敏）核对 `picBed.uploader` 是否与 DB 的 `IsDefault` 一致 |
| **魔法路径没生效** | 该驱动不支持自定义远端路径 | 1. 查 `Capabilities.SupportsPathTemplate`<br>2. 若为 `false` → 已按 D44 降级为「文件名前缀」，在文件名里能看到路径片段<br>3. 若为 `true` 但路径没变 → 查 agent 是否在 `beforeUploadPlugins` 里注册了 rename 钩子（**必须在 `upload()` 之前注册**），且 `PathTemplate` 已随请求下发 |
| **把并发度调到 2 之后，图片传错了存储驱动**（或出现莫名其妙的覆盖） | 用的是**未打补丁**的 picgo-core（例如依赖被换回上游 `picgo`），`UploadOptions.uploader` 被忽略，`picBed.uploader` 被并发互相覆盖 | 1. 先看**是否真的生效了 2 并发**：补丁缺失时服务会**自动强制降级为 1**（不会真并发）<br>2. 查 Go 日志有无 `picgo-core 补丁缺失，并发上传不安全 —— 已强制降级为单并发`<br>3. 查 agent 日志有无 `picgo-core 缺少本项目所需补丁`<br>4. 查 `GET /healthz` 的 `Patches` 字段（或 `GET /api/web/v1/system/info` 的 `Picgo`）<br>5. 确认 `picgo-agent/package.json` 依赖是 **`@yeqingky/picgo-core`**（不是上游 `picgo`）<br>6. 若确实缺补丁 → **这是正确行为，不是故障**：保持 `upload.concurrency = 1`，或把依赖换回 `@yeqingky/picgo-core` 后重建镜像 |
| **上传主题 zip 失败** | 未通过安装校验（§8.9 的 9 条） | 1. 看返回的错误码与消息：`40001` = 校验不过，`40901` = 目标目录已存在（需勾「覆盖」）<br>2. 逐项对照 §8.9：**Zip Slip**（含 `..` 或绝对路径的 entry 会被拒）、含 **symlink** entry、超过 `theme.maxPackageBytes` / `theme.maxExtractBytes` / `theme.maxFileBytes` / `theme.maxFiles`、`manifest.json` 超过 `theme.maxManifestBytes`<br>3. 检查 zip 结构：根（或唯一顶层目录）下必须有 `manifest.json` **且其 `ID` 与目录名一致**，并有 `index.html`<br>4. 阈值可在 `SystemSettings` 调（键名见 DATA-MODEL.md §7.4 的 `category=theme`）<br>5. 失败会清理临时目录，**不会留下半个主题**；若目标目录已存在残留，手工删除后重试 |
| **主题的 JS / CSS 加载 404** | 主题构建时**没配 `base: '/theme-assets/'`**，资源引用仍指向默认的 `/assets/`——那是**内置 SPA 的目录**，自然找不到 | 1. 打开 DevTools → Network，看失败请求的路径：若形如 `/assets/index-xxx.js` 而该文件属于主题 → 就是本问题<br>2. 主题工程里把 Vite 的 `base` 设为 **`/theme-assets/`** 重新构建、重新上传（或替换目录后「重新扫描」）<br>3. 确认 `<dataDir>/themes/<ID>/assets/` 里确实有对应文件（`GET /theme-assets/**` 就是从这读）<br>4. **不要**把主题资源放进 `/assets/` 试图绕过——那个前缀永远属于内置 SPA，会被内置资源覆盖（§8.6） |
---

## 与决策的偏差（已逐条裁决）

本文在撰写中与 `DECISIONS.md` / `DATA-MODEL.md` 的**文档级不一致**。**已全部裁决**，
实现时以裁决结果为准。

| # | 不一致 | 裁决 |
|---|---|---|
| 1 | D45 的 `OperationLog` 结构片段写 `UserID uint64` 与单个 `Target` 字段；`DATA-MODEL.md` §6.1 定义为 `UID` / `UserUID` / `Username` / `TargetType` / `TargetUID` | ✅ **以 `DATA-MODEL.md` 为准**（表结构唯一真源）；D45 已改为引用，不再重复定义 |
| 2 | D47 正文写「`uploads` 表必须存 `raw_output`」，而 `DATA-MODEL.md` 按 D78 拆到 `UploadResults.RawOutput` | ✅ **以 `UploadResults.RawOutput` 为准**；D47 已修正措辞 |
| 3 | D8 写「串行队列（见 D24）」，D24 实为密码登录 | ✅ 已修正为 **D35** |
| 4 | D40 与 D73 都描述上传限流，D40 未给键名 | ✅ **以 D73 为准**（键名 `upload.rateLimit.*`，**默认禁用**）；D40 已标注「已被 D73 取代」 |
| 5 | `Jobs.SkippedItems` 与 D37 的 `skipped` 当前没有产生者（D66 取消去重） | ✅ 保留字段但恒为 `0`，符合 D77「宁可先留着不用」。**实现时不要为它编造逻辑** |
| 6 | 需要「服务正在重启」的 SSE 提示事件，但 API 事件集不含系统类提示 | ✅ **已在 `API.md` §8 新增 `system.notice`**（`data = {Level, Message}`）。本文 §2.2 与 §10.4 使用它 |
| 7 | 邮件邀请/重置的一次性令牌存哪未定 | ⬜ **仍未决**（本文 §6.4）：当前用 `UserSettings` KV 承载（键 `auth.inviteToken` / `auth.resetToken`）；若改为独立表须按 D78 新建并在 DATA-MODEL 登记 |
| 8 | 配额不足错误码：D20 早期写 `40301`，API 已分配 `40302` | ✅ **以 `40302` 为准**（`40301` = 权限不足）；D20 已修正 |
| 9 | Go 侧 `POST /storage/configs/{uid}/test` 依赖 agent 测试原语，而 agent 契约未定义 | ✅ **已在 `API.md` §13 新增 `POST /api/uploaders/test`**，返回 `{Ok, Message, LatencyMs, Detail?}` |
| 10 | 改 `PicgoConfigName` 会因 `createOrUpdate` 语义产生重复配置，且 `UID` 会变、需迁移引用 | ✅ **裁决：`PicgoConfigName` 创建后只读**；`Name` 仅作展示（符合 D64：UID 为唯一标识、Name 仅展示）。`PATCH` 不接受该字段。本文 §7.2 已按此写 |
| 11 | §3.5 曾提出配额对账端点 `POST /system/maintenance/recount`，`API.md` 未定义 | ✅ **裁决：不新增端点**。改为**随每日维护任务静默对账**，仅在发现偏差时写日志（与 D82「不做备份/恢复」同一取向）。本文 §3.5 已按此写 |
| 12 | **命名规范变更（D81）**：全部表名列名与 API 字段由 snake_case 改为 **PascalCase** | ✅ 本文已全文采用 PascalCase（`Uploads.UID` / `Users.UsedBytes` / `JobItems.Attempts` …）。**例外保持不变**：Lsky 兼容层（snake_case + `{status, message, data}`）、环境变量、配置键（`dot.lowerCamel`）、picgo 侧键名（`picBed` / `_configName`）、枚举取值（`upload` / `system.log.cleanup`） |
| 13 | **`DATA-MODEL.md` §4.3 的相册路径注解曾经过时**：旧文写「对外路径为 `/api/v1/gallery/albums`（`/api/v1/albums` 保留给 Lsky）」 | ✅ **已闭环**：按 D80，内部 API 前缀迁至 `/api/web/v1` 后内部相册路径为 **`/api/web/v1/albums`**，**不再有 `gallery/` 中间段**。本文按 D80 写（§1.3 第 ③ 步等处用 `/api/web/v1/albums/{uid}/move-uploads`）。已核实 `DATA-MODEL.md` §4.3 的注解已同步修正 |
| 14 | **`DECISIONS.md` 部分历史条目的行文仍用 snake_case**（如 D20 的 `users.capacity_bytes`、D22 的 `storage_configs.updated_at`、D64 的 `storage_configs.uid`） | ✅ **非冲突**：按「新决策追加编号、不改历史条目」的维护规则，历史条目保持原样；**表结构一律以 `DATA-MODEL.md` 为准**（已全部 PascalCase）。本文所有表列引用均按 DATA-MODEL 书写 |
| 15 | **主题范围变更（D94–D99）**：先前设计为「前端整体以主题形式交付、**不内置**」，现改为「**前端内置**（`go:embed web/dist`）+ **主题只接管 `manifest.Pages` 声明的路径**，**默认主题只注册首页**」；并新增 `ThemeConfigs` 独立表、`Pages` 自行注册机制、以及「**认证页与 `/admin/**` 永久保留**」的安全默认值 | ✅ **本文已整体按最终定义重写**：**新增 §8 全节**（职责边界 / 磁盘布局 / 启动 seed / 扫描校验 / 请求分发 / 资源托管 / 兜底降级 / 配置读写 / zip 九条校验 / 切换生效 / 运维要点）；§0 总览与「命名与路径约定」已补行；§5.3 已补 **7 个 `theme.*` 日志类型**；§12 速查表已增 3 行。**已彻底移除**的服务端内容：`site.background.*` 全套键、`site/background` 端点、ACG 直链缓存与定时任务、「背景图三种模式」——按 D97 背景图改为**主题配置项 `BackgroundURL` 单一 URL，不做任何判断** |
| 16 | **本文小节编号因新增 §8 而后移**（删除流程 §8→**§9**、PicGo-Core 补丁 §9→**§10**、日志与可观测性 §10→**§11**、运维速查表 §11→**§12**） | ✅ 本文**内部**交叉引用已同步更新（含 §5.5、§7.2、§8.4/§8.9 的内部指向与 §12 速查表中的 `§10.4`）。⚠️ **`docs/README.md` 中按序号引用本文的地方需同步**：其「改存储驱动 / 插件」一行写的是 `OPERATIONS.md §7–§8`，重编号后 **§7 仍是「存储驱动同步」，但 §8 已变为「主题系统」**（删除流程现为 §9）→ 建议改为 `§7` 与 `§9`。**本次仅被授权写 `OPERATIONS.md`，故在此登记** |

> 除上表外，本文与 `DECISIONS.md` / `DATA-MODEL.md` / `API.md` 无其它已知冲突。
