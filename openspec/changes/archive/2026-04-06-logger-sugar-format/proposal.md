## Why

当前 logger 仅支持 slog 风格的 key-value pair 日志调用（`Info(msg, key, val, ...)`），缺乏格式化字符串接口（`Infof("user %d logged in", uid)`）。对于需要在消息体中嵌入动态内容的场景，现有接口无法满足。

## What Changes

- 新增 `SugaredLogger` 接口，提供 `f`（格式化）系列方法：
  - `Debugf / Infof / Warnf / Errorf / Fatalf(format string, args ...any)`
  - `DebugContextf / InfoContextf / WarnContextf / ErrorContextf(ctx, format, args...)`
- 在 `Logger` 接口新增 `Sugar() SugaredLogger` 方法，返回 sugar 视图
- `zapLogger` 实现 `Sugar()`，底层使用 `zap.SugaredLogger`
- 全局包级便捷函数：`logger.Infof(...)` 等
- 分类 logger（Access / Platform / Business）也通过 `Sugar()` 获得同等能力

## Capabilities

### New Capabilities
- `logger-sugar`: 基于 `zap.SugaredLogger` 的格式化日志接口，包括 `SugaredLogger` 接口定义、`zapLogger` 实现、全局便捷函数

### Modified Capabilities
<!-- no existing spec files found -->

## Impact

- **修改文件**：`logger/logger.go`（`Logger` 接口增加 `Sugar()` 方法、全局 `Infof` 等便捷函数）、`logger/zap_logger.go`（实现 `Sugar()`）、`logger/category.go`（分类 logger 代理 Sugar()）
- **新增文件**：`logger/sugar.go`（`SugaredLogger` 接口定义及 `zapSugaredLogger` 实现）
- **依赖**：无新依赖，`go.uber.org/zap` 已包含 `SugaredLogger`
- **不破坏兼容性**：现有 `Logger` 接口只增不减，存量代码无需修改
