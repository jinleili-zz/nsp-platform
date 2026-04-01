## 1. 定义 SugaredLogger 接口

- [x] 1.1 新建 `logger/sugar.go`，定义 `SugaredLogger` 接口，包含 `f`（格式化）系列方法：`Debugf/Infof/Warnf/Errorf/Fatalf` 及对应的 `ContextF` 系列
- [x] 1.2 在 `SugaredLogger` 接口中添加 `With(keysAndValues ...any) SugaredLogger` 方法，支持链式附加字段

## 2. 扩展 Logger 接口

- [x] 2.1 在 `logger/logger.go` 的 `Logger` 接口中新增 `Sugar() SugaredLogger` 方法

## 3. 实现 zapSugaredLogger

- [x] 3.1 在 `logger/sugar.go` 中实现 `zapSugaredLogger` 结构体，持有 `*zap.SugaredLogger`
- [x] 3.2 实现所有 `f` 系列方法（`Debugf` 等），委托给 `zap.SugaredLogger` 对应方法
- [x] 3.3 实现 `ContextF` 系列方法：从 context 提取 trace/span 字段后调用对应 sugar 方法
- [x] 3.4 实现 `With(keysAndValues ...any) SugaredLogger` 方法，返回携带新字段的 `zapSugaredLogger`

## 4. 实现 zapLogger.Sugar()

- [x] 4.1 在 `logger/zap_logger.go` 中为 `zapLogger` 实现 `Sugar() SugaredLogger` 方法，调用 `l.zlogger.Sugar()` 并封装为 `zapSugaredLogger`

## 5. 全局包级快捷函数

- [x] 5.1 在 `logger/logger.go` 中新增全局 `Debugf / Infof / Warnf / Errorf / Fatalf` 函数，委托给 `GetLogger().Sugar()`
- [x] 5.2 在 `logger/logger.go` 中新增全局 `DebugContextf / InfoContextf / WarnContextf / ErrorContextf`，委托给 `GetLogger().Sugar()`

## 6. 测试

- [x] 6.1 在 `logger/sugar_test.go` 中编写 `f` 系列方法的单元测试，验证 message 格式化正确
- [x] 6.2 编写 `ContextF` 测试，验证 trace_id/span_id 字段自动注入
- [x] 6.3 编写 `With` 链式测试，验证 `l.With("k","v").Sugar().Infof(...)` 携带附加字段
- [x] 6.4 编写分类 logger 的 `Sugar()` 测试，验证 `category` 字段存在且输出路径与分类一致
