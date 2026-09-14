# PicGo 集成与 picgo-core 边界

> **上位约束**：本文档服从 [`DECISIONS.md`](./DECISIONS.md)（尤其 D2、D6–D8、D22、D35–D44、
> D47–D51、D64–D70、D77、D81、D82）。
> 表名/列名以 [`DATA-MODEL.md`](./DATA-MODEL.md) 为准（**PascalCase**）；
> agent 的 HTTP 契约见 [`API.md`](./API.md) §13。
>
> **本文所有结论均来自实测**：`picgo-core` v3.0.2（本地 `PicGo-Core`，分支 `PicGo-Web`，
> 基线上游 `dev`）与真实插件源码（`picgo-plugin-github-plus@1.2.3`、`picgo-plugin-lankong@1.1.3` 等）。
> 引用处标注了具体文件与行号，便于复核。
>
> **命名约定（D81）**：本文提到的**我们自己的**字段名用 PascalCase
> （如 `StorageConfigs.PathTemplate`、`Capabilities.SupportsRemoteDelete`、`UploadResults.RawOutput`）；
> **picgo 与插件定义的**字段名一律原样保留
> （如 `picBed`、`_configName`、`IImgInfo.fileName`、`IPluginConfig.dependsOn`）。

---

## 1. 结论先行

**`picgo-core` v3 的公开 API 已覆盖本项目所需的全部能力** —— 编程式上传、同类型多配置图床、
插件列出/启停/安装/卸载/更新、插件配置表单 schema 求值、日志读取、内置 HTTP 服务。

**四项能力缺口我们自己补，且补丁已落地并验证通过**（§8）：

| 缺口 | 解决方式 | 状态 |
|---|---|---|
| 按次指定上传目标（core 的 `upload()` 只能读全局 `picBed.uploader`） | 补丁 P1+P2：`UploadOptions.uploader` + per-context 配置覆盖 | ✅ **已落地** |
| 并发下的事件归属（`finished`/`failed` 是全局事件） | 补丁 P4：`UploadOptions.contextData` | ✅ **已落地** |
| `Lifecycle.step` 并发串扰 | 补丁 P3：实例字段 → 局部变量 | ✅ **已落地**（顺手修真 bug） |
| 删除远端文件（core **没有**删除 API） | 走 `remove` 事实约定 + `guiApi` shim | ✅ 方案确定（§7） |

**两条运行路径，业务代码同形**：

| 路径 | 并发度 | 依赖补丁？ | 做法 |
|---|---|---|---|
| **默认** | `upload.concurrency = 1`（D35） | ❌ **不依赖** | `picgo.setConfig()`（只改内存、不落盘）串行切换驱动 |
| **并发** | `= N > 1` | ✅ 使用补丁 | `picgo.upload(paths, { uploader, contextData })` |

> 切换只需改 `upload.concurrency` 配置值，**不动任何业务代码**。
> 这就是把 agent 的上传端点设计成「单文件 + 同步」（D39）的价值：
> 两条路径在 Go 侧完全同形，差异被压在 agent 的一个分支里。

---

## 2. 为什么必须有 Node 侧车

`picgo-core` 的插件是 **npm 包**，通过 `require()` 在运行时动态加载；**只能在 Node 运行时里跑**，
Go 无法安全加载它们。GUI（Electron）与 `picgo` CLI 都是 Node 宿主，本项目沿用同一模式。

```
┌─────────────────────────────┐
│  Browser (React SPA)        │
└──────────┬──────────────────┘
           │ HTTP / SSE
┌──────────▼──────────────────┐
│  Go server  :8080           │  鉴权 / DB / REST / SSE / 托管前端
└──────────┬──────────────────┘
           │ HTTP + SSE (127.0.0.1:36678, X-Agent-Token)
┌──────────▼──────────────────┐
│  picgo-agent (Node 24/TS)   │  持有唯一 PicGo 实例、插件、上传队列
└──────────┬──────────────────┘
           │ 进程内调用
┌──────────▼──────────────────┐
│  picgo-core  (PicGo)        │
│    ├── <baseDir>/config.json│
│    ├── <baseDir>/package.json  ← 插件清单
│    ├── <baseDir>/node_modules/ ← 插件实体
│    └── <baseDir>/picgo.log  │
└──────────┬──────────────────┘
           │ HTTPS
      图床（GitHub / S3 / WebDAV / …）
```

> 端口分配见 D6：Go 服务 `:8080`、agent `:36678`、picgo 内置 server `36677`（默认不启用）。
> 早期设计曾让 Go 服务占用 `36677`，与 picgo-core 内置 server 的默认端口冲突，**已修正**。

### 2.1 `baseDir` 的语义（实测）

`baseDir = path.dirname(configPath)`（`src/core/PicGo.ts` `initConfigPath()`）。
插件系统的全部文件都挂在 `baseDir` 下，实测自 `src/lib/PluginLoader.ts`：

| 环节 | 实测行为 |
|---|---|
| 插件清单来源 | `load()` 读 `<baseDir>/package.json` 的 `dependencies` + `devDependencies` |
| 名称过滤 | 正则 `/^picgo-plugin-\|^@[^/]+\/picgo-plugin-/` |
| 存在性校验 | `resolvePlugin()` 用 `resolve.sync(name, { basedir: baseDir })`，失败则回退 `<baseDir>/node_modules/<name>` |
| 加载 | `getPlugin()` 用 `require(<baseDir>/node_modules/ + name)`，**无 cache-busting** |
| 缺失时 | `init()` 自动创建一个占位 `package.json`（`name: picgo-plugins`） |
| `node_modules` 不存在 | `load()` 直接 `return false`，不报错 |

**推论（对 agent 的硬要求）**：

1. `baseDir` 必须真实可写，且能在其中执行 `npm install`（`pluginHandler` 依赖它）。
2. 插件必须安装到 `<baseDir>`，**所以 configPath 不能是用户主目录**
   （D7/D8 的 `PICGO_AGENT_CONFIG_PATH` 就是为此）。
3. `require()` 有模块缓存 → 安装/卸载后**必须重启 agent 进程**才能让插件生效（见 §9.5）。

---

## 3. picgo-core 公开 API 清单（本项目实际使用）

入口导出实测自 `src/index.ts`：`PicGo`、`evaluatePluginConfig`、`Logger`、`PluginHandler`、
`PluginLoader`、`LifecyclePlugins`、`Request`、`ServerManager`、`PicGoUtils` 等。

### 3.1 构造与配置

```ts
import { PicGo, evaluatePluginConfig } from 'picgo'

const picgo = new PicGo(configPath)   // 传 config.json 的绝对路径；不传则用 ~/.picgo/config.json
```

| 成员 | 用途 | 备注 |
|---|---|---|
| `picgo.configPath` / `picgo.baseDir` | 配置路径与其目录 | `baseDir` 是插件根（§2.1） |
| `picgo.getConfig<T>(name?)` | 点路径读取（`lodash.get`） | 不传 `name` 返回整份 config |
| `picgo.saveConfig(map)` | 点路径写入 **并落盘** | `setConfig` + DB 持久化 |
| `picgo.setConfig(map)` | **只改内存，不落盘** | 默认并发路径切驱动用它（D8） |
| `picgo.unsetConfig(key, prop)` | 只改内存删除子键 | |
| `picgo.removeConfig(key, prop)` | 改内存 + 落盘删除 | |
| `picgo.VERSION` | picgo-core 版本 | 写入 `Capabilities.PicgoVersion` |
| `picgo.log.{success,info,warn,error,debug}` | 日志（同时写 `<baseDir>/picgo.log`） | 见 §3.9 |
| `picgo.uploaderConfig` | 多配置图床管理（§3.5） | |
| `picgo.pluginLoader` / `picgo.pluginHandler` | 插件（§3.6、§3.7） | |
| `picgo.helper` | 六个生命周期容器（§3.4） | |
| `picgo.server` | 内置 Hono HTTP 服务（§3.9，可选） | |

**黑名单注意**：`setConfig` / `removeConfig` 会过滤 `isConfigKeyInBlackList()` 命中的键，
命中时只打 warn 不生效。写配置时应校验返回值/日志，不要假设一定成功。

### 3.2 上传 —— **失败语义是本项目最大的坑**

```ts
async upload (input?: any[], options?: UploadOptions): Promise<IImgInfo[] | Error>
```

实测 `src/core/PicGo.ts` 的两个分支行为**不一致**：

| 分支 | 触发条件 | 失败行为 |
|---|---|---|
| **按路径上传**（本项目唯一使用） | `input` 是非空数组 | **不抛异常**。`Lifecycle.start()` 内部 catch 后，除非 `getConfig('debug')` 为真值，否则**吞掉异常**并 `return ctx`；`upload()` 随后返回 `ctx.output`（**可能为空数组**，也可能只含部分成功项） |
| 剪贴板上传 | `input` 为空/未传 | catch 里 `throw e` → **会 reject** |

实测 `src/core/Lifecycle.ts` 的 catch 块：

```ts
} catch (e: any) {
  // 若错误发生在 UPLOAD 阶段且已有部分项拿到 imgUrl，仍跑 afterUpload
  if (step === LifecycleStep.UPLOAD && ctx.output.some(item => item.imgUrl !== undefined)) {
    try { step = LifecycleStep.AFTER_UPLOAD; await this.afterUpload(ctx, options) } catch {}
  }
  ctx.log.warn(IBuildInEvent.FAILED)
  ctx.emit(IBuildInEvent.UPLOAD_PROGRESS, -1)
  ctx.emit(IBuildInEvent.FAILED, e)
  ctx.log.error(e)
  if (ctx.getConfig<Undefinable<string>>('debug')) { throw e }   // ← 只有 debug 才抛出
  return ctx                                                     // ← 默认吞掉
}
```

**因此 agent 必须做三件事，缺一不可**：

1. **监听 `failed` 事件** 拿真正的 `Error`（这是唯一可靠的错误来源）；
2. **校验返回值**：逐项检查 `imgUrl` 是否非空字符串，为空即视为该项失败；
3. **设置超时**（`upload.itemTimeoutSeconds`）：被吞掉的错误不会立刻冒泡，
   若图床挂起，`await upload()` 可能长时间不返回。

> `debug` 配置：picgo-core 支持 `getConfig('debug')`。**不要为了「让错误抛出来」而全局开启 debug** ——
> 它同时影响插件行为与日志噪音。用事件监听才是正解。

#### ⚠️ 唯一例外：非法 `uploader` 目标 **会抛出**（补丁行为）

我们的补丁（§8）在校验 `options.uploader` 时**故意**让它以 **rejection** 抛出，
而不是被上面的 catch 吞成一个空数组：

```
Uploader type "nosuchtype" not found
Uploader config "nope" not found for type "mock"
No config found for uploader type "mock". Configure it before uploading.
```

理由：这是**调用方/配置错误**，不是上传失败，必须让调用方立刻看到。
实测已确认（`uploadTargetOverride.spec.ts` 的两个用例）。

**agent 的处理**：捕获这三个错误 → 映射为 **HTTP 400 / `40001`**（§9.6），
不要与「上传失败（200 + `50003`）」混为一谈。

#### 上传输入形态与返回值

```ts
await picgo.upload(['/abs/path/a.png'])                        // 文件路径数组（推荐）
await picgo.upload([{ buffer, fileName, extname }])             // 内存 buffer（transformer 需支持）

// 补丁新增：按次指定目标 + 事件归属（§8）
await picgo.upload(['/abs/path/a.png'], {
  uploader: { type: 'github', configName: 'work' },
  contextData: { jobUid: 'job_01HXX' }
})
```

返回值 `IImgInfo`（`src/types/index.ts`，本项目关心的字段）：

```ts
{
  fileName?: string; extname?: string; imgUrl?: string; originImgUrl?: string;
  width?: number; height?: number; size?: number;
  contentType?: string; mimeType?: string;   // mimeType 已 @deprecated
  origin?: string; type?: string;            // type 由 Lifecycle 在成功后回填为 uploader 名
  [propName: string]: any;                   // ← 插件可回写任意字段（如 github-plus 的 sha）
}
```

> **末尾的 `[propName: string]: any` 索引签名是远端删除能工作的前提**（§7）：
> 插件会往返回项上回写任意字段（如 `github-plus` 的 `img.sha`）。完整返回值必须落库到
> **`UploadResults.RawOutput`**（DATA-MODEL §4.2），否则这些字段丢失后**再也删不掉**。

**另一个重要事实**：`createContext()` 每次上传都会创建全新的 `output: []` 与 `input: []`
（实测 `src/utils/createContext.ts`），所以 **`upload()` 的返回值在并发下是准确的**，
不会串到别人的结果。并发下会串的**只有配置读取与事件**（§3.3）。

### 3.3 事件

`picgo.on(event, handler)`。实测事件名来自 `src/utils/enum.ts`（`IBuildInEvent`）：

| 事件 | 参数 | 用途 | 本项目是否用 |
|---|---|---|---|
| `uploadProgress` | `0 \| 30 \| 60 \| 100 \| -1` | **仅 4 档 + 失败**，粒度极粗 | ⚠️ 仅在单文件上传内做插值 |
| `beforeTransform` | `ctx`（`ctx.input`） | 原始输入 | 记录用 |
| `beforeUpload` | `ctx`（`ctx.output`：`base64Image`/`fileName`/`width`/`height`/`extname`） | 转换后、上传前 | 与魔法路径联动（§5） |
| `afterUpload` | `ctx.output`（已含 `imgUrl`） | 拿到 URL | 记录用 |
| `finished` | `ctx.output` | 全生命周期插件跑完的最终结果 | ⚠️ 需归属（见下） |
| `failed` | `error` | **唯一可靠的失败来源** | ✅ 必用 |
| `notification` | `{ title, body, text? }` | 图床抛出的人类可读提示 | ✅ 透传到前端 toast |

#### 并发下事件无法归属（实测根因）

`src/utils/createContext.ts` 里事件方法是**绑定到根实例**的：

```ts
on:    ctx.on.bind(ctx),
once:  ctx.once.bind(ctx),
emit:  ctx.emit.bind(ctx),
// …其余 EventEmitter 方法同理
```

即：**所有上传上下文共享同一个 EventEmitter**。两个上传并发时，A 注册的 `finished` 监听器
会收到 B 的成功事件 —— 无法从事件本身判断属于哪个任务。

| 并发度 | 处置 |
|---|---|
| `= 1`（默认） | 全局事件天然无歧义：任意时刻只有一个上传在跑。**这是 D35 选默认 1 并发的一个额外收益** |
| `> 1` | 用补丁 **P4** 的 `contextData` 回传 `jobUid` 做归属（§8.3） |

> 与返回值的关系：**返回值准确、事件不准确**。所以即使并发，只要不用事件做归属，
> 用「返回值 + 每文件独立 HTTP 调用」的模型也是安全的（这正是 D39「单文件 + 同步」的设计依据）。

### 3.4 生命周期钩子

```ts
picgo.helper.beforeTransformPlugins.register(id, { handle: (ctx) => { /* ... */ } })
picgo.helper.beforeUploadPlugins.register(id,      { handle: (ctx) => { /* ... */ } })
picgo.helper.afterUploadPlugins.register(id,       { handle: (ctx) => { /* ... */ } })
picgo.helper.afterFinishPlugins.register(id,       { handle: (ctx) => { /* ... */ } })
```

- 六个容器：`transformer` / `uploader` / `beforeTransformPlugins` / `beforeUploadPlugins` /
  `afterUploadPlugins` / `afterFinishPlugins`（`picgo.helper` 实测字段）。
- ⚠️ **必须在调用 `upload()` 之前注册**，否则本次上传不生效。
- 同一个 id 重复注册会覆盖（`LifecyclePlugins` 以 id 为键）。
- **魔法路径/文件名就挂在这里**（§5）。参考实现：
  `PicGo/src/main/apis/app/uploader/index.ts` 的 `renameFn`。

### 3.5 图床多配置（`picgo.uploaderConfig`）

实测 `src/types/index.ts` + `src/lib/UploaderConfigManager.ts`：

```ts
listUploaderTypes(): string[]                              // 返回 helper.uploader.getIdList()
getConfigList(type): IUploaderConfigItem[]                 // 该类型下全部配置
getActiveConfig(type): IUploaderConfigItem | undefined     // 当前激活（defaultId 命中，否则取首个）
use(type, configName?): IUploaderConfigItem                // 激活；configName 不存在会 throw
createOrUpdate(type, configName?, patch?): IUploaderConfigItem
copy(type, configName, newConfigName): IUploaderConfigItem
rename(type, oldName, newName): IUploaderConfigItem
remove(type, configName): void
```

`IUploaderConfigItem`：`{ _id, _configName, _createdAt, _updatedAt, ...业务字段 }`
（**picgo 定义，字段名原样**）。

**四个必须知道的实测行为**（直接影响 D22 的同步流程）：

| # | 行为 | 影响 |
|---|---|---|
| 1 | **`createOrUpdate` / `use` 会顺带切换当前上传器** —— 内部调 `persistTypeAndMirror(..., { setCurrent: true })`，写入 `picBed.current` 与 `picBed.uploader` | 批量同步多条配置后，当前上传器会被**最后一条**覆盖 → 必须显式重新激活 `IsDefault` 那条 |
| 2 | 构造时 `init()` 自动做 legacy 迁移：把老的 `picBed.<type>` 升级为 `uploader.<type>.configList[] + defaultId` | 首次接管已有 `config.json` 时无需手工迁移 |
| 3 | `use()` 在 `configList` 为空时**不报错**，而是创建一个「只有元数据」的配置并打 warn | 前端应引导管理员先填配置 |
| 4 | `remove()` 删掉最后一条时会同时清掉 `picBed.<type>`，并 warn「当前正在使用该类型」 | agent 需要感知并回退默认 |

**类型不存在时**：`assertUploaderTypeExists()` 抛 `Type ${type} not found`；
`use()` 找不到配置名抛 `Config ${configName} not found in type ${type}`。
agent 需把这两类错误映射为业务错误码（而非 500）。

> GUI 早期版本在 `PicGo/src/main/utils/handleUploaderConfig.ts` 里手写了一套等价逻辑
> （直接操作 `uploader.<type>.configList`）。**agent 一律用上面的官方 API，不复制那套。**

### 3.6 插件元信息与启停

```ts
picgo.pluginLoader.getFullList(): string[]                    // 全部（含被禁用）
picgo.pluginLoader.getList(): string[]                        // 仅启用
picgo.pluginLoader.getPlugin(name): IPicGoPluginInterface | undefined
picgo.pluginLoader.hasPlugin(name): boolean
```

**启停机制（实测 `registerPlugin()`）** —— 开关就是 config 里的 `picgoPlugins.<name>`：

```ts
if (ctx.getConfig(`picgoPlugins.${name}`) === true ||
    ctx.getConfig(`picgoPlugins.${name}`) === undefined) {   // ← undefined 视为启用
  this.list.push(name)
  this.getPlugin(name)!.register(this.ctx)
  ctx.saveConfig({ [`picgoPlugins[${name}]`]: true })        // ← 加载后会回写 true
}
```

| 结论 | 说明 |
|---|---|
| 禁用插件 | 写 `picgoPlugins.<name> = false`，然后**重启 agent**（`require` 缓存不会自己失效） |
| 启用插件 | 写 `true`（或删除该键），重启 agent |
| 加载失败 | `registerPlugin` 内部 catch：回滚 `pluginMap`/`list`/`fullList`，打 error 日志，并 emit `notification` |
| 卸载 | `unregisterPlugin(name)` 会从六个 helper 容器 + `cmd` 全部 unregister，并 `removeConfig('picgoPlugins', name)` |

**`IPicGoPluginInterface` 可读字段**（用于列表展示与能力判断）：

```ts
{ register: (ctx) => void,
  config?: (ctx) => IPluginConfig[],
  uploader?: string,        // 该插件注册的 uploader 名
  transformer?: string,
  guiMenu?: (ctx) => IGuiMenuItem[],   // ← Electron 专属
  commands?: (ctx) => ICommandItem[],  // ← Electron 专属
  [prop: string]: any }
```

**版本/描述/作者/首页**：读 `<baseDir>/node_modules/<name>/package.json`
（GUI 的 `picgoCoreIPC.ts:70-80` 就是这么做的）。读取失败要降级为「未知」而不是报错。

**`GuiOnly` 判定**：`guiMenu` 或 `commands` 存在即为 `true`，落到 **`Plugins.GuiOnly`**
（DATA-MODEL §8.1），前端置灰并提示「该能力仅在桌面端可用」。

### 3.7 插件安装 / 卸载 / 更新

```ts
install(plugins: string[], options: IPluginHandlerOptions, env?: IProcessEnv)
update (plugins: string[], options: IPluginHandlerOptions, env?: IProcessEnv)
uninstall(plugins: string[], env?: IProcessEnv)            // ← 注意：不接受 options

interface IPluginHandlerOptions { npmProxy?: string; npmRegistry?: string }
// 返回 IPluginHandlerResult<boolean> = { success: boolean, body: string[] | string }
//   成功：body 是处理后的包名数组
//   失败：body 是错误信息字符串
```

> ⚠️ **`uninstall` 没有 `options` 参数**（实测 `src/types/index.ts`）。
> npm 源/代理只在 install / update 时能指定。

`pluginName` 支持四种写法（实测 `PicGoUtils.getPluginNameType` / `handleStreamlinePluginName`）：
完整名 `picgo-plugin-x`、短名 `x`、scope `@s/picgo-plugin-y`、本地路径 `./p`。

**安装后如何生效**：`pluginHandler` 只负责 npm 层面的增删，**不做热加载**。
实测 `getPlugin()` 用裸 `require()`，无缓存清理手段，因此：

> **安装/卸载/更新成功后由 agent 主动重启自身进程**，换取状态绝对干净。
> 低频操作，代价可接受（§9.5）。Go 侧负责探测并重新连接。

### 3.8 插件配置表单 schema —— **Web 端渲染的关键**

插件/内置 uploader 通过 `config(ctx) => IPluginConfig[]` 暴露表单描述：

```ts
interface IPluginConfig {
  name: string
  type: 'input' | 'password' | 'list' | 'checkbox' | 'confirm' | 'editor' | string
  required: boolean
  default?: any | ((answers: PluginConfigAnswers) => any)                 // ← 可能是函数
  choices?: IPluginConfigChoice[] | ((answers) => IPluginConfigChoice[])  // ← 可能是函数
  dependsOn?: string[]     // 依赖联动字段
  alias?: string
  message?: string
  [prop: string]: any
}
```

> 这个接口与它的字段名是 **picgo / 插件定义的**，按 D81.3 一律**原样保留**
> （`name` / `type` / `required` / `default` / `choices` / `dependsOn` / `alias` / `message`），
> 不做大写转换。前端渲染器要按这套字段名消费。

实测内置驱动的 `config` 大量使用 **getter + i18n**，例如 `src/plugins/uploader/github.ts`：

```ts
{
  name: 'token',
  type: 'password',
  get alias () { return ctx.i18n.translate<ILocalesKey>('PICBED_GITHUB_TOKEN') },
  default: userConfig.token || '',   // ← 读的是 picBed.github 的历史值
  required: true
}
```

#### 三条必须遵守的规则

| # | 规则 | 原因 |
|---|---|---|
| 1 | **用 `evaluatePluginConfig(config, {}, { onError })` 在服务端求值成静态结构后再转发前端** | `default` / `choices` 可能是函数，前端**永不执行插件代码**（安全 + 不能跑） |
| 2 | **求值时用 schema-only context 隐藏该 uploader 自身已有配置** | 插件会读 `picBed.<type>` 覆盖 `default`（见上面 github 的 `default: userConfig.token \|\| ''`），不隐藏就会把「schema 默认值」污染成「历史值」，前端无法区分 |
| 3 | **`dependsOn` 联动通过回调 agent 重新求值**，不在前端跑插件函数 | 接口 `POST /api/web/v1/storage/uploaders/schema`，请求体 `{ Type, Answers }`（API.md §3） |

**规则 2 的实现参考**：GUI 的 `PicGo/src/main/utils/schemaOnlyUploaderContext.ts`
（用 `Proxy` 拦截 `getConfig`，对 `picBed.<uploader>` / `uploader.<uploader>` 及其子路径返回 `undefined`，
对 `picBed` / `uploader` 整体读取时删掉该键）。
**agent 不要 import 它**（§4），照语义在 agent 内重写这几十行。

**规则 1 的落地形态**：

```ts
// picgo-agent/src/picgo/uploaders.ts
import { PicGo, evaluatePluginConfig } from 'picgo'

export function resolveUploaderSchema (picgo: PicGo, type: string, answers = {}) {
  const uploader = picgo.helper.uploader.get(type)
  if (!uploader) throw new Error(`Uploader ${type} not found`)
  if (!uploader.config) return { Name: uploader.name ?? type, Config: [] }

  const ctx = createSchemaOnlyUploaderContext(picgo, type)   // 隐藏自身历史配置
  const raw = uploader.config(ctx)
  const config = evaluatePluginConfig(raw, answers, {
    onError: (fieldName, kind, error) => {
      picgo.log.warn(`[plugin-config] ${fieldName}.${kind} threw: ${String(error)}`)
    }
  })
  // 包络层用我们自己的 PascalCase 字段名；Config 内部的 IPluginConfig 字段名原样透传
  return { Name: uploader.name ?? type, Config: config }
}
```

> **收益**：内置图床（smms / tcyun / github / qiniu / aliyun / imgur / upyun）走的是**同一套流程**，
> 所以前端**只实现一个通用表单渲染器**，无需为每个图床硬编码字段。

**内置驱动的配置字段（实测）** —— 同时是 §5 能力推断的输入：

| 驱动 | 配置字段 |
|---|---|
| `github` | `repo` `branch` `token` `path` `customUrl` |
| `aliyun` | `accessKeyId` `accessKeySecret` `bucket` `area` `path` `customUrl` `options` |
| `tcyun` | `version` `secretId` `secretKey` `bucket` `appId` `area` `endpoint` `path` `customUrl` `options` `slim` |
| `qiniu` | `accessKey` `secretKey` `bucket` `url` `area` `options` `path` |
| `upyun` | `bucket` `operator` `password` `url` `options` `path` |
| `smms` | `token` `backupDomain` |
| `imgur` | `clientId` `proxy` |

→ **5 个内置驱动有 `path` 字段**（github / aliyun / tcyun / qiniu / upyun），
smms / imgur **没有**。这就是 D44「魔法路径看驱动」的具体形态。

### 3.9 日志 与 内置 HTTP 服务

**日志**：`Logger` 写入 `settings.logPath` 或 **`<baseDir>/picgo.log`**（实测 `src/lib/Logger.ts`），
同时输出到 stdout。支持 `log.createLogger({ logPath, consoleOutput, respectSilent })` 派生 logger。

- agent 的 `GET /api/logs?Tail=N` 就是读这个文件（只读，不解析）。
- 上传生命周期会写 `picgo.log`，因此**用户可读的失败原因在那里也有一份**（补充 §3.2 的事件监听）。

**内置 HTTP 服务**：`picgo.server`（`ServerManager`，Hono 实现），
提供 `registerGet/Post/Put/Delete`、`registerMiddleware`、`registerStatic`、`mount`、
`listen(port, host, ignoreExistingExternalServer, secret)`、`shutdown()`、`isListening()`。

- 它自带 `POST /upload`（multipart / JSON `{list}` / 空 body 走剪贴板）、`/heartbeat`、`/auth/callback`。
- 实测其 `POST /upload` **只认 `{ list: string[] }`**，**不接受任何 uploader / 图床参数** ——
  这正是「core 无法按次指定目标」的直接证据（§8 的 P1 解决的正是它）。
- 第三方插件可经 `mount()` 注册自己的路由。
- **本项目默认关闭**（端口与 Go 服务冲突问题见 D6）；如将来要暴露「插件自带管理页」，
  可开启 + 反向代理（非当前范围）。

---

## 4. 明确排除

| 能力 | 为什么不用 |
|---|---|
| `picgo.cloud`（PicGo Cloud 商业相册） | 与本项目自建图库重复，且依赖第三方商业服务（D63） |
| `picgo.cmd`（Commander / inquirer 交互） | 交互式 prompt 不适合服务端 |
| `picgo.cloud.login/logout` 的浏览器流程 | 服务端无浏览器，且登录态由本项目自己的鉴权体系管理 |
| Electron 专属能力（`guiMenu` / `commands` / 托盘 / 快捷键） | 仅桌面端有意义；插件元信息里标 `GuiOnly` 让前端置灰 |
| `ConfigSyncManager` / `ConfigMerger`（config 云同步） | 本项目 config 由 DB 投影（D22），不需要第二套同步机制 |
| `picgo.server` 的默认路由 | 默认关闭；避免与 Go 的端口/鉴权体系重叠 |
| **从 `PicGo`(GUI) 仓库 import 任何模块** | GUI 模块 `import 'electron'`，在纯 Node 里会直接崩 |

> 最后一条的替代方案：需要 GUI 里某段**纯逻辑**时（如插件元信息拼装、
> `schemaOnlyUploaderContext` 的语义），**在 agent 里重写那小段**（几十行），不引依赖。

---

## 5. 魔法路径 / 魔法文件名

对应 D42–D44、D70：**图片命名完全由本项目决定**，且**模板按每个存储配置独立设置**
（落到 `StorageConfigs.PathTemplate` / `StorageConfigs.FileTemplate`）。

### 5.1 机制（实测）

生命周期钩子是唯一入口。参考 GUI 的真实实现
（`PicGo/src/main/apis/app/uploader/index.ts`）：

```ts
picgo.helper.beforeUploadPlugins.register('renameFn', {
  handle: async (ctx: IPicGo) => {
    await Promise.all(ctx.output.map(async (item) => {
      item.fileName = '20260214093015123.png'   // ← 直接改 fileName，驱动会用它
    }))
  }
})
```

在本项目里，把变量替换成模板渲染结果即可：

```ts
picgo.helper.beforeUploadPlugins.register('picgo-web-magic-name', {
  handle: (ctx) => {
    const tpl = plan.PathTemplate        // 来自 StorageConfigs.PathTemplate
    const ftpl = plan.FileTemplate       // 来自 StorageConfigs.FileTemplate
    for (const item of ctx.output) {
      const vars = buildVars(item, plan) // §5.3
      const name = render(ftpl, vars)    // 空模板 → 回退 {uniqid}{extname}
      const dir = render(tpl, vars)
      item.fileName = dir ? `${dir}/${name}` : name   // ← 见 5.2 的能力分支
    }
  }
})
```

> 必须在 `upload()` **之前**注册，且每个存储配置要用的模板要在发起该文件上传前设定
> （单文件 + 同步模型下天然满足，D39）。

### 5.2 三个维度的可行性（D44）

| 维度 | 通用性 | 说明 |
|---|---|---|
| **魔法文件名** | ✅ **完全通用** | 直接改 `item.fileName`，所有驱动生效（文件名是 `IImgInfo` 的公共字段） |
| **魔法路径** | ⚠️ **看驱动** | 多数驱动**没有**「按次传路径」的参数，它们的 `path` 是**配置态**（写在 `picBed.<type>.path`）。但驱动普遍按 `config.path + fileName` 拼接，因此**把路径塞进 `fileName` 就能生效，甚至能造子目录**（GitHub 即如此） |
| **不支持时降级** | — | 路径转成文件名前缀，如 `2026-02-14_a1b2c3d4.png` |

**能力推断（D77 要求：不硬编码驱动名列表）**：

```ts
// 探测逻辑（结果缓存进 StorageConfigs.Capabilities）
const fields = schemaConfig.map(f => f.name)              // 来自 §3.8 求值后的 schema
const PATH_FIELD_NAMES = ['path', 'root', 'basePath', 'prefix', 'dir', 'directory']
const pathFieldNames = fields.filter(f => PATH_FIELD_NAMES.includes(f))

capabilities.SupportsPathTemplate = pathFieldNames.length > 0
```

- 命中 → `SupportsPathTemplate = true`，魔法路径**直接塞进 `fileName`**
  （走驱动的 `config.path + fileName` 拼接）。
- 未命中（如 smms / imgur）→ `false`，**自动降级**为文件名前缀，并在存储配置界面上提示管理员。
- 探测结果落 **`StorageConfigs.Capabilities`**（DATA-MODEL §3.1），
  含 `PathFieldNames` / `DetectedAt` / `PicgoVersion`，供前端展示与排查。
  其中 `ConfigFields` 里列的驱动字段名（`repo` / `token` / `path` …）是 picgo/插件定义的，
  **原样保留**（DATA-MODEL §3.1 的显式例外）。

> **注意**：能力探测需要在**未保存配置**前也能跑（管理员新建配置时就要看到提示），
> 因为它是纯 schema 层推断，不依赖凭据。

### 5.3 模板变量（D70 常用集）

| 变量 | 含义 | 来源 |
|---|---|---|
| `{Y}` `{m}` `{d}` | 年（4 位）/ 月（2 位）/ 日（2 位） | 上传时刻 |
| `{H}` `{i}` `{s}` | 时 / 分 / 秒（2 位） | 上传时刻 |
| `{timestamp}` | Unix 秒级时间戳 | 上传时刻 |
| `{filename}` | 原始文件名（**不含**扩展名） | `Uploads.OriginalName` |
| `{md5}` / `{md5-8}` | 文件 MD5 全量 / 前 8 位 | 本地计算 |
| `{sha256-8}` | SHA-256 前 8 位 | 本地计算（落 `Uploads.SHA256`） |
| `{uid}` | 上传者用户 `UID` | 鉴权上下文 |
| `{uniqid}` | 随机串（短） | 本地生成 |
| `{extname}` | 扩展名（含点，如 `.png`） | `ctx.output[i].extname` |

**边界规则**：

| 情况 | 行为 |
|---|---|
| 模板为空字符串 | 回退默认：文件名 `{uniqid}{extname}`，路径为空 |
| 未知变量 `{xxx}` | **原样保留** + 记一条 warn，**不报错、不阻断上传** |
| 应用模板后出现非法路径字符 | `\ / : * ? " < > \|` 替换为 `_`（**注意：路径分隔符 `/` 要在替换之后再用于拼目录，顺序不能反**） |
| 结果为空或全为分隔符 | 回退默认命名 |

> **实现提醒**：路径分隔符的处理顺序容易写错。建议流程：
> 1. 渲染模板 → 2. 对**每一段**单独做非法字符替换（段内不含 `/`）→ 3. 用 `/` 拼回。
> 这样既允许模板表达子目录，又不会让文件名里混入路径穿越（`..`）。

---

## 6. config.json 的边界（D22）

### 6.1 纠正一个早期错误

> ⚠️ **早期设计认为「`config.json` 是派生文件，可整体删除后由 DB 重建」—— 这是错的。**

**实测证据**：`picgo-plugin-github-plus@1.2.3` 的 `dist/index.js` 会往 picgo config 写入自己的**私有状态**：

```js
// 图片账本（用于它自己的「同步删除」与列表）
const uploaded = (ctx.getConfig('uploaded') || []).filter(each => each.type !== UploaderName)
uploaded.unshift(...imgList)
ctx.saveConfig({ uploaded, [PluginName]: { lastSync: getNow() } })

// onRemove 失败回滚时也写回
self.saveConfig({ uploaded, [PluginName]: { lastSync: getNow() } })
```

**整体重建会直接抹掉插件的账本与同步游标**，导致插件行为异常。

### 6.2 规则：键级合并

| 受管键（我们可覆盖） | 说明 |
|---|---|
| `picBed.*` | 当前上传器 / transformer / 各驱动的镜像配置（由 `uploaderConfig` 维护） |
| `uploader.*` | 同类型多配置的 `configList` + `defaultId`（D64/D65 的落点） |
| `picgoPlugins.*` | 插件启停（§3.6） |
| `settings.*` | transformer / 日志等级 / proxy / npm 源等运行时设置 |

| 非受管键（**一律不动**） | 例 |
|---|---|
| 插件私有键 | `uploaded`、`<PluginName>.lastSync`、以及任何插件自定义键 |
| picgo 自维护键 | `debug`、`silent` 等 |

> `PICGO_ENV` 之类的「GUI 运行时标记」**不在受管清单内**，agent 不要写入。
> 实测 picgo-core 源码中**没有任何** `PICGO_ENV` 的消费逻辑，写入它只会污染 config。

### 6.3 启动 reconcile 流程（幂等）

```
Go 启动（或调用 POST /api/web/v1/picgo/resync）
  │
  ├─ 1. 读 DB：StorageConfigs WHERE Enabled = true ORDER BY UpdatedAt ASC
  │
  ├─ 2. 逐条推给 agent：POST /api/uploaders/configs
  │       { Type, ConfigName, Config, Activate: false }
  │     agent 侧转成：picgo.uploaderConfig.createOrUpdate(Type, ConfigName, Config)
  │
  ├─ 3. 最后一步：激活 IsDefault 那条
  │       POST /api/uploader/use { Type, ConfigName }
  │     agent 侧转成：picgo.uploaderConfig.use(Type, ConfigName)
  │
  └─ 4. 同步 picgoPlugins.* 与 settings.*（键级合并，非受管键不动）
```

> 其中 `Config` 的内部键（驱动的配置字段，如 `repo` / `token`）是 picgo/插件定义的，
> **原样透传**；`Type` / `ConfigName` 等封套字段才是我们自己的命名（D81）。

**为什么必须「最后显式激活」**：实测 `createOrUpdate` 内部是
`persistTypeAndMirror(type, store, active, { setCurrent: true, ... })`，
会顺手写 `picBed.current` / `picBed.uploader` = 当前 type。
**批量同步多条后，当前上传器会被最后一条覆盖** —— 所以第 3 步是必需的，不能省。

### 6.4 两个已知坑

| 坑 | 表现 | 处置 |
|---|---|---|
| **写坏插件私有键** | 插件账本/游标被抹 → 插件行为异常、删除同步失效 | 只写受管键（§6.2）；`PUT /api/web/v1/picgo/config`（整体覆盖）标记为**不推荐**并在 UI 上警示 |
| **同步顺序导致当前上传器漂移** | 期望 GitHub，实际变成 WebDAV | 严格按「先全部 createOrUpdate（`Activate: false`）→ 最后 use（IsDefault）」的顺序执行（§6.3） |

**并发安全**：reconcile 必须**全局串行**（一把 Go 侧互斥锁）。
`createOrUpdate` 是「读改写」`config.json`，并发调用会丢更新。

---

## 7. 远端删除（D47）

### 7.1 core 没有删除 API（实测）

在 `picgo-core@3.0.2` 全量产物中检索，以下标识符**出现次数均为 0**：
`deleteFile` / `removeFile` / `deleteImage` / `deleteObject` / `removeObject` /
`removeImage` / `deleteRemote` / `deletePicture`。

现存的所有 `delete` / `remove` 都是无关语义（`uploaderConfig.remove`、`removeConfig`、
`removeAllListeners`、云相册 CLI 的 `cloud album delete`）。
→ **core 层没有任何「删除已上传对象」的能力。**

### 7.2 但存在事实约定（ecosystem convention）

| 位置 | 代码 |
|---|---|
| GUI 触发 | `PicGo/src/main/events/picgoCoreIPC.ts:158` → `picgo.emit('remove', files, GuiApi.getInstance())` |
| 插件监听 | `picgo-plugin-github-plus@1.2.3` `dist/index.js:253` → `ctx.on('remove', onRemove)` |

插件侧实现（同文件 `dist/index.js:152`）：

```js
async function onRemove (files, { showNotification }) {
  const rms = files.filter(each => each.type === UploaderName)   // ← 按 type 过滤归属
  if (rms.length === 0) return
  const ins = initOcto(this)
  const fail = []
  for (const each of rms) {
    await ins.removeFile(each).catch(e => { this.log.error(e); fail.push(each) })
  }
  if (fail.length) { /* 把失败的写回 uploaded 账本 */ }
  notic(showNotification, '删除提示', fail.length === 0
    ? '成功同步删除' : `删除失败${fail.length}个`)              // ← 唯一的成功/失败信号
}
```

底层调用（`dist/lib/octokit.js:131`）：

```js
removeFile (img) {
  const { repo, path, owner, branch } = this
  return this.octokit.repos.deleteFile({
    repo, owner, branch,
    path: url_join(path, img.fileName),
    message: `Deleted ${img.fileName} by PicGo - ${getNow()}`,
    sha: img.sha                                   // ← 关键字段
  })
}
```

### 7.3 三道坎

| # | 坎 | 实测依据 | 应对 |
|---|---|---|---|
| **1** | **需要上传时回写、事后可取的额外字段** | GitHub 的 `deleteFile` 需要 `img.fileName` **+ `img.sha`**；插件在上传时回写 `img.sha = sha`（`github-plus/dist/index.js:127`） | **必须持久化完整返回值到 `UploadResults.RawOutput`（JSON）**。只存 URL 的话 `sha` 丢失后**永远删不掉**（DATA-MODEL §4.2） |
| **2** | **`guiApi` 是 Electron 专属** | 插件签名 `onRemove(files, { showNotification })`，会解构 GUI 提供的 API | agent 造一个 **shim**，至少提供 `showNotification` |
| **3** | **`emit` 无返回值、插件是 async 且无人 await** | `EventEmitter.emit` 同步返回 boolean；`onRemove` 是 async 但 `ctx.on` 不会 await | **从 shim 捕获 `showNotification` 的文案反推结果**，配合超时 |

### 7.4 agent 侧实现

**端点**：`POST /api/delete`（API.md §13）

```jsonc
// 请求
{ "Uploader": { "Type": "github", "ConfigName": "work" },
  "Item": { /* UploadResults.RawOutput 中的完整 IImgInfo，含 sha */ } }

// 响应
{ "Code": "OK", "Message": "ok", "Data": { "Deleted": true, "Message": "成功同步删除" } }
```

**实现要点（伪码）**：

```ts
async function deleteRemote (picgo: PicGo, type: string, item: IImgInfo) {
  const outcome: { message?: string } = {}
  const guiApiShim = {
    showNotification: (n: { title?: string; body?: string }) => {
      outcome.message = `${n.title ?? ''} ${n.body ?? ''}`.trim()   // 捞取结果信号
    },
    // 其余 GUI API 一律给安全空实现，避免插件解构时报错
    copyToClipboard: () => {},
    openUrl: async () => {}
  }

  const hasListener = picgo.listenerCount('remove') > 0
  if (!hasListener) return { Deleted: false, Reason: 'DRIVER_UNSUPPORTED' }

  picgo.emit('remove', [{ ...item, type }], guiApiShim)   // 不 await（emit 无返回值）

  await waitFor(() => outcome.message !== undefined, DELETE_TIMEOUT_MS)
  // 「删除失败 N 个」/「删除失败」→ 失败；否则视为成功
  const failed = !outcome.message || /失败/.test(outcome.message)
  return failed
    ? { Deleted: false, Reason: 'REMOTE_DELETE_FAILED', Message: outcome.message }
    : { Deleted: true, Message: outcome.message }
}
```

> **判定文案的脆弱性**：插件文案是中文且可能随版本变化（`notic()` 的第 3 个参数）。
> 因此：
> - 判定逻辑要**宽松**（拿不到文案 → 视为失败，不误报成功）；
> - 删除失败**不回滚本地删除**（D46/D72：配额照退），只把结果记进
>   `OperationLogs`（`Type = image.delete`）的 `Detail`
>   （`{ RemoteDeleted: false, RemoteError: "..." }`），让用户可追溯。

### 7.5 能力标志与降级

| 驱动情况 | `Capabilities.SupportsRemoteDelete` | 行为 |
|---|---|---|
| 插件实现了 `remove` | `true` | 真删远端文件 |
| 插件没实现 `remove` | `false` | **只删本地记录**；前端标记「该驱动不支持远端删除」（D47） |

**探测方式（不硬编码驱动名，D77）**：`remove` 监听器是**插件级**（不是 uploader 级），
所以只能做**粗粒度**探测 —— 检查是否存在 `remove` 监听器：

```ts
const supports = picgo.listenerCount('remove') > 0   // 粗粒度：至少有一个插件关心删除
```

- 一个插件可能注册了监听但只处理自己 `UploaderName` 的项（github-plus 就是这样），
  所以 `SupportsRemoteDelete = true` **不保证**该驱动的每个项都能删。
- agent 在**应用层做二次过滤**：把 `Item.Type` 与插件的 `uploader` 名比对，
  不匹配时直接返回「不支持」（避免把别人的项发给它）。
- 拒绝误伤：**不要**为了探测而 `emit('remove', [])`（那会真的通知到插件）。
  探测只读 `listenerCount`，不触发事件。

---

## 8. 补丁清单（`PicGo-Web` 分支 —— **已落地并验证**）

### 8.1 fork 事实（D48–D51）

| 项 | 值 |
|---|---|
| 仓库 | `Github-me:YeqingKy/PicGo-Core` |
| 本地路径 | `PicGo/PicGo-Core`（与 `PicGo-Web` 同级） |
| 分支 | **`PicGo-Web`** |
| 基线 | 上游 `dev` @ **v3.0.2**（`f710083`） |
| 补丁提交 | **`6419c2f`** —— `feat(upload): per-upload uploader target + per-context config overrides` |
| 改动规模 | **6 个文件，+687 / −16** |
| 上游 | `https://github.com/PicGo/PicGo-Core`，`dev` |
| 策略 | **自维护，不提交上游**（D48）；上游更新时以 `dev` 为基线 rebase（D51） |

**改动文件清单**：

| 文件 | 类型 |
|---|---|
| `src/types/index.ts` | 修改（+36） |
| `src/utils/createContext.ts` | 修改（+114） |
| `src/core/Lifecycle.ts` | 修改（+79） |
| `src/__tests__/unit/uploadTargetOverride.spec.ts` | **新增**（+327，13 用例） |
| `FORK-NOTES.md` | **新增**（+145，改动说明与上游合并指引） |
| `pnpm-workspace.yaml` | **新增**（+2，pnpm 12 构建审批，见 §8.6） |

### 8.2 为什么需要补丁 —— 根因

实测 `src/core/Lifecycle.ts`：`start()` 第一件事就是 `createContext(this.ctx)`，
看似隔离，但实测 `src/utils/createContext.ts` 里**关键方法都是 bind 到根实例的**：

```ts
getConfig: ctx.getConfig.bind(ctx),     // ← 读根实例共享的 _config
setConfig: ctx.setConfig.bind(ctx),
saveConfig: ctx.saveConfig.bind(ctx),
emit: ctx.emit.bind(ctx),               // ← 共享同一个 EventEmitter
on: ctx.on.bind(ctx),
```

所以（补丁前）：

| 项 | 并发下是否安全 | 说明 |
|---|---|---|
| `ctx.output` / `ctx.input` | ✅ 安全 | `createContext` 里是 `output: []`、`input: []` 新数组 |
| **`getConfig('picBed.uploader')`** | ❌ **不安全** | 读的是根实例的**同一份 `_config`**。A 想传 GitHub、B 想传 SMSS，`setConfig` 会互相覆盖 |
| **事件（`finished`/`failed`/`uploadProgress`）** | ❌ **不安全** | 共享 EventEmitter，无法归属 |
| **`Lifecycle.step`** | ❌ **不安全** | 是**实例字段**（`private step: LifecycleStep = IDLE`），被 `start()` 读写，并发下串扰 |

**P1 + P2 给每个 context 一份独立的配置覆盖表**；**P3 修 `step`**；**P4 让事件可归属**。

### 8.3 四处改动

#### P1 — `UploadOptions.uploader` 与 `contextData`（`src/types/index.ts`）

```ts
export interface IUploaderTarget {
  type: string
  /** 配置名（大小写不敏感）。省略 → 用该图床当前激活配置 */
  configName?: string
}

export interface UploadOptions {
  outputFormat?: OutputFormat
  /** 覆盖本次上传的图床；只影响这一次 upload()，不写盘、不影响其他并发上传 */
  uploader?: IUploaderTarget
  /** 附加元数据，回调里从 ctx.contextData 取回，用于把全局事件归属到具体任务 */
  contextData?: Record<string, unknown>
}

export interface IPicGo extends NodeJS.EventEmitter {
  // ...
  /** 由 UploadOptions.contextData 提供；未传时为空对象 */
  contextData?: Record<string, unknown>
}
```

补丁前 `UploadOptions` **只有 `outputFormat`**，所以这是纯新增字段 —— 不传时行为完全不变。

#### P2 — per-context 配置覆盖（`src/utils/createContext.ts`）

用模块级 `WeakMap` 保存每个上下文的覆盖表，并导出 `getContextOverrides(ctx)` 供 `Lifecycle` 使用：

```ts
const contextOverrides = new WeakMap<IPicGo, Record<string, unknown>>()

export const getContextOverrides = (ctx: IPicGo): Record<string, unknown> | undefined =>
  contextOverrides.get(ctx)

export const createContext = (ctx: IPicGo, options?: UploadOptions): IPicGo => {
  const overrides: Record<string, unknown> = {}

  const getConfig = <T>(name?: string): T => {
    const paths = Object.keys(overrides)
    if (paths.length === 0) return ctx.getConfig<T>(name)          // ① 无覆盖 → 透传
    if (name === undefined) {
      return applyOverrides(ctx.getConfig<unknown>(), overrides) as T   // ② 全量读取 → 合并
    }
    if (Object.prototype.hasOwnProperty.call(overrides, name)) {  // ③ 精确命中
      return overrides[name] as T
    }
    for (const path of paths) {                                    // ④ 后代命中
      if (name.startsWith(`${path}.`)) {
        return get(overrides[path], name.slice(path.length + 1)) as T
      }
    }
    const subOverrides: Record<string, unknown> = {}               // ⑤ 祖先命中
    for (const path of paths) {
      if (path.startsWith(`${name}.`)) {
        subOverrides[path.slice(name.length + 1)] = overrides[path]
      }
    }
    if (Object.keys(subOverrides).length > 0) {
      return applyOverrides(ctx.getConfig<unknown>(name), subOverrides) as T
    }
    return ctx.getConfig<T>(name)                                  // ⑥ 兜底
  }

  const context: IPicGo = {
    // ...其余字段原样保留（含 output: []、input: []）...
    getConfig,
    contextData: options?.contextData ?? {},
    // ...其余 bind 原样...
  }
  contextOverrides.set(context, overrides)
  return context
}
```

`applyOverrides` 的关键性质：**只克隆覆盖路径沿途的分支**，绝不修改根实例的共享对象
（单测 `never mutates the root config` 断言了这一点）。

**五条解析分支缺一不可**：内置 uploader 与第三方插件普遍读 `picBed.<type>.<field>`
（→ ④）或整个 `picBed` 对象（→ ⑤）。

#### P3 — 应用覆盖 + 修并发串扰（`src/core/Lifecycle.ts`）

**3a. 新增 `applyUploaderOverride(ctx, options)`**，把 `options.uploader` 解析后写入覆盖表：

```ts
const applyUploaderOverride = (ctx: IPicGo, options?: UploadOptions): void => {
  const target = options?.uploader
  if (!target || !target.type) return

  const overrides = getContextOverrides(ctx)
  if (!overrides) return

  if (!ctx.uploaderConfig.listUploaderTypes().includes(target.type)) {
    throw new Error(`Uploader type "${target.type}" not found`)
  }

  const configName = typeof target.configName === 'string' ? target.configName.trim() : ''
  let item: IUploaderConfigItem | undefined

  if (configName) {
    const wanted = configName.toLowerCase()
    item = ctx.uploaderConfig
      .getConfigList(target.type)
      .find(config => String(config._configName).trim().toLowerCase() === wanted)
    if (!item) {
      throw new Error(`Uploader config "${target.configName}" not found for type "${target.type}"`)
    }
  } else {
    item = ctx.uploaderConfig.getActiveConfig(target.type)
    if (!item) {
      throw new Error(`No config found for uploader type "${target.type}". Configure it before uploading.`)
    }
  }

  overrides['picBed.uploader'] = target.type
  overrides['picBed.current'] = target.type
  overrides[`picBed.${target.type}`] = item
}
```

**调用位置（关键）**：在 `try` **之前**，紧跟 `createContext` 之后：

```ts
async start (input: any[], options?: UploadOptions): Promise<IPicGo> {
  const ctx = createContext(this.ctx, options)

  // 在进入生命周期之前校验目标：非法目标是调用方错误，必须以 rejection 抛出，
  // 而不是被下面的上传错误处理吞成一个空数组。
  applyUploaderOverride(ctx, options)

  let step: LifecycleStep = LifecycleStep.IDLE   // ← 见 3b
  try { /* 生命周期 */ } catch (e) { /* ... */ }
}
```

**为什么这是「插件零适配」的关键**：实测 `Lifecycle.doUpload()` 读的是
`ctx.getConfig('picBed.uploader')`，内置驱动（如 `github.ts`）读的是 `ctx.getConfig('picBed.github')`。
由于这两处都走 `ctx.getConfig`，**覆盖表自动生效，插件一行都不用改**。

**3b. `private step: LifecycleStep` → 局部变量**

实测 `step`（`src/core/Lifecycle.ts`）的**全部**读写点都在 `start()` 内部，
**没有任何跨调用的状态依赖**。改成局部变量**语义完全等价**，但让 `Lifecycle` 并发安全：

```ts
private readonly ctx: IPicGo
// private step: LifecycleStep = IDLE   ← 删掉

async start (...) {
  let step: LifecycleStep = LifecycleStep.IDLE   // ← 局部变量
  ...
}
```

不修的话：A 在 UPLOAD 阶段失败时，`this.step` 可能已被 B 覆盖，
导致「是否补跑 `afterUpload`」的判断出错。

#### P4 — `contextData`（事件归属）

由 P1 的类型 + P2 的 `contextData: options?.contextData ?? {}` 组成，agent 就能：

```ts
picgo.once('finished', (ctx) => resolve({ kind: 'finished', jobUid: ctx.contextData?.jobUid }))
picgo.once('failed',   (err) => resolve({ kind: 'failed',   jobUid: currentJobUid, err }))
```

**纯增量、不改任何现有事件形状**，对插件完全透明。

### 8.4 实测证据

**并发隔离（核心验证）** —— 两个命名配置（`work` / `personal`，指向不同 bucket）并发上传：

```
改动前：两个并发请求实际落到的桶: /personal-bucket , /personal-bucket   ❌ 串了
改动后：图床实际收到: [{"bucket":"/work-bucket","fileName":"work.png"},
                       {"bucket":"/personal-bucket","fileName":"home.png"}]  ✅ 隔离
        返回值 URL 也各自正确
```

**其它已验证项**：

| 验证项 | 结果 |
|---|---|
| 不带 `options.uploader` 时行为不变 | ✅ 走全局 config 路径 |
| `setConfig` 不污染 `config.json` | ✅ 只改内存（文件里仍是旧值） |
| 覆盖表不修改根实例 config | ✅ 单测断言 |
| `configName` 大小写不敏感（`PERSONAL` 命中 `personal`） | ✅ |
| 省略 `configName` → 用激活配置 | ✅ |
| 非法 `configName` → **抛出**且消息明确 | ✅ `Uploader config "nope" not found for type "mock"` |
| 非法 `type` → **抛出**且消息明确 | ✅ `Uploader type "nosuchtype" not found` |
| 并发下用 `contextData` 归属事件 | ✅ 两个批次的事件各归各家（`{work.png: A, home.png: B}`） |
| 端到端上传（本地 mock 图床） | ✅ 事件序列 `0→30→60→beforeUpload→afterUpload→100→finished` 完整；宽高探测正常 |
| 魔法文件名（`beforeUploadPlugins` 改 `fileName`） | ✅ 生效且落到图床 |
| 多配置 API（`createOrUpdate` / `getActiveConfig` / `use`） | ✅ 全通 |
| 全量单测回归 | ✅ **250 passed / 0 failed** |
| `pnpm lint`（`dpdm` + `tsc --noEmit` + `eslint`） | ✅ 通过，无循环依赖 |
| `pnpm build`（rollup 出 CJS + ESM） | ✅ 成功 |
| CLI（`picgo -v` / `get uploader --format json` / `get plugins --format json`） | ✅ 正常 |
| 内置 HTTP server（`POST /upload` 200、无 token 401） | ✅ 正常 |

### 8.5 补丁的回归测试

新增单测 `src/__tests__/unit/uploadTargetOverride.spec.ts`（**13 个用例**，2 个 describe 块）：

| describe | 用例 | 断言要点 |
|---|---|---|
| `createContext config overrides` | passes config reads through when nothing is overridden | 无覆盖时完全透传 |
| | resolves exact, descendant and ancestor reads against overrides | ③④⑤ 三条分支都覆盖 |
| | never mutates the root config | 根实例 config 不被改写 |
| | keeps overrides isolated between contexts | 两个上下文的覆盖互不干扰 |
| | exposes contextData on the context | `contextData` 默认 `{}`，传入时原样 |
| `Lifecycle per-upload uploader target` | uploads with the explicitly named config | 用指定配置上传，根 config 不变 |
| | accepts a case-insensitive config name | `PERSONAL` 命中 `personal` |
| | falls back to the active config when configName is omitted | 省略 → 激活配置 |
| | keeps the global config path working when no target is given | 不传 → 走全局 |
| | rejects unknown config names instead of silently uploading elsewhere | **必须 reject** |
| | rejects unknown uploader types | **必须 reject** |
| | isolates concurrent uploads targeting different configs | 并发各走各的 bucket |
| | attributes globally emitted events back to the caller via contextData | 事件按 `contextData` 归属 |

> 用 `pnpm test` 跑（项目用 vitest）。改动只在传 `uploader` 时生效，
> 因此**不传该参数的上游行为由既有测试覆盖**（全量 250 用例仍然绿）。

### 8.6 依赖方式与构建

| 阶段 | 方式 |
|---|---|
| **开发期** | `picgo-agent/package.json` 写 `"picgo": "file:../../PicGo-Core"`；Makefile 保证先 `cd PicGo-Core && pnpm install && pnpm build` |
| **构建/部署** | `cd PicGo-Core && pnpm build && pnpm pack` 产出 tarball，Docker 内用 `file:./vendor/picgo-3.0.2-custom.tgz` |

> ⚠️ **git 依赖（`github:...#PicGo-Web`）不可用**：实测 `PicGo-Core` 的
> `dist/` 在 `.gitignore` 中，且 `package.json` 的 `prepare` 是 `husky`（不是 `build`），
> 所以从 git 安装拿到的是**没有构建产物的仓库**，`require('picgo')` 会失败。
> **必须用 `file:` 或 tarball。**

> **`pnpm-workspace.yaml`（补丁新增）**：pnpm 12 默认拦截依赖的构建脚本，
> 不显式允许 `esbuild` 时 `pnpm install` 会以 `ERR_PNPM_IGNORED_BUILDS` 失败
> （`esbuild` 经 `vite`/`vitest` 传递依赖引入）。内容：
>
> ```yaml
> allowBuilds:
>   esbuild: true
> ```
>
> 这个文件与补丁一同提交，保证他人 clone 后 `pnpm install` 能直接跑通。

### 8.7 上游合并注意

rebase 到新的上游 `dev` 时，冲突面仅限这三个源文件：

| 文件 | 冲突风险 |
|---|---|
| `src/types/index.ts` | 低（纯新增接口与字段） |
| `src/utils/createContext.ts` | **中**（上游若增删上下文字段需手工对齐） |
| `src/core/Lifecycle.ts` | **中**（`start()` 若重构需重新插入 `applyUploaderOverride` 调用） |

**上游若原生支持「按次指定图床」，直接删除本分支改动，改用上游 API。**
落地时的详细改动说明与逐条合并提示见 fork 内 `FORK-NOTES.md`。

### 8.8 两条运行路径对比（业务代码同形）

| 问题 | 默认路径（`concurrency = 1`，不依赖补丁） | 并发路径（`> 1`，用补丁） |
|---|---|---|
| 切换驱动 | `picgo.setConfig({ 'picBed.uploader': type, 'picBed.current': type, ['picBed.' + type]: active })`，**只改内存不落盘** | `picgo.upload(paths, { uploader: { type, configName } })` |
| A→GitHub、B→WebDAV 并发 | 不存在（严格串行） | ✅ 各 ctx 独立覆盖 |
| 事件归属 | 全局事件天然无歧义 | ⚠️ 用 `contextData` 回传 `JobUID` |
| `upload()` 返回值 | ✅ 准确 | ✅ 准确 |
| `Lifecycle.step` | ✅ | ✅（P3 修复） |
| 插件适配成本 | — | ✅ **零** |

```ts
// agent 内部的单一分支（两路径同形，业务代码零改动）
if (concurrency > 1) {
  await picgo.upload([filePath], { uploader: { type, configName }, contextData: { jobUid } })
} else {
  picgo.setConfig({ 'picBed.uploader': type, 'picBed.current': type, [`picBed.${type}`]: active })
  await picgo.upload([filePath])
}
```

> **降级保护**：若 agent 检测到运行时加载的 `picgo` 不支持 `uploader` 选项
> （例如误用了未打补丁的上游包），应**自动回落到串行路径**并写一条日志，
> 而不是让所有上传失败。

---

## 9. agent 侧实现要点

### 9.1 实例与路径

```ts
// picgo-agent/src/picgo/instance.ts
import { PicGo, evaluatePluginConfig } from 'picgo'

let instance: PicGo | null = null

export function getPicgo (configPath: string): PicGo {
  if (!instance) {
    instance = new PicGo(configPath)          // configPath 由 env 注入
    registerMagicNameHook(instance)           // §5，必须在任何 upload() 之前
  }
  return instance
}
```

| 项 | 约定 |
|---|---|
| **单实例** | 全进程一个 `PicGo`（D8）。**不要为了并发做多实例池** —— P1+P2 更省资源，且避免 N 份插件实例化与 `node_modules` 并发写 |
| **configPath** | env `PICGO_AGENT_CONFIG_PATH`；默认 `<dataDir>/picgo/config.json`。**不污染用户主目录**（`baseDir` 要能装插件，§2.1） |
| **baseDir 内容** | `config.json` + `package.json`（插件清单）+ `node_modules/` + `picgo.log` |
| **运行时标记** | 不写 `PICGO_ENV` 等非受管键（§6.2） |

### 9.2 config 写前备份

每次落盘前把 `config.json` 复制为 `config.json.bak.<ts>`，保留最近 **5** 份（轮转删除最旧的）。
用于：插件写坏配置、误操作 `PUT /api/web/v1/picgo/config` 后的快速回滚。

> **这是 agent 的内部安全措施，不暴露给用户界面** —— D82 明确「不做备份/恢复功能」
> （无界面、无端点、无定时备份任务）。用户侧的备份由运维直接备份 `data/` 目录完成。

### 9.3 上传队列

| 项 | 约定 |
|---|---|
| 并发 | 默认 **1**（D35）。由 Go 侧的 `upload.concurrency` 通过 env 传给 agent |
| 粒度 | agent 的 `/api/upload` 是**单文件 + 同步**（D39）：一次请求 = 一个文件，阻塞到结束 |
| 队列位置 | **主队列在 Go**（`Jobs` / `JobItems`，D36）。agent 只保留一个极薄的信号量防重入 |
| 超时 | agent 侧也要有硬超时（略大于 `upload.itemTimeoutSeconds`），防止 `await upload()` 永久挂起（§3.2） |
| 具名参数 | 每次请求带 `Uploader.{Type,ConfigName}` 与 `JobUID`，转成 `upload()` 的 `uploader` + `contextData` |

> **注意**：Go 是队列的真相源；agent 重启后 Go 负责重新入队（D41 启动恢复）。
> agent 不要自己做持久化队列。

### 9.4 日志尾随

`GET /api/logs?Tail=N` 读 `<baseDir>/picgo.log`（`settings.logPath` 可覆盖，§3.9）。
插件安装/更新期间，agent 额外把 `npm` 的输出以 SSE `job.log` 推给 Go，
让「插件安装过程」在任务面板可见（**`JobLogs`** 表，DATA-MODEL §5.3）。

### 9.5 插件操作后重启进程

| 操作 | agent 行为 |
|---|---|
| `install` / `uninstall` / `update` 成功 | 把 job 结果写回 Go → **主动 `POST /api/shutdown` 或 `process.exit(0)`**，由 Go 重新拉起 |
| `PATCH /api/plugins/{name}`（启停） | 写 `picgoPlugins.<name>` → 同样重启 |
| 重启期间 | Go 对依赖 agent 的接口返回 `503 / 50002`；前端提示「正在重启内核」并轮询 `/healthz`（D7） |

理由：`getPlugin()` 用裸 `require()`，**没有缓存清理手段**（§2.1/§3.6）。
卸载也不彻底（`unregisterPlugin` 只清内存注册表，Node 模块缓存仍在）。
重启是唯一可靠且实现最简单的方案，且插件操作属低频行为。

### 9.6 错误映射（agent → Go）

| agent 侧错误 | HTTP | Go 错误码 |
|---|---|---|
| `Uploader type "X" not found`（补丁抛出） | **400** | `40001` |
| `Uploader config "Y" not found for type "X"`（补丁抛出） | **400** | `40001` |
| `Type ${type} not found` / `Config ${name} not found in type ${type}`（`uploaderConfig` 抛出） | 400 | `40001` |
| 上传失败（`failed` 事件 / 返回项无 `imgUrl`） | 200 + `ERR_PICGO` | `50003` |
| 插件操作失败（`pluginHandler` 返回 `success: false`） | 200 + `ERR_PICGO` | `50004` |
| 远端删除不支持 / 失败 | 200 + `ERR_PICGO` | 记 `OperationLogs`（`Type = image.delete`），**不报错给用户**（D72） |
| agent 未就绪 / 请求超时 | 503 | `50002` |

> **契约细节以 [`API.md`](./API.md) §13 为准**。agent 的封套与自有字段遵循 D81（PascalCase），
> 例如 `{ Code, Message, Data }`；而透传的 picgo 配置内部键（`picBed` / `_configName` 等）
> **保持原样**。

---

## 10. 当前状态对照表

| 需求 | picgo-core 是否支持 | 结论 | 备注 |
|---|---|---|---|
| 编程式上传 | ✅ `upload(input, options)` | 直接用 | 输入用文件路径数组 |
| **上传失败的可观测性** | ⚠️ **失败被吞掉**（默认不 reject） | **必须监听 `failed` 事件 + 校验 `imgUrl` + 设超时** | 见 §3.2；`debug` 为真值才 re-throw |
| 进度粒度 | ⚠️ 仅 `0/30/60/100/-1` 四档 | 单文件内插值；整体进度按 item 计数（D36） | 见 §3.3 |
| 并发下事件归属 | ❌ 共享 EventEmitter | 默认串行天然无歧义；并发用 P4 的 `contextData` | 见 §3.3、§8.3 |
| 多配置图床 | ✅ `picgo.uploaderConfig`（8 个方法） | 直接用 | `createOrUpdate` 会顺带切当前上传器，见 §3.5 |
| **按次指定上传目标** | ❌ core 无此参数（内置 `/upload` 只认 `{list}`） → ✅ **已由补丁提供** | 默认走 `setConfig` 串行；并发走 `uploader` 选项 | 见 §8（提交 `6419c2f`） |
| 插件列出 / 启停 | ✅ `pluginLoader` + `picgoPlugins.<name>` | 直接用 | `undefined` 视为启用；改后需重启 agent |
| 插件安装/卸载/更新 | ✅ `pluginHandler` | 直接用 | ⚠️ `uninstall` 不接受 npm 源/代理参数；成功后需重启 agent |
| 插件配置表单 schema | ✅ `config(ctx)` + `evaluatePluginConfig` | 直接用 | **服务端求值 + schema-only context + dependsOn 回调**，见 §3.8 |
| 日志读取 | ✅ `<baseDir>/picgo.log` | 直接用 | `settings.logPath` 可覆盖 |
| 魔法文件名 | ✅ `beforeUploadPlugins` 改 `fileName` | 直接用 | **完全通用**，见 §5 |
| 魔法路径 | ⚠️ **看驱动**（取决于是否有 `path` 类配置字段） | 能力探测 + 自动降级 | 5/7 内置驱动有 `path`；见 §5.2 |
| 远端删除 | ❌ core 无 API；⚠️ 靠 `remove` 事实约定 | 支持，插件有 `remove` 才生效 | 需 `UploadResults.RawOutput` + `guiApi` shim；见 §7 |
| `config.json` 管理 | ⚠️ 插件会写私有键 | **键级合并，不可整体重建** | 见 §6（修正早期错误） |
| 内置 HTTP 服务 | ✅ `picgo.server`（Hono） | 默认关闭 | 见 §3.9 |
| 配置备份/恢复 | ❌ 不做（D82） | agent 内部仅做轮转 `.bak`；用户侧由运维备份 `data/` | 无界面、无端点 |

**→ 按次指定图床已由 `PicGo-Web` 分支的补丁提供（`6419c2f`，13 个单测 + 端到端验证）；
默认 `concurrency = 1` 时不依赖补丁，业务代码两路径同形。**

---

## 11. 与决策的偏差

### 11.1 已修正的历史遗留（前 6 条）

编写早期版本时发现的问题，**均已回写修正**，此处保留记录以便追溯。

| # | 位置 | 问题 | 处理 |
|---|---|---|---|
| 1 | `DECISIONS.md` D65 | 表格写 `storage_configs.config`（AES 加密），但按 D78 凭据已拆到独立表 | ✅ 已改为 **`StorageSecrets.EncryptedPayload`** |
| 2 | `DECISIONS.md` D70 | `{sha256-8}` 注释为「去重键短形式」，但 D66 已取消去重 | ✅ 已改为「SHA-256 前 8 位（仅元数据参考；**不是去重键**）」 |
| 3 | `DECISIONS.md` D8 | 「串行队列（见 D24）」—— D24 实为密码登录 | ✅ 已改为 **D35** |
| 4 | 旧版本文件 | 曾称「`upload()` resolve 一个 Error 而不是 reject」 | ✅ 实测更精确：**路径上传失败时既不 reject 也不返回 Error，而是吞掉异常、返回（可能为空的）数组**；`Error` 只出现在类型签名里。本文 §3.2 已按实测描述 |
| 5 | 旧版本文件 | 曾写「上传串行化排队（p-limit，默认并发 3）」 | ✅ 已按 **D35（默认 1 并发）** 重写 |
| 6 | 旧版本文件 | 曾写「改 PicGo(GUI) fork + 提上游 PR 的最小侵入协议」 | ✅ 已按 **D48–D51（改 PicGo-Core、自维护不提上游）** 重写为补丁清单（§8） |

### 11.2 命名规范变更（D81）

**变更内容**：本轮起，我们自己的字段名统一改为 **PascalCase（大驼峰）**，缩写词全大写
（`UID` / `URL` / `ID` / `API`）。本文档已按此重写：

| 旧写法（snake_case） | 现写法（PascalCase） |
|---|---|
| `upload_results.raw_output` | **`UploadResults.RawOutput`** |
| `storage_configs.path_template` / `file_template` | **`StorageConfigs.PathTemplate`** / **`FileTemplate`** |
| `capabilities.supportsRemoteDelete` | **`Capabilities.SupportsRemoteDelete`** |
| `capabilities.supportsPathTemplate` | **`Capabilities.SupportsPathTemplate`** |
| `plugins.gui_only` | **`Plugins.GuiOnly`** |
| `uploads.original_name` / `uploads.sha256` | **`Uploads.OriginalName`** / **`SHA256`** |
| `job_logs` | **`JobLogs`** |
| `operation_logs` | **`OperationLogs`** |

**明确保持原样的例外**（D81.3，本文档严格遵守）：

| 例外 | 例 | 原因 |
|---|---|---|
| picgo 侧字段名 | `picBed` / `picgoPlugins` / `_id` / `_configName` / `_createdAt` / `_updatedAt` | picgo-core 定义 |
| `IImgInfo` 字段 | `fileName` / `imgUrl` / `extname` / `sha` / `width` / `height` | picgo-core 定义；`UploadResults.RawOutput` 里原样存（§7.3 坎 1） |
| `IPluginConfig` 字段 | `name` / `type` / `required` / `default` / `choices` / `dependsOn` / `alias` / `message` | 插件定义；前端表单渲染器按它消费（§3.8） |
| `IPluginHandlerOptions` 字段 | `npmProxy` / `npmRegistry` | picgo-core 定义（§3.7） |
| 驱动的配置字段名 | `repo` / `token` / `path` / `bucket` / `secretId` … | 插件定义（§3.8 表格、`Capabilities.ConfigFields`） |
| 环境变量 | `PICGO_WEB_*` / `PICGO_AGENT_CONFIG_PATH` | D81.3 第 2 条 |
| `settings` 配置键 | `settings.logPath` / `picgo.npmRegistry` / `upload.concurrency` | D81.3 第 3 条（KV 字符串 key，非列名） |
| Lsky 兼容层 | 路径 `/api/v1/**`、字段 `strategy_id` / `capacity` / `useCapacity`、封套 `{status, message, data}` | D81.3 第 1 条（外部冻结契约） |
| 操作日志 `Type` 取值 | `upload` / `image.delete` / `mail.send` / `user.create` … | 字符串枚举值，非列名 |

### 11.3 ✅ 已同步：本轮发现的其它文档不一致

本文档编写时发现的 6 处跨文档不一致，**已由文档负责人全部同步**：

| # | 位置 | 结果 |
|---|---|---|
| 1 | `DECISIONS.md` 多处 snake_case 表/列引用 | ✅ 已全部改为 PascalCase（D16 / D45 / D46 / D47 / D65 / D66 / D78 等） |
| 2 | `DECISIONS.md` D77.2「路径带 `/api/v1`」 | ✅ 已改为 `/api/web/v1` |
| 3 | `DATA-MODEL.md` §7.4 OAuth 回调地址 | ✅ 已改为 `/api/web/v1/auth/oauth/github/callback` |
| 4 | `DECISIONS.md` §十「待落补丁」标题 | ✅ 已改为「**已落补丁（已实现并验证，提交 `6419c2f`）**」，并补入实测证据 |
| 5 | `DECISIONS.md` D66 的 `uploads` / `sha256` 写法 | ✅ 已改为 `Uploads` / `SHA256` |
| 6 | 旧版 §11「仍待定」列 O11 为待定 | ✅ O11 已裁决为**不做**（D82） |

### 11.4 仍待定但**不阻塞开发**的项

- **O12 多语言**：不影响 picgo 集成。注意插件 schema 的 `alias` / `message` 由
  `ctx.i18n.translate` 提供，其语言由 **picgo 自己的 i18n** 决定（与前端 i18n 相互独立）。
  agent 在 `GET /api/uploaders` 时应把**已翻译后的字符串**直接透传给前端，**不做二次 i18n**。
- **O11 备份/恢复**：✅ 已裁决 **不做**（D82）—— 见 §9.2。
