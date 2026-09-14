package service

// appVersion 是程序版本（用于 User-Agent 等对外标识）。
//
// 与 internal/server.Version 保持一致。之所以在这里再声明一次：
// service 不能 import server（server → handler → service 会成环）。
// 发布时两处一起改（或将来由构建期 ldflags 注入）。
const appVersion = "0.1.0"
