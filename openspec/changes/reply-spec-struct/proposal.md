## Why

`Task.ReplyTo` 当前定义为 `string` 类型，仅能表达队列名称。随着业务复杂度增长，回调路由可能需要携带更多信息（如 routing key、回调超时、优先级等），届时直接在 `string` 上扩展代价极高且需要破坏性变更。将其提前封装为 `ReplySpec` 结构体，以最小代价为未来扩展预留空间。

## What Changes

- 在 `taskqueue/task.go` 中新增 `ReplySpec` 结构体，当前包含单一字段 `Queue string`
- `Task.ReplyTo string` 改为 `Task.Reply *ReplySpec`（指针类型，nil 表示 fire-and-forget，与原 empty string 语义等价）
- asynq envelope 中 `_rto` 字段的值从队列名字符串改为序列化的 `ReplySpec` JSON（向后兼容旧的纯字符串格式）
- 更新 `asynqbroker` 的 wrap/unwrap 逻辑适配新类型
- 更新 `simplify-broker-layer` 变更中 `broker-core` capability 的相关 spec

## Capabilities

### New Capabilities

（无新 capability，变更范围在已有 broker-core 内）

### Modified Capabilities

- `broker-core`: `Task.ReplyTo string` 改为 `Task.Reply *ReplySpec`，ReplySpec 结构体定义及 nil 语义
- `broker-asynq`: envelope `_rto` 字段序列化格式变更，需向后兼容旧纯字符串格式

## Impact

- **修改的代码**：`taskqueue/task.go`（新增 ReplySpec，修改 Task 字段）、`taskqueue/asynqbroker/trace_envelope.go`（wrap/unwrap 逻辑）、`taskqueue/asynqbroker/trace_envelope_test.go`
- **示例代码**：`examples/` 中所有使用 `Task.ReplyTo` 的地方改为 `Task.Reply = &taskqueue.ReplySpec{Queue: "..."}`
- **破坏性变更**：`Task.ReplyTo string` → `Task.Reply *ReplySpec`，所有设置或读取 ReplyTo 的代码需要迁移
- **无依赖变化**：不引入新的外部依赖
