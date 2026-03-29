## Why

当前 `taskqueue` 模块将 broker（消息投递）和 workflow（编排状态机）耦合在同一个包中。broker 层的核心类型 `HandlerFunc` 依赖 `TaskPayload`（含 `TaskID`/`ResourceID`），这些是 workflow 层的业务概念，迫使 consumer 实现硬编码固定的 JSON 反序列化格式，限制了 broker 作为通用消息投递层的复用性。团队决定去掉 workflow 编排层，只保留轻量的 broker 抽象，并增加回调队列（ReplySpec）能力，支持 worker 将结果投递到正确的 producer 队列。

## What Changes

- **BREAKING** 删除 workflow 编排层：`engine.go`、`store.go`、`pg_store.go`、`engine_test.go`、`migrations/` 及所有 Workflow/StepTask/CallbackPayload 相关类型
- **BREAKING** 删除 `TaskPayload`、`TaskResult` 结构体，broker 层只传递 `[]byte` payload，业务语义由调用方自行解析
- **BREAKING** `HandlerFunc` 签名从 `func(ctx, *TaskPayload) (*TaskResult, error)` 改为 `func(ctx context.Context, task *Task) error`，handler 直接接收通用的 `Task`
- **BREAKING** 删除 `rocketmqbroker/` 目录的实现代码，减少引入 taskqueue 后的外部依赖；Broker/Consumer/Inspector 接口本身保持不变，未来可在独立模块中添加 RocketMQ 实现
- `Task` 结构体新增 `Reply *ReplySpec` 字段，`ReplySpec` 当前只有 `Queue string` 字段，nil 表示 fire-and-forget
- 保留 `asynqbroker/` 中的 trace envelope 机制（trace ID 透传），扩展 envelope 新增 `_rto`（ReplySpec JSON）和 `_meta`（Metadata）字段
- 保留 `Broker` 接口（Publish/Close）和 `Consumer` 接口（Handle/Start/Stop）签名不变
- 保留四层 Inspector 接口体系不变
- 保留 Priority 机制和 Metadata 扩展字段
- 更新 `examples/` 适配新接口，删除 workflow demo

## Capabilities

### New Capabilities

- `broker-core`: broker 层核心抽象重新定义——Task（含 ReplySpec）、Broker、Consumer、HandlerFunc 接口、Priority 模型
- `broker-asynq`: asynq 实现层——Broker/Consumer/Inspector 的 asynq 适配，包含 trace envelope 透传及 ReplySpec/Metadata 序列化

### Modified Capabilities

（无已有 spec 需要修改）

## Impact

- **删除的代码**：`taskqueue/engine.go`、`taskqueue/store.go`、`taskqueue/pg_store.go`、`taskqueue/engine_test.go`、`taskqueue/migrations/`、`taskqueue/rocketmqbroker/` 整个目录
- **修改的代码**：`taskqueue/task.go`（重新定义类型，新增 ReplySpec）、`taskqueue/handler.go`、`taskqueue/asynqbroker/*`（适配新 Task 结构和 HandlerFunc 签名）
- **依赖变化**：go.mod 移除 `rocketmq-client-go`、`database/sql`/`lib/pq` 的直接依赖（仅 taskqueue 包级别）；保留 `asynq`、`go-redis`
- **示例代码**：`examples/taskqueue-workflow-demo/` 删除；broker/priority demo 适配新接口
- **破坏性变更**：所有依赖 `TaskPayload`/`HandlerFunc(ctx, *TaskPayload)` 签名的 consumer 代码需要迁移
