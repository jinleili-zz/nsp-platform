## ADDED Requirements

### Requirement: SugaredLogger 格式化接口
系统 SHALL 提供 `SugaredLogger` 接口，包含以下方法：
- `Debugf(format string, args ...any)`
- `Infof(format string, args ...any)`
- `Warnf(format string, args ...any)`
- `Errorf(format string, args ...any)`
- `Fatalf(format string, args ...any)`
- `DebugContextf(ctx context.Context, format string, args ...any)`
- `InfoContextf(ctx context.Context, format string, args ...any)`
- `WarnContextf(ctx context.Context, format string, args ...any)`
- `ErrorContextf(ctx context.Context, format string, args ...any)`

所有 `f` 方法 SHALL 使用 `fmt.Sprintf` 语义将 `format` 和 `args` 合并为日志消息体。

#### Scenario: 格式化字符串日志输出
- **WHEN** 调用 `sugar.Infof("user %d logged in from %s", uid, ip)`
- **THEN** 日志条目的 message 字段为 `"user 42 logged in from 127.0.0.1"`（经 `fmt.Sprintf` 格式化）

#### Scenario: 带 context 的格式化日志
- **WHEN** context 携带 trace_id，调用 `sugar.InfoContextf(ctx, "req %s done", reqID)`
- **THEN** 日志条目同时包含 `trace_id` 字段和格式化后的 message

### Requirement: Logger 接口增加 Sugar() 方法
`Logger` 接口 SHALL 新增 `Sugar() SugaredLogger` 方法，返回与当前 logger 共享底层配置（level、outputs、service 字段）的 sugar 视图。

#### Scenario: 从 Logger 获取 SugaredLogger
- **WHEN** 调用 `l.Sugar()`
- **THEN** 返回非 nil 的 `SugaredLogger`，且其输出包含与 `l` 相同的 `service` 字段和当前动态 level

#### Scenario: With 字段继承
- **WHEN** 先调用 `l2 := l.With("module", "auth")`，再调用 `l2.Sugar().Infof("ok")`
- **THEN** 日志条目包含 `module="auth"` 字段

### Requirement: 全局包级 Sugar 快捷函数
系统 SHALL 在 `logger` 包级别暴露以下函数，委托给 `GetLogger().Sugar()`：
- `Debugf / Infof / Warnf / Errorf / Fatalf`
- `DebugContextf / InfoContextf / WarnContextf / ErrorContextf`

#### Scenario: 包级 Infof 调用
- **WHEN** 调用 `logger.Infof("hello %s", "world")`
- **THEN** 全局 logger 输出 message 为 `"hello world"` 的日志，caller 指向调用 `logger.Infof` 的业务代码行

### Requirement: 分类 Logger 的 Sugar 能力
`category` logger（Access、Platform、Business）的 `Sugar()` SHALL 返回与该分类 logger 共享同一 zap 核心配置的 `SugaredLogger`，输出路径、level 与分类 logger 一致。

#### Scenario: 分类 logger 格式化日志
- **WHEN** 调用 `logger.Access().Sugar().Infof("GET %s %d", path, status)`
- **THEN** 日志写入 access 分类的输出目标，并携带 `log_category="access"` 字段
