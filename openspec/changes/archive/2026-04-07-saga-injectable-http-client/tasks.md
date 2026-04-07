## 1. Config 结构体变更

- [x] 1.1 在 `saga/executor.go` 的 `ExecutorConfig` 中新增 `HTTPClient *http.Client` 字段，添加 godoc 注释
- [x] 1.2 在 `saga/engine.go` 的 `Config` 中新增 `HTTPClient *http.Client` 字段，添加 godoc 注释

## 2. Executor 初始化逻辑变更

- [x] 2.1 修改 `saga/executor.go` 的 `NewExecutor()`：当 `cfg.HTTPClient != nil` 时直接使用，否则使用 `HTTPTimeout` 创建默认 client
- [x] 2.2 修改 `saga/engine.go` 的 `NewEngine()`：将 `cfg.HTTPClient` 透传到 `ExecutorConfig.HTTPClient`

## 3. 测试 — 各出站路径独立验证

- [x] 3.1 新增测试：注入自定义 HTTPClient 时，同步步骤 action（`ExecuteStep`）使用该 client
- [x] 3.2 新增测试：注入自定义 HTTPClient 时，异步步骤 action（`ExecuteAsyncStep`）使用该 client
- [x] 3.3 新增测试：注入自定义 HTTPClient 时，补偿请求（`CompensateStep`）使用该 client
- [x] 3.4 新增测试：注入自定义 HTTPClient 时，轮询请求（`Poll`）使用该 client
- [x] 3.5 新增测试：未传入 HTTPClient（nil）时保持默认 client 创建行为
- [x] 3.6 运行现有全量测试，确认无回归

## 4. 文档同步更新

- [x] 4.1 更新 `AGENTS.md` 中 SAGA 模块说明，补充 `HTTPClient` 字段文档
- [x] 4.2 更新 `docs/saga.md` 用户文档，补充自定义 HTTP Client 注入说明
- [x] 4.3 更新 `docs/modules/saga.md` 模块文档，补充 Config 字段变更
- [x] 4.4 更新 `saga/README.md` 包级文档，补充使用示例
