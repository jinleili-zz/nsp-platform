## ADDED Requirements

### Requirement: Producer 直接使用 Broker.Publish 发送任务
Producer SHALL 直接使用 `asynqbroker.Broker.Publish` 将任务发送到对应优先级队列，不封装 TaskManager，不监听回调队列。

#### Scenario: 发送高优先级任务
- **WHEN** producer 调用 `broker.Publish` 发送 type 为 `send:payment:notification` 的任务到 `nsp:taskqueue:high`
- **THEN** 任务成功投递到 Redis 中 `nsp:taskqueue:high` 队列

#### Scenario: 发送中优先级任务
- **WHEN** producer 调用 `broker.Publish` 发送 type 为 `send:email` 的任务到 `nsp:taskqueue:middle`
- **THEN** 任务成功投递到 Redis 中 `nsp:taskqueue:middle` 队列

#### Scenario: 发送低优先级任务
- **WHEN** producer 调用 `broker.Publish` 发送 type 为 `generate:report` 的任务到 `nsp:taskqueue:low`
- **THEN** 任务成功投递到 Redis 中 `nsp:taskqueue:low` 队列

### Requirement: task_id 放入 payload 而非 Metadata
所有任务 SHALL 将 `task_id` 作为 payload JSON 的一个字段传递，不使用 `taskqueue.Task.Metadata`。

#### Scenario: payload 包含 task_id
- **WHEN** producer 构造任务 payload 时
- **THEN** payload JSON 包含 `task_id` 字段，且 `taskqueue.Task.Metadata` 中无 `task_id`

### Requirement: Consumer 按权重消费三级优先级任务
Consumer SHALL 配置 `Queues` 权重：`high=30`、`middle=20`、`low=10`，直接处理任务并打印日志，不发送回调。

#### Scenario: 按权重拉取任务
- **WHEN** 三个队列均有任务时启动消费者
- **THEN** 高优先级队列的任务被更频繁地拉取和处理

#### Scenario: handler 处理任务并打印日志
- **WHEN** consumer handler 收到任务时
- **THEN** handler 从 payload 解析 task_id 和业务参数，打印包含 trace_id 的日志

### Requirement: 不使用 Reply/Callback 机制
Producer 构造 `taskqueue.Task` 时 SHALL 不设置 `Reply` 字段，Consumer 处理完成后 SHALL 不发送回调消息。

#### Scenario: Task 无 Reply
- **WHEN** producer 构造任务时
- **THEN** `task.Reply` 为 nil

### Requirement: Trace 上下文传播
Producer SHALL 在启动时初始化 `TraceContext` 并注入 context；Consumer handler SHALL 通过 `trace.MustTraceFromContext(ctx)` 获取 trace 信息并记录日志。

#### Scenario: handler 日志包含 trace_id
- **WHEN** consumer 处理任务时
- **THEN** 日志输出包含非空的 `trace_id` 字段
