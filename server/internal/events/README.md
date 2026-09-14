# internal/events

进程内的 SSE 事件总线。

## 数据流

```
picgo-agent 的 SSE ──(AgentBridge)──▶ Hub ──▶ 各 HTTP SSE 订阅者（浏览器）
                                        ▲
Go 侧业务（上传队列 / 插件 / 存储同步）──┘
```

- agent 是唯一能观察到「npm 安装日志」的地方，但浏览器**不能**直连它
  （只监听 127.0.0.1 且要求内部令牌）→ 由 Bridge 单向订阅后转发
- **上传进度由 Go 自己发**（带 `UserUID`），不走 Bridge —— 见下方「两套事件源」

## 两套事件源（重要）

agent 的事件是**全局**的（`picgo.emit` 绑到根实例），事件体里**没有 UserUID**。
若把它原样转发给所有浏览器，A 就能看到 B 的上传进度（文件名 + 百分比）。

因此分工如下：

| 事件 | 谁发 | 是否带 UserUID |
|---|---|---|
| `upload.progress` / `upload.finished` / `upload.failed` | **Go**（UploadService） | ✅ 按用户投递 |
| `job.started` / `job.finished` | **Go** | ✅ 按用户投递 |
| `job.log` | agent → Bridge | ❌ 广播（仅管理员场景为插件安装日志，无用户数据） |
| `system.notice` | Go（也含 Bridge 转发的） | ❌ 广播（站点级提示） |
| `ping` | 各自 | ❌ |

Hub **只广播，不做过滤**（过滤需要知道「谁在看」）。
权限过滤在订阅端完成：`handler.JobHandler.Events` 用 `visibleTo()` 判断
（`AdminOnly` → 仅管理员；带 `UserUID` → 仅本人与管理员）。

## 设计要点

| 要点 | 说明 |
|---|---|
| **事件名是小写点分字符串** | `upload.progress` 等，原样。它是协议层标识符，与 D81（PascalCase）无关 |
| **事件体内部字段用 PascalCase** | D81；例如 `{"JobUID": "...", "Progress": 60}` |
| **慢消费者丢弃而不是阻塞** | 缓冲满即丢（记 debug 日志）。一个卡住的浏览器不能拖垮上传 worker；前端有「重连后用 `GET /jobs` 对齐状态」的约定，因此丢事件不会导致状态永久不一致 |
| **`cancel` 必须幂等** | 进程退出时 `Hub.Close()` 会关闭所有订阅通道，而仍在运行的 SSE handler 会 `defer cancel()`。两者相撞会导致 `close of closed channel` panic —— 即「优雅关闭时反而崩掉」。已在 `hub_test.go` 有回归 |
| **Bridge 断线指数退避重连** | 1s→2s→4s→8s，上限 30s；另有「空闲 60s 无字节」看护（防 TCP 半开） |

## 用法

```go
// 装配（一次）
hub := events.New(log)

// 业务侧发布（推荐用语义化助手，避免手写 map 拼错字段名）
hub.PublishUploadProgress(jobUID, uploadUID, seq, fileName, 60, userUID)
hub.PublishUploadFinished(jobUID, uploadUID, seq, fileName, url, thumbURL, userUID)
hub.PublishUploadFailed(jobUID, uploadUID, seq, fileName, errMsg, attempts, userUID)
hub.PublishJobFinished(events.JobFinishedPayload{...}, userUID)
hub.PublishNotice("warn", "内核正在重启")

// 桥接 agent 的 SSE（独立 goroutine）
go events.NewAgentBridge(events.AgentBridgeConfig{
    EventsURL: client.EventsURL(),
    Token:     client.Token(),
}, hub, log).Run(ctx)

// 订阅（HTTP handler）
ch, cancel := hub.Subscribe(userUID, isAdmin)
defer cancel()
for ev := range ch { ... }
```

## 文件

| 文件 | 内容 |
|---|---|
| `hub.go` | `Hub` / `Event` / 事件名常量 / 语义化发布助手 |
| `bridge.go` | `AgentBridge`：SSE 解析（含多行 `data`）+ 指数退避重连 + 空闲看护 |

## 测试

```bash
go test ./internal/events/ -v
```

覆盖：广播、多订阅者、**慢消费者不阻塞**、AdminOnly 过滤、`cancel` 幂等、
`Close` 幂等、事件体 PascalCase、Bridge 转帧、**失败后重连**、多行 data 拼接。
