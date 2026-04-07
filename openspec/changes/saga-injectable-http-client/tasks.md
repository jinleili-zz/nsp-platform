## 1. Config 结构体变更

- [ ] 1.1 在 `saga/executor.go` 的 `ExecutorConfig` 中新增 `HTTPClient *http.Client` 字段，添加 godoc 注释
- [ ] 1.2 在 `saga/engine.go` 的 `Config` 中新增 `HTTPClient *http.Client` 字段，添加 godoc 注释

## 2. Executor 初始化逻辑变更

- [ ] 2.1 修改 `saga/executor.go` 的 `NewExecutor()`：当 `cfg.HTTPClient != nil` 时直接使用，否则使用 `HTTPTimeout` 创建默认 client
- [ ] 2.2 修改 `saga/engine.go` 的 `NewEngine()`：将 `cfg.HTTPClient` 透传到 `ExecutorConfig.HTTPClient`

## 3. 测试

- [ ] 3.1 新增单元测试：验证传入自定义 HTTPClient 时 Executor 使用该 client 发送请求
- [ ] 3.2 新增单元测试：验证未传入 HTTPClient 时保持默认行为
- [ ] 3.3 运行现有全量测试，确认无回归
