/**
 * 两套 schema 的原始类型（与 `docs/API.md` / `docs/DESIGN.md` §7 对应）。
 *
 * ⚠️ **字段名保持原样**：这些是 picgo / 主题 manifest 定义的字段名，
 * 不适用 D81 的 PascalCase 规则（D81.3 第 5 条）。
 */

// ---------------------------------------------------------------------------
// 插件 / 驱动 schema（agent 求值后返回）
// 来源：docs/API.md §13 `GET /api/uploaders` 的 `Uploaders[].Config`
//
// ⚠️ **字段名是 PascalCase**（因为这是**我们自己的 agent** 返回的 JSON，适用 D81）；
//   但 **`Type` 的取值沿用 picgo 的词汇**（`input`/`password`/`list`/…）——
//   那些是「值」而不是「字段名」，不能翻译。
//   映射关系见 docs/DESIGN.md §7.2。
// ---------------------------------------------------------------------------

export interface IPluginConfigChoiceObject {
  Name?: string
  Value: unknown
  Checked?: boolean
}

/** 选项：既可能是纯字符串（同时作 label 与 value），也可能是对象形式。 */
export type IPluginConfigChoice = string | IPluginConfigChoiceObject

export interface IPluginConfig {
  /** 字段名（提交时的 key） */
  Name: string
  /**
   * 控件类型。取值为 picgo 的词汇：
   * `input` / `password` / `list` / `checkbox` / `confirm` / `editor` / 其它。
   */
  Type: string
  Required: boolean
  /** 展示标签（inquirer 风格），优先于 `Name` */
  Alias?: string
  /** 辅助说明 */
  Message?: string
  Default?: unknown
  /** `list` / `checkbox` 的选项 */
  Choices?: IPluginConfigChoice[]
  /** 依赖的其它字段名；变化时需回源重求值 */
  DependsOn?: string[]
  [propName: string]: unknown
}

// ---------------------------------------------------------------------------
// 主题 schema（`manifest.Configuration.Items`，D98）
// 来源：docs/API.md §10 `GET /site/config` 的 `Theme.Settings` 与
//       `GET /themes/{ThemeID}/settings` 的 `Schema`
// ---------------------------------------------------------------------------

export interface ThemeConfigItemSchema {
  Key: string
  Name: string
  /** `string` / `text` / `number` / `switch` / `select` / `json` / 其它 */
  Type?: string
  Required?: boolean
  Default?: unknown
  /** `select` 的选项：**逗号分隔字符串** */
  Options?: string
  Help?: string
  /** `json` 类型可选：声明后按结构化列表（repeater）渲染 */
  ItemSchema?: ThemeConfigItemSchema[]
}

export interface ThemeConfigItem extends ThemeConfigItemSchema {
  /** 当前生效值（`GET .../settings` 返回） */
  Value?: unknown
  /** 值来源：`db` | `default` */
  Source?: 'db' | 'default'
  /** `password` 类字段的掩码指示 */
  HasValue?: boolean
  Secret?: boolean
}
