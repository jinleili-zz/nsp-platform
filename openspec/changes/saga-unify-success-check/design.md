## Context

当前 `saga/executor.go` 中有 4 处 HTTP 响应成功判断逻辑：

1. `ExecuteStep`（同步步骤正向执行）- 第 156 行
2. `ExecuteAsyncStep`（异步步骤提交）- 第 260 行
3. `CompensateStep`（补偿执行）- 第 391 行
4. `Poll`（异步轮询）- 第 488 行

这些判断当前仅检查 HTTP 状态码是否在 `[200, 300)` 范围内，不校验响应体的业务状态码。下游子系统可能返回 HTTP 200 但业务实际失败（body 中 `code != "0"`），当前实现会错误地将其视为成功。

## Goals / Non-Goals

**Goals:**
- 提取一个公共函数统一判断步骤 HTTP 响应是否成功
- 成功条件：HTTP 2xx **且** 响应体 JSON 中 `code` 字段值为 `"0"`
- 将 executor.go 中所有散落的判断逻辑替换为该公共函数
- 补充单元测试覆盖新逻辑
- 同步更新文档

**Non-Goals:**
- 不改变 `MatchPollResult` 的轮询结果匹配逻辑（那是 JSONPath 级别的业务字段匹配）
- 不引入可配置的成功判断策略（当前阶段固定为 `code == "0"`）
- 不改变 CompensateStep 的重试机制

## Decisions

### 1. 公共函数签名设计

```go
// IsStepHTTPSuccess checks whether an HTTP response indicates a successful
// step execution. It returns true only when the status code is 2xx AND the
// response body contains {"code":"0"}.
// 
// On success it also returns the parsed body as map[string]any.
// On failure it returns a non-nil error describing the reason.
func IsStepHTTPSuccess(statusCode int, body []byte) (map[string]any, bool, error)
```

返回值说明：
- `map[string]any`：解析后的响应体（成功时有值，失败时可能为 nil）
- `bool`：是否成功
- `error`：解析错误等异常情况

**理由**：executor.go 中多处在成功后需要 `json.Unmarshal` 解析响应体，将解析逻辑也纳入公共函数可减少重复代码。

### 2. `code` 字段的类型兼容

`code` 字段统一按字符串 `"0"` 匹配。如果 body 中 `code` 是数字 `0`（`json.Number` 或 `float64`），也需要兼容识别为成功。

实现方式：先尝试 string 断言，再尝试 float64 断言（`== 0`），再尝试 `json.Number` 的 `.String() == "0"`。

### 3. CompensateStep 同样使用该公共函数

补偿接口也是下游子系统，同样需要校验 body code。补偿成功的标准与正向执行一致。

### 4. Poll 函数的处理

`Poll` 返回的是 `(map[string]any, error)`，当前在 poller 中通过 `MatchPollResult` 做业务状态匹配。`Poll` 函数本身也应该使用 `IsStepHTTPSuccess` 做初步校验——HTTP 非 2xx 或 body 无 `code:"0"` 时直接返回错误，不再进入 `MatchPollResult` 流程。

### 5. body 为空或非 JSON 的处理

- body 为空：视为失败（缺少 `code` 字段）
- body 非 JSON：视为失败，返回解析错误
- body 中没有 `code` 字段：视为失败

## Risks / Trade-offs

- **[兼容性]** 如果现有下游子系统不返回 `{"code":"0"}` 格式，升级后会导致步骤执行失败 → 需要在文档中明确约定，并在上线前确认所有子系统响应格式
- **[补偿影响]** 补偿接口如果不返回 `{"code":"0"}`，会导致补偿被误判为失败 → 同上，需确认补偿接口也遵循此约定
- **[Poll 影响]** Poll 接口如果不返回 `{"code":"0"}`，会导致轮询直接报错 → 需确认异步回查接口也遵循此约定
