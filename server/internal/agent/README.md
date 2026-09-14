# internal/agent

picgo-agent（Node 侧车）的 Go 客户端。

## 职责

```
Go 侧              本包                  picgo-agent（Node）
─────              ─────                 ─────────────────
存储配置真相源  →   投影到 agent 的配置   → 持有唯一的 PicGo 实例
上传队列/重试   →   单文件同步调用        → 执行一次真实上传
任务与审计      →   只消费契约            → 只维护执行期状态
```

- agent **仅监听 `127.0.0.1`**，所有请求带 `X-Agent-Token`
- 契约见 [`docs/API.md` §13](../../docs/API.md)

## 用法

```go
// 真实客户端（推荐：动态读取上传超时，使设置改动即时生效）
client := agent.New(agent.Config{
    BaseURL: cfg.AgentURL,      // http://127.0.0.1:36678
    Token:   cfg.AgentToken,
    Log:     log,
    UploadTimeoutFn: func() time.Duration {
        sec := settings.GetInt("upload.itemTimeoutSeconds", 300)
        return time.Duration(sec) * time.Second
    },
})

// 内存实现（PICGO_WEB_AGENT_MOCK=true；前端联调 / CI / 端到端自测）
client = agent.NewMock(agent.MockConfig{TempDir: cfg.PicgoConfigDir()})
```

`Client` 是 **interface**，因此上层（service）可以注入 mock 而不需要 Node 进程。

## 两个必须知道的语义

### 1. `upload()` 的失败有**两条**路径

| 情形 | agent 响应 | 本包返回的错误码 |
|---|---|---|
| 目标 `Type` / `ConfigName` 不存在 | HTTP 400 + `ERR_PARAM` | `CodeInvalidParam`（**不重试**） |
| 上传本身失败（网络 / 图床报错） | HTTP 200 + `ERR_PICGO` | `CodeUploadFailed`（**可重试**） |
| agent 连不上 / 超时 / 401 | — | `CodeAgentUnavailable`（**不重试**） |

`service.UploadService` 依据这些码决定是否重试：
**参数错与内核不可用都不重试**（重试多少次都是同样的错，只会拖长队列）。

### 2. 字段命名有两套，**不要统一**

| 位置 | 命名 | 原因 |
|---|---|---|
| 我们自己的外层字段 | PascalCase（`Type` / `Config` / `Capabilities`） | D81 |
| `RawConfig` 的键 | **picgo 原生**（`picBed` / `picgoPlugins` / `uploaded`） | D22：插件私有键必须原样保留 |
| `RawImgInfo` 的键 | **picgo 原生**（`fileName` / `imgUrl` / `sha`） | D81.3 第 5 条：删除远端时要原封不动交回插件 |
| `DriverConfigField.Name` | **驱动定义**（`repo` / `token` / `path`） | 插件定义 |

**`RawImgInfo` 用 `map[string]any` 而不是结构体**：插件会回写附加字段
（如 `github-plus` 写 `sha`），结构体会把未知字段丢掉 —— 那会导致这些图片
**再也删不掉**（D47）。

## 文件

| 文件 | 内容 |
|---|---|
| `types.go` | `Client` 接口、`Config`、超时约定、`Error` 与错误码映射 |
| `dto.go` | 全部 DTO（含 `RawConfig` / `RawImgInfo` 的原样性说明） |
| `client.go` | HTTP 实现：`call` 内核 + 各端点方法 |
| `mock.go` | 内存实现（含失败注入、能力探测、多配置管理） |

## 测试

```bash
go test ./internal/agent/ -v
```

覆盖：令牌透传、错误码映射（5 种）、`/healthz` 无信封特例、
超时/连接失败、上传与删除的字段名原样性、scoped 插件名路径编码、
mock 的失败注入与多配置生命周期。
