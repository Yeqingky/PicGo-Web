# picgo-agent

**PicGo-Web 的 Node 侧车**：持有唯一的 `picgo-core` 实例，把它包成一个
**仅监听 127.0.0.1** 的小型 HTTP 服务供 Go 主服务调用。

> 契约唯一真源：`docs/API.md` §13（picgo-agent 内部契约）。
> 本文件只讲「怎么跑、怎么调、有哪些坑」。

---

## 为什么需要它

`picgo-core` 的插件是 **npm 包 + `require()` 动态加载**，只能在 Node 运行时里跑：

- 插件通过 `require('picgo-plugin-xxx')` 注册 uploader / transformer
- 插件配置表单是 `config(ctx) => IPluginConfig[]`，其中 `default` / `choices` **可能是函数**
- 插件安装/卸载本质是 `npm install` / `npm uninstall` 到 `<baseDir>/node_modules/`

Go 无法安全地加载这些东西（也不该把 Node 打包进 Go 二进制）。
因此沿用 PicGo GUI / CLI 的同一模式：**一个 Node 进程 = 一个 `PicGo` 实例**。

```
Browser ──► Go server (:8080) ──► picgo-agent (:36678, 仅回环) ──► picgo-core ──► 图床
                    │  HTTP + SSE，带 X-Agent-Token
                    └── 拉起/健康探测/退避重启子进程
```

---

## 快速开始

### 前置：PicGo-Core 必须已构建

`package.json` 里 `"picgo": "file:../../PicGo-Core"`（agent 与 PicGo-Core 同级）。

```bash
# 在同级的 PicGo-Core 目录里（分支 PicGo-Web）
pnpm install
pnpm build      # ⚠️ 必须：dist/ 被 gitignore，且没有 prepare:build 钩子
```

> ⚠️ **不能用 git 依赖**：`PicGo-Core` 的 `dist/` 被 gitignore 且没有 `prepare` 构建钩子，
> 因此 `"picgo": "github:..."` 装出来的包**没有 dist**。只能用 `file:` 或 tarball。

### 启动

```bash
cd picgo-agent
pnpm install

# 开发（tsx watch，改代码自动重启）
PICGO_AGENT_TOKEN=devtoken \
PICGO_AGENT_CONFIG_PATH=$PWD/../data/picgo/config.json \
pnpm dev

# 生产（先构建，再由 Go 拉起或手动跑）
pnpm build
PICGO_AGENT_TOKEN=$TOKEN PICGO_AGENT_CONFIG_PATH=/data/picgo/config.json node dist/index.js
```

**实际部署时**：agent 由 Go 主服务以子进程方式拉起（`PICGO_WEB_AGENT_AUTOSTART=true`），
Go 会注入 `PICGO_AGENT_CONFIG_PATH` 与 `PICGO_AGENT_TOKEN`，**不需要手动启动**。

### 冒烟验证

```bash
T=devtoken
curl -sS :36678/healthz                      # 免 token，返回 {Ok, PicgoVersion, ConfigPath, ...}
curl -sS -H "X-Agent-Token: $T" :36678/api/uploaders | head -c 400
```

---

## 环境变量

| 变量 | 默认 | 说明 |
|---|---|---|
| `PICGO_AGENT_PORT` | `36678` | 监听端口。**刻意避开 `36677`**——那是 picgo-core 内置 server 的默认端口 |
| `PICGO_AGENT_HOST` | `127.0.0.1` | 监听地址。**保持回环**；设成其它值会打 WARN |
| `PICGO_AGENT_TOKEN` | （空） | 与 Go 的共享令牌（请求头 `X-Agent-Token`）。空且未开 `ALLOW_NO_TOKEN` 时**拒绝启动** |
| `PICGO_AGENT_ALLOW_NO_TOKEN` | `false` | 允许无令牌访问。**仅本地调试**，启动时打 WARN |
| `PICGO_AGENT_CONFIG_PATH` | `<cwd>/data/picgo/config.json` | picgo 的 `config.json` 路径。**其父目录同时是插件安装目录** |
| `PICGO_AGENT_LOG_LEVEL` | `info` | `debug` / `info` / `warn` / `error`。`debug` 时同时开启请求日志 |
| `PICGO_AGENT_NPM_REGISTRY` | （空） | 插件安装用的 npm 源（空则用 picgo 自带默认） |
| `PICGO_AGENT_NPM_PROXY` | （空） | npm 代理 |
| `PICGO_AGENT_UPLOAD_PROXY` | （空） | 上传代理；非空时写入 `picBed.proxy` |
| `PICGO_AGENT_CONFIG_BACKUP_COUNT` | `5` | 写 config 前轮转备份的份数（`bak.0`…） |
| `PICGO_AGENT_REMOVE_TIMEOUT_MS` | `3000` | 远端删除时等插件通知的超时 |
| `PICGO_AGENT_UPLOAD_CONCURRENCY` | `1` | 上传并发度。**1 = 严格串行**，此时 `failed` 事件与本次上传一一对应，错误原因绝对准确 |

> **命名**：环境变量保持 `UPPER_SNAKE_CASE`（D81.3 第 2 条）；
> 而 agent 的 **HTTP JSON 字段一律 PascalCase**（`JobUID` / `PicgoVersion` / `SupportsRemoteDelete`）。

---

## 端点一览

> 除 `/healthz` 外，**全部需要 `X-Agent-Token`**。
> 统一响应体 `{ Code, Message, Data }`，`Code` 是**字符串**：
> `OK` / `ERR_PARAM` / `ERR_PICGO` / `ERR_NOT_FOUND` / `ERR_INTERNAL`。

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/healthz` | **免 token、无信封**（唯一例外）。返回扁平 PascalCase 对象 |
| GET | `/api/config` | 完整 picgo 配置（**未脱敏**，仅内网） |
| PUT | `/api/config` | 「整体替换」→ **实际只替换管辖键**（见下） |
| PATCH | `/api/config` | 点路径合并，**只允许管辖键** |
| GET | `/api/uploaders` | 全部驱动 + 求值后的表单 schema + 能力探测 |
| POST | `/api/uploaders/schema` | 带 `Answers` 重求值（`dependsOn` 联动） |
| POST | `/api/uploaders/test` | 连通性测试（真上传一张 1×1 PNG） |
| GET | `/api/uploaders/configs?Type=` | 某类型的多配置列表（**原样 picgo 形态**） |
| POST | `/api/uploaders/configs` | `createOrUpdate` |
| DELETE | `/api/uploaders/configs?Type=&ConfigName=` | 删除配置 |
| POST | `/api/uploader/use` | 切换当前上传器 |
| GET | `/api/transformers` | transformer 列表 |
| GET | `/api/plugins` | 已装插件（含 `GuiOnly`） |
| GET | `/api/plugins/{name}/readme` | 插件 README |
| POST | `/api/plugins/install` \| `uninstall` \| `update` | → `{JobUID}`，**异步** |
| PATCH | `/api/plugins/{name}` | 启用 / 禁用 |
| POST | `/api/upload` | **单文件 + 同步**上传 |
| POST | `/api/delete` | 远端删除（走 `remove` 事件约定） |
| GET | `/api/jobs` · `/api/jobs/{uid}` · DELETE `/api/jobs/{uid}` | 任务（内存） |
| GET | `/api/events` | SSE：`upload.progress` `upload.finished` `upload.failed` `job.log` `job.finished` `job.started` `system.notice` `ping` |
| GET | `/api/logs?tail=200` | 读 `<baseDir>/picgo.log` 尾部 |
| POST | `/api/shutdown` | 优雅退出 |

---

## 必须知道的 6 个坑

这一节是本项目的「血泪清单」，改代码前请先读。

### 1. `upload()` 的失败**不抛错**

传路径上传失败时，picgo **既不 reject 也不返回 `Error`**，而是：
- 吞掉异常
- 返回**可能为空的数组**
- 同时 `emit('failed', err)`

**唯一例外**：`options.uploader` 指向不存在的 `type` / `configName` 时 picgo **会抛错**。

因此 `performUpload` 必须覆盖两条路，并区分它们：

| 情形 | 判定 | HTTP |
|---|---|---|
| 目标非法（抛错） | `kind = 'param'` | **400** `ERR_PARAM` |
| 上传失败（空数组 / 无 imgUrl） | `kind = 'picgo'` | **200** `ERR_PICGO` |

返回 200 是**刻意**的：Go 侧要逐项记录失败原因，不应被 HTTP 层拦住。

### 2. 驱动常抛**非 Error 对象**

`tcyun` / `github` 等驱动的 throw 形态是 `{ statusCode, body }`，axios 是
`{ response: { status, data } }`。

直接用 `String(err)` 会得到 **`"[object Object]"`**，把失败原因彻底丢掉。
因此统一走 `src/errors.ts` 的 `describeError()`（含循环引用保护）。

### 3. 事件是**全局**的，归属靠串行化

picgo 的事件 `emit` 绑定在**根实例**上：

- `finished` / `afterUpload` → **带 ctx**，可用 `ctx.contextData.JobUID` 精确归属
- `failed` → **只带 error**
- `uploadProgress` → **只带一个数字**

后两者无法用 `contextData` 过滤。因此默认 `PICGO_AGENT_UPLOAD_CONCURRENCY=1`
（严格串行），这样「此刻收到的事件一定属于本次上传」。

调大并发时：**成功/失败的判定仍然可靠**（看返回值长度），但**错误文案可能串到别的请求上**。

### 4. config 只能**键级合并**，绝不整体重建

插件会往 config 里写自己的状态，例如 `picgo-plugin-github-plus` 写：

```jsonc
{ "uploaded": [ /* 图片账本 */ ], "picgo-plugin-github-plus": { "lastSync": 12345 } }
```

整体重写会**毁掉插件的账本**。因此：

- 只允许写 `picBed.*` / `uploader.*` / `picgoPlugins.*` / `settings.*`
- 其余键**原样保留**
- `PATCH` 写非管辖键 → **400 拒绝**
- `PUT` 也会**拒绝**不含任何管辖键的请求体，且实际仍只替换管辖键

每次写入前会把 config 轮转备份为 `config.json.bak.0..N`。

### 5. `Uploader.Type` 必须转成 picgo 的 `uploader.type`

命名是两套（D81）：

| 层 | 命名 | 例子 |
|---|---|---|
| agent 的 HTTP JSON | **PascalCase** | `{ "Uploader": { "Type": "github", "ConfigName": "Work" } }` |
| picgo 的 API 参数 | camelCase | `{ uploader: { type: 'github', configName: 'Work' } }` |

**不要把 `Type` 直接传给 picgo**，否则匹配不到配置（`upload.ts` 里的 `toPicgoUploader` 负责转换）。

同理，**从 picgo 读来的东西一律原样透传、不转换**：
`picBed` / `picgoPlugins` / `_id` / `_configName` / 驱动字段名（`repo` / `token` / `path`）/
`IImgInfo`（`fileName` / `imgUrl` / `extname` / `sha`）。

### 6. 远端删除**不可靠**，UI 必须如实告知

picgo-core **没有**删除 API。生态的事实约定是：

```ts
picgo.emit('remove', files, guiApi)   // 调用方
ctx.on('remove', onRemove)            // 插件侧（如 github-plus）
```

三个不可靠点：
1. `emit` **无返回值**
2. 插件的 handler 是 **async 且无人 `await`**
3. `guiApi` 是 **Electron 专属**（插件会解构 `showNotification`）

因此我们造一个 `guiApi` shim，`emit` 后**等一小段**，从捕获到的通知文案反推结果：

| 响应 | 含义 |
|---|---|
| `Supported=false` | 该驱动不支持远端删除 → Go 只删本地记录 |
| `Supported=true, RemoteDeleted=false` | 插件实现了但失败/沉默 |
| `Supported=true, RemoteDeleted=true` | 远端已删 |

**必要条件**：`RawOutput` 必须保存完整的 `IImgInfo`（含插件回写的 `sha` / `mockKey` 之类），
否则删远端时字段丢了，插件无法定位文件。

---

## 目录结构

```
picgo-agent/
├── package.json  tsconfig.json  tsconfig.build.json  vitest.config.ts  eslint.config.mjs
└── src/
    ├── index.ts              入口：env → logger → PicGo 单例 → 注册钩子 → HTTP → 优雅退出
    ├── env.ts                环境变量
    ├── logger.ts             JSON 行日志
    ├── types.ts              与 docs/API.md §13 一一对应的类型
    ├── errors.ts             describeError()：非 Error 对象 / 循环引用的可读化
    ├── context.ts            应用上下文（显式容器，便于测试）
    ├── http/
    │   ├── server.ts         hono 装配 + 404 + 兜底错误处理
    │   ├── envelope.ts       {Code, Message, Data} 响应助手
    │   └── auth.ts           X-Agent-Token 中间件（恒定时间比较）
    ├── picgo/
    │   ├── instance.ts       单例 PicGo + baseDir 准备
    │   ├── config.ts         键级合并 + 非管辖键校验 + 写前备份
    │   ├── uploaders.ts      uploaderConfig 封装 + schema 求值 + schema-only context
    │   ├── capability.ts     能力探测 + 进程内缓存（不硬编码驱动名）
    │   ├── template.ts       模板引擎（D70 变量 + 惰性哈希 + 路径清洗）
    │   ├── rename.ts         beforeUploadPlugins 钩子（魔法命名）
    │   ├── upload.ts         单文件同步上传 + 失败语义归一化 + 串行闸门
    │   ├── remove.ts         远端删除 + guiApi shim + 文案反推
    │   └── plugins.ts        插件列表 / README / 安装 / 卸载 / 更新 / 启停
    ├── jobs/
    │   ├── store.ts          内存任务表 + 日志环形缓冲
    │   └── sse.ts            SSE 广播（25s ping）+ 事件类型
    ├── routes/               healthz / config / uploaders / plugins / upload / remove / jobs / events / logs / shutdown
    └── testing/
        └── fake-picgo.ts     假 PicGo（单测用；不打进 dist）
```

---

## 开发与验证

```bash
pnpm install
pnpm typecheck    # tsc --noEmit
pnpm lint         # eslint src
pnpm test         # vitest run（168 个用例）
pnpm build        # tsc -p tsconfig.build.json → dist/
pnpm dev          # tsx watch
```

### 单测覆盖（168 个用例）

| 文件 | 覆盖内容 |
|---|---|
| `picgo/template.test.ts`（31） | D70 全部变量、惰性哈希、路径清洗、路径降级、未知变量保留、兜底 |
| `picgo/capability.test.ts`（24） | 路径字段识别、能力组装、`remove` 监听探测、插件提供的 uploader、GuiOnly、缓存 |
| `picgo/config.test.ts`（22） | **键级合并**、非管辖键拒绝、`PUT` 过滤、**插件私有键保留回归**、备份轮转 |
| `picgo/upload.test.ts`（21） | **两条失败路径**（抛错 vs 空数组）、命名转换、`contextData`、Raw 保留、串行闸门 |
| `picgo/remove.test.ts`（21） | 文案反推优先级、guiApi shim、无监听器、沉默、插件抛错、原样传递 IImgInfo |
| `jobs/store.test.ts`（17） | 状态流转、日志环形缓冲与增量拉取、清理与 GC |
| `picgo/rename.test.ts`（16） | 钩子注册、`contextData` 读取、端到端改名、无 magicPath 时 no-op |
| `errors.test.ts`（16） | `describeError` 各形态 + **循环引用** + 截断 |

### 端到端验证（curl 脚本）

仓库外维护的验证脚本覆盖（可复现）：
注册一个真实图床插件 → 配两条同类型配置 → 上传（含魔法路径/文件名）→
校验图床实际收到的文件名 → 失败语义 → 非法目标 → 路径降级 → 远端删除 →
SSE 事件 → **并行上传到两个不同配置（验证 PicGo-Core 补丁的按次指定图床）**。

---

## 与 Go 主服务的关系

| 项 | 说明 |
|---|---|
| 谁启动谁 | **Go 拉起 agent 子进程**（`PICGO_WEB_AGENT_AUTOSTART=true`），注入 configPath 与 token；Go 退出时调 `POST /api/shutdown` |
| 健康探测 | Go 定期 `GET /healthz`（2s 超时），失败退避重启（最多 5 次） |
| 重启时机 | ① Go 主动重启；② agent 在**插件装卸后主动 `process.exit(0)`**（换取模块图干净） |
| config 真相源 | **Go 的 DB 是真相源**，agent 的 `config.json` 是投影（键级合并写入） |
| 凭据 | agent 侧**不脱敏**（它就是 picgo 的真实存储）；**脱敏是 Go 的职责**，Go 不可把 agent 的响应原样回传前端 |
| 任务 | agent 的任务只维护**执行期**状态（内存，重启即丢）；持久化的 `Jobs` 在 Go 侧 |
| 契约 | Go **单向消费** agent 的契约；agent 内部实现可重构 |

### 首次启动时 agent 会做什么

1. 读 env，校验 token（缺失且未开 `ALLOW_NO_TOKEN` → 拒绝启动）
2. `ensureBaseDir`：创建 `<configPath 的父目录>` 与 `<baseDir>/package.json`（插件清单）
   —— **刻意不污染 `~/.picgo`**
3. `new PicGo(configPath)`，写 `PICGO_ENV=WEBUI`（插件用它做环境判断）
4. 注册魔法命名钩子（`picgo-web-magic-path`）—— **必须在任何 upload 之前**
5. 起 HTTP 服务

---

## 相关文档

| 文档 | 内容 |
|---|---|
| `docs/API.md` §13 | **本 agent 的接口契约（唯一真源）** |
| `docs/PICGO-INTEGRATION.md` | picgo-core 集成细节：公开 API、魔法路径机制、config 边界、远端删除、补丁清单 |
| `docs/OPERATIONS.md` | 运行机制：队列、配额、限流、删除流程 |
| `docs/DECISIONS.md` | 全部决策（D22 config 边界 / D39 单文件上传 / D44 魔法路径 / D47 远端删除 / D81 命名） |
| `../PicGo-Core/FORK-NOTES.md` | 我们在 PicGo-Core 上打的补丁（`UploadOptions.uploader` / `contextData` / per-ctx 配置覆盖） |
