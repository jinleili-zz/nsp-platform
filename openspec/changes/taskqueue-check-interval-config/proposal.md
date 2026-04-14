## Why

asynq server 内部通过 `TaskCheckInterval`（默认 1s）控制队列空闲时轮询新任务的频率。对于延迟敏感的业务场景（如实时通知、设备指令下发），1s 的默认间隔可能过大；而过于频繁的轮询又会增加 Redis 负载。当前 `asynqbroker.ConsumerConfig` 没有暴露此参数，用户无法按需调整，需要在 Consumer 接口层提供受限可配的 `TaskCheckInterval`。

## What Changes

- 在 `asynqbroker.ConsumerConfig` 中新增 `TaskCheckInterval time.Duration` 字段
- 在 `NewConsumer` 构造函数中将该值传入 `asynq.Config.TaskCheckInterval`
- 增加校验逻辑：最小 200ms、最大 2s；为零值时不设置（保持 asynq 默认 1s）
- 更新 taskqueue 模块文档，说明新参数及其约束
- 补充单元测试覆盖边界校验与默认行为

## Capabilities

### New Capabilities

- `consumer-check-interval`: 允许用户配置 asynq consumer 的 TaskCheckInterval 参数，控制空闲队列轮询频率

### Modified Capabilities

（无现有 spec 级别的行为变更）

## Impact

- **代码**：`taskqueue/asynqbroker/consumer.go`（ConsumerConfig 结构体 + NewConsumer 逻辑）
- **测试**：`taskqueue/asynqbroker/broker_consumer_test.go`（新增校验测试）
- **文档**：`taskqueue/docs/` 下相关文档、`AGENTS.md` 中 taskqueue 部分（如已包含）
- **API 兼容性**：纯新增字段，零值保持原有行为，向后兼容
