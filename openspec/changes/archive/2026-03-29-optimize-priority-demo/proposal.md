## Why

当前 `taskqueue-priority-demo` 混入了业务层面的 workflow 机制（`TaskStore`、`TaskManager`、任务状态管理、重试逻辑），并使用 `taskqueue.Task.Metadata` 传递业务字段 `task_id`。这些代码模糊了 demo 的核心目标——演示基于 asynq 的 broker 层能力（优先级队列、权重消费、回调路由）。需要精简 demo，聚焦 broker 能力展示，并补充 inspector 示例。

## What Changes

- 移除 `store/store.go` 中的 `Task`、`TaskStore` 及所有任务状态管理代码（`Create`、`GetByID`、`UpdateStatus` 等）
- 移除 producer 中的 `TaskManager`（`SubmitTask`、`HandleCallback`、`QueryTask`、重试逻辑）
- 移除 consumer 中的 `CallbackSender` 及回调机制（`Success`、`Fail`、`send`）
- 将 `task_id` 从 `Metadata` 移入 payload，作为业务数据的一部分
- producer 简化为直接使用 `asynqbroker.Broker.Publish` 发送任务，无回调监听
- consumer 简化为直接处理任务并打印结果，无回调发送
- 新增 inspector 示例：在 producer 发送完任务后展示队列统计信息

## Capabilities

### New Capabilities

- `priority-demo-broker`: 聚焦 broker 层的优先级队列发布与消费演示，含 inspector 队列查询
- `priority-demo-inspector`: 在 demo 中集成 inspector 展示队列统计信息

### Modified Capabilities

（无已有 spec 需要修改）

## Impact

- 影响目录：`examples/taskqueue-priority-demo/`
- 删除文件：`store/store.go`（整个 store 包移除）
- 重写文件：`cmd/producer/main.go`、`cmd/consumer/main.go`
- 依赖无变化，复用已有依赖（`asynq`、`trace`、`logger`）
