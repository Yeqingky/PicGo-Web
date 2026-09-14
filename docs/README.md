# PicGo-Web 开发文档

本目录是 PicGo-Web 的**开发文档集**：一套把上游 PicGo 生态（`picgo-core` + 插件）包进自建 Web 管理平台的工程约定，覆盖定位、架构、数据模型、接口契约、集成细节、运行机制与分工计划。

> 根仓库说明（面向用户与开发者）见 [`../README.md`](../README.md)。

## 1. 推荐阅读顺序

**按顺序读**（越靠前约束越强）：

| 顺序 | 文件 | 用途 | 读者 | 什么时候读 |
|---|---|---|---|---|
| 1 | [`DECISIONS.md`](./DECISIONS.md) | **决策记录 · 最高约束**。D1–D93 全部关键决策 + O 待定项 | 全部 | **动手前必读；任何改动前先查这里** |
| 2 | [`ARCHITECTURE.md`](./ARCHITECTURE.md) | 架构总览：拓扑、目录结构、技术栈、请求生命周期、扩展点、安全清单 | 开发 | 第一次上手；涉及跨模块改动时 |
| 3 | [`DATA-MODEL.md`](./DATA-MODEL.md) | **表结构唯一真源**。20 张表字段、跨方言规则、分表原则、配置键位表、索引清单 | 开发 | 动数据库 / 加字段 / 加表时 |
| 4 | [`API.md`](./API.md) | **接口契约唯一真源**。内部 REST + SSE + Lsky 兼容层 + agent 内部契约 | 开发 | 动接口 / 联调 / 写前端请求时 |
| 5 | [`OPERATIONS.md`](./OPERATIONS.md) | 运行机制：上传队列、启动恢复、优雅关闭、配额、限流、日志、邮件、驱动同步、删除流程 | 开发 · 运维 | 实现或排查「跑起来之后」的行为时 |
| 6 | [`PICGO-INTEGRATION.md`](./PICGO-INTEGRATION.md) | picgo-core 集成：公开 API 清单、魔法路径机制、config.json 边界、远端删除、补丁清单 | 开发 | 动 agent / 上传链路 / 插件能力时 |
| 7 | [`DESIGN.md`](./DESIGN.md) | **前端设计唯一真源**。设计 token、路由与页面清单、布局、交互规范、组件清单、背景图方案 | 前端 | 做任何前端页面前 |
| 8 | [`PLAN.md`](./PLAN.md) | 工作流拆分、并行批次、里程碑与验收、质量门禁、风险对策 | 开发 · 协作 | 领取任务、排期、验收时 |
| 9 | [`research/`](./research/) | 两份调研报告原文（lsky-pro / skyImage），**仅供追溯** | 参考 | 想确认某个结论的来源时 |

> **四条「唯一真源」**：决策 → `DECISIONS.md`；表结构 → `DATA-MODEL.md`；接口 → `API.md`；
> 前端设计 → `DESIGN.md`。其余文档只能**引用**它们，不能各自复述一份。

## 2. 改动前必读哪几份

按「你要改什么」查表，**该列的文件必须通读后再动手**：

| 改动类型 | 必读 | 说明 |
|---|---|---|
| **改数据库**（加字段 / 加表 / 改索引） | `DECISIONS.md`（D77、D78、**D81**）+ `DATA-MODEL.md` | 先过 `DATA-MODEL.md` §11 扩展性检查清单；能用 `Metadata` 或 KV 配置解决的**不要加迁移**；新内容域**必须新建表**；表/列名用 **PascalCase** |
| **改接口**（加端点 / 改字段 / 改响应） | `DECISIONS.md`（D77.2、**D80**、**D81**）+ `API.md` | 内部接口**一律挂 `/api/web/v1/**`**，`/api/v1/**` 是 Lsky 保留集不得占用；先改 `API.md` 再改代码；新增字段一律向后兼容；对外标识一律用 `UID`，不暴露自增 `ID`；JSON 字段用 PascalCase |
| **改上传流程** | `DECISIONS.md`（D35–D41、D66、D72）+ `OPERATIONS.md` §1–§3 + `PICGO-INTEGRATION.md` §3–§4 | 注意并发度、重试、配额校验与退还、`Jobs`/`JobItems` 两层结构；`picgo.upload()` 失败时返回空数组而非抛错，必须同时监听 `failed` 事件 |
| **改魔法路径 / 文件名** | `DECISIONS.md`（D42–D44、D70）+ `PICGO-INTEGRATION.md` §5 | 模板按**每个存储配置**独立设置；驱动能力靠探测不能硬编码 |
| **改存储驱动 / 插件** | `DECISIONS.md`（D4、D22、D64、D65）+ `PICGO-INTEGRATION.md` §6–§8 + `OPERATIONS.md` §7–§8 | 不能整体重建 `config.json`（插件会往里写私有键）；密钥与元数据分表；驱动差异查 `Capabilities` |
| **改日志 / 邮件 / 配额 / 限流** | `DECISIONS.md`（D20、D21、D29、D45、D72–D74）+ `OPERATIONS.md` §3–§6 | 注意管理员的两个例外（跳配额、跳限流）；限流**默认禁用**；操作日志的 `Type` 取值是字符串枚举，原样保留 |
| **加新功能** | `DECISIONS.md`（D77 全部）+ `DATA-MODEL.md` §11 + `ARCHITECTURE.md` 扩展点一节 | 先确认是否属于「明确不做」（`DECISIONS.md` §十二）；注意 **D82：不提供备份/恢复功能** |
| **改前端** | `DECISIONS.md`（D13、D14、D33、D71、D75、**D81**、**D83–D87**）+ `API.md` + **`DESIGN.md`（必读）** | 先查 `DESIGN.md` 的设计 token 与组件清单；文案走 i18n key 不硬编码；服务端数据不镜像进 Zustand；**类型字段用 PascalCase，变量/函数保持 camelCase**；新增页面只需往 `lib/navigation.ts` 配置数组加一项 |
| **改 PicGo-Core 补丁** | `DECISIONS.md`（D48–D51）+ `PICGO-INTEGRATION.md` §8 + `PicGo-Core/FORK-NOTES.md` | 只增不改、不提交上游；记录改动与上游合并注意事项；**改完必须跑通 `pnpm lint` + `pnpm test`** |
| **改部署** | `DECISIONS.md`（D76、**D82**）+ 根 `README.md` | 面向用户**只提供 docker compose**；备份由运维自行处理，不做导出导入功能 |

## 3. 文档维护规则

| 规则 | 说明 |
|---|---|
| **决策记录是最高约束** | 任何文档与 `DECISIONS.md` 冲突，**以 `DECISIONS.md` 为准**。发现冲突应改文档，不能改决策 |
| **新决策只追加** | 在 `DECISIONS.md` **追加新编号**（当前最新为 **D82**，下一个是 D83），**不要修改历史条目**。要推翻旧决策就新增一条并注明「取代 DXX」（如 D40 已被 D73 取代） |
| **契约唯一** | 接口以 `docs/API.md` 为唯一真源；前端类型定义（`web/src/types/api.ts`）必须与其手动保持同步 |
| **表结构唯一真源** | 以 `docs/DATA-MODEL.md` 为准；其他文档引用表/字段时必须与它**逐字一致**（表名、列名、类型） |
| **命名规范以 D81 为准** | API JSON 字段与数据库标识符用 **PascalCase**，缩写词全大写（`UID`/`URL`/`ID`/`API`）；TS 变量/函数与 Go 局部变量保持 camelCase。**例外**（不得改名）：Lsky 兼容层的一切、环境变量、`settings` 配置键、picgo 侧字段名与 `IImgInfo` 字段、驱动配置字段名、操作日志 `Type` 取值。新增文档时**直接把这条抄进示例**，避免又写成 snake_case |
| **先改文档再改代码** | 任何字段 / 端点 / 表结构变更，**先改文档**，再改实现。禁止「代码先行、文档后补」 |
| **迁移只前进** | `schemaMigrations` 只追加条目，已发布条目永不修改（`DATA-MODEL.md` §9） |
| **文档不重复** | 同一事实只在**一个**文件里写全，其他文件用链接引用。避免同一张表在四处各写一遍然后互相矛盾 |
| **引用要带编号** | 写结论时标明来源决策编号（如「见 D80」），便于追溯与后续推翻 |

## 4. 关于 `research/`

`research/` 下是立项阶段对两个参考项目的源码侦察报告：

| 文件 | 对象 | 内容 |
|---|---|---|
| [`research/lsky-pro.md`](./research/lsky-pro.md) | [lsky-org/lsky-pro](https://github.com/lsky-org/lsky-pro)（PHP / Laravel） | 数据模型、枚举与配置键、HTTP 接口面、业务能力清单、关键业务流程、前端形态，以及「砍掉后端存储与图片分发后剩下什么」 |
| [`research/skyimage.md`](./research/skyimage.md) | [nxtcorex/skyImage](https://github.com/nxtcorex/skyImage)（Go + React） | 数据模型、设置系统、路由清单、Lsky 兼容接口、前端形态、installer、工程实践参考 |

**这些报告的结论已经抽取进 [`DECISIONS.md`](./DECISIONS.md)，原文仅供追溯。**

也就是说：

- **不要**把报告里的设计当成我们的设计 —— 报告记录的是**别人怎么做**，包含大量本项目**明确不做**的部分
  （用户组、标签、公开画廊、商城支付、兑换码、工单、Passkey、图片审核、水印、后端存储与图片分发等，见 `DECISIONS.md` §十二）
- **要**用它们来追溯某个决策的来历（例如「为什么砍掉策略表」「为什么 Lsky 兼容层只做 v1」）
- 要看本项目实际怎么做，**只读 `DECISIONS.md` + `ARCHITECTURE.md` + `DATA-MODEL.md` + `API.md`**

## 5. 文档状态速查

> 行数为**概数**（各文档仍在演进），仅供判断文档体量级别。

| 文档 | 体量 | 主要内容 | 最近的主要变更 |
|---|---|---|---|
| [`DECISIONS.md`](./DECISIONS.md) | 约 800 行 | D1–D82 决策 + O 待定项 + 两个管理员例外附录 | 新增 **D80**（API 路径分层）、**D81**（PascalCase 命名规范）、**D82**（不做备份/恢复） |
| [`ARCHITECTURE.md`](./ARCHITECTURE.md) | 约 900 行 | 拓扑、目录结构、技术栈、请求生命周期、扩展点、安全清单 | 端口改 `:8080`；API 前缀改 `/api/web/v1`；补 GORM 命名配置 |
| [`DATA-MODEL.md`](./DATA-MODEL.md) | 约 850 行 | 20 张表字段、跨方言规则、分表原则、配置键位表、索引清单 | **全量表名/列名/JSON 键改为 PascalCase**；新增 GORM `NoLowerCase` 与 `TableName()` 约定 |
| [`API.md`](./API.md) | 约 2200 行 | 内部 REST + SSE + Lsky v1 兼容层 + agent 内部契约 + 端点索引 | 内部路径迁至 `/api/web/v1`；字段改 PascalCase；新增操作日志、设置、agent 补丁说明 |
| [`OPERATIONS.md`](./OPERATIONS.md) | 约 900 行 | 上传队列、启动恢复、配额、限流、日志、邮件、驱动同步、删除流程、运维速查 | 表名列名改 PascalCase；限流默认禁用；补「PicGo-Core 补丁对运行的影响」 |
| [`PICGO-INTEGRATION.md`](./PICGO-INTEGRATION.md) | 约 1100 行 | picgo-core 公开 API、魔法路径、`config.json` 边界、远端删除、补丁清单、状态对照 | 补丁由「待落」改为「**已落地并验证**」（含实测证据） |
| [`PLAN.md`](./PLAN.md) | 约 700 行 | 里程碑、工作流拆分、并行批次、质量门禁、风险对策、当前进度 | 新增 W0（PicGo-Core fork 与补丁，已完成）；W1 标记完成；补命名规范约束 |
| `research/lsky-pro.md`、`research/skyimage.md` | 约 600 行 / 600 行 | 两个参考项目的源码侦察原文 | 冻结，不再更新（结论已抽进 `DECISIONS.md`） |

## 6. 文档同步待办

本轮规范变更（**D80** 路径分层 / **D81** 命名规范 / **D82** 不做备份恢复）已**全部同步到位**：

| # | 事项 | 结果 |
|---|---|---|
| 1 | 内部 API 前缀 → `/api/web/v1/**`；Lsky 独占 `/api/v1/**` | ✅ 全部文档一致，无路径冲突 |
| 2 | 内部相册路径 → `/api/web/v1/albums`（取消让位） | ✅ 已同步 |
| 3 | 表名 / 列名 / API JSON 字段 → PascalCase | ✅ `DECISIONS.md`、`DATA-MODEL.md` 及各文档一致 |
| 4 | OAuth 回调地址、D77.2 版本段、D36/D38 示意路径 | ✅ 已同步 |
| 5 | PicGo-Core 补丁状态 | ✅ 已标记「已落地并验证」（提交 `6419c2f`） |
| 6 | 备份/恢复（O11） | ✅ 已裁决**不做**（D82） |

> 修正这些条目时**只改字面表述**，不要改动决策语义；
> 若发现某处是**语义**冲突而非笔误，请新增决策编号并在 `DECISIONS.md` 中说明如何取代。

## 7. 文档清单速查

```
docs/
├── README.md               ← 你在这里（索引 + 阅读顺序 + 维护规则 + 同步待办）
├── DECISIONS.md            决策记录（最高约束，D1–D82 + O 待定）
├── ARCHITECTURE.md         架构总览与扩展点
├── DATA-MODEL.md           表设计唯一真源（20 张表）
├── API.md                  接口契约唯一真源（REST + SSE + Lsky + agent）
├── OPERATIONS.md           运行机制（队列/配额/限流/日志/邮件/删除）
├── PICGO-INTEGRATION.md    picgo-core 集成与补丁
├── PLAN.md                 分工、里程碑、验收
└── research/               立项期调研原文（仅供追溯）
    ├── lsky-pro.md
    └── skyimage.md
```
