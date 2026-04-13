## Why

executor.go 中有 4 处散落的 HTTP 成功判断逻辑（`resp.StatusCode >= 200 && resp.StatusCode < 300`），且当前仅检查 HTTP 状态码。需要统一收敛为一个公共函数，同时增加对响应体 `{"code":"0"}` 的校验，确保下游子系统真正返回业务成功。当判断条件未来变化时只需修改一处。

## What Changes

- 新增一个公共函数（如 `IsStepHTTPSuccess`），同时判断 HTTP 状态码（2xx）和响应体中 `code` 字段是否为 `"0"`
- 将 `ExecuteStep`、`ExecuteAsyncStep`、`CompensateStep`、`Poll` 中的成功判断统一替换为该公共函数
- 补偿场景（`CompensateStep`）同样使用该公共函数
- 更新相关单元测试，覆盖新的判断逻辑（HTTP 2xx 但 body code 非 "0" 应视为失败）
- 同步更新文档

## Capabilities

### New Capabilities
- `step-response-success-check`: 提取统一的步骤 HTTP 响应成功判断函数，判断条件为 HTTP 2xx 且 body `{"code":"0"}`

### Modified Capabilities

## Impact

- `saga/executor.go`: 4 处成功判断逻辑替换为公共函数调用
- `saga/*_test.go`: 新增/修改测试用例覆盖 body code 校验
- `docs/saga.md`、`docs/modules/saga.md`、`saga/README.md`、`AGENTS.md`: 文档同步更新，说明新的成功判断规则
