// Package model 定义全部数据库模型。
//
// 约定（DATA-MODEL.md §0.2）：
//   - 表名/列名 **PascalCase**（D81），每个模型显式实现 TableName()，不依赖词形变化库
//   - 时间列一律 `int64` Unix 秒，**不用 time.Time**
//   - JSON 语义的列用 `string` + `gorm:"type:text"`，由 service 层显式 marshal/unmarshal
//   - 枚举存字符串，不用 DB enum
//   - **只建索引，不建 FOREIGN KEY 约束**（D77）：两方言行为差异大且阻碍归档
//
// 表清单（20 张，见 DATA-MODEL.md §1）：
//
//	身份鉴权  Users / UserProfiles / OAuthIdentities / RefreshTokens / APITokens / LoginAttempts
//	存储配置  StorageConfigs / StorageSecrets
//	主题配置  ThemeConfigs
//	媒体资源  Uploads / UploadResults
//	任务执行  Jobs / JobItems / JobLogs
//	审计记录  OperationLogs / EmailLogs
//	系统配置  SystemSettings / UserSettings
//	插件缓存  Plugins
//	迁移版本  SchemaMeta
package model

// Now 返回当前 Unix 秒。
//
// 所有 CreatedAt / UpdatedAt / *At 字段都用它赋值，保证全库时间口径一致。
// 单独提出来是为了便于测试替换（将来若需要注入时钟，只改这里）。
func Now() int64 { return nowFunc() }

// nowFunc 可被测试替换。
var nowFunc = defaultNow
