## 1. 公共函数实现

- [x] 1.1 在 `saga/executor.go` 中新增 `IsStepHTTPSuccess(statusCode int, body []byte) (map[string]any, bool, error)` 函数，判断逻辑：HTTP 2xx 且 body JSON `code` 字段为 `"0"`（兼容 string/float64/json.Number）
- [x] 1.2 为 `IsStepHTTPSuccess` 编写单元测试，覆盖 spec 中所有场景（HTTP 200+code "0"、200+code 0 数字、200+code "1"、200+空 body、200+非 JSON、200+无 code 字段、500+code "0"、404）

## 2. Executor 方法替换

- [x] 2.1 将 `ExecuteStep` 中的成功判断和 body 解析逻辑替换为 `IsStepHTTPSuccess` 调用
- [x] 2.2 将 `ExecuteAsyncStep` 中的成功判断和 body 解析逻辑替换为 `IsStepHTTPSuccess` 调用
- [x] 2.3 将 `CompensateStep` 中的成功判断逻辑替换为 `IsStepHTTPSuccess` 调用
- [x] 2.4 将 `Poll` 中的成功判断和 body 解析逻辑替换为 `IsStepHTTPSuccess` 调用

## 3. 测试验证

- [x] 3.1 修改/新增 executor 测试用例，验证 HTTP 200 但 body code 非 "0" 时各方法返回失败
- [x] 3.2 确保现有测试中的 mock 服务响应体包含 `{"code":"0"}` 以通过新判断逻辑
- [x] 3.3 运行 `go test ./saga` 确认所有测试通过（不含集成测试）

## 4. 文档更新

- [x] 4.1 更新 `docs/saga.md` 和 `docs/modules/saga.md`，说明步骤执行的成功判断规则变更
- [x] 4.2 更新 `AGENTS.md` 中 Saga 模块的实现细节，增加成功判断函数说明
