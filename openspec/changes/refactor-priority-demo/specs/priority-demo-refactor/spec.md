## ADDED Requirements

### Requirement: Producer 通过 TaskManager 按业务域路由回调队列投递任务
Producer SHALL 使用 `TaskManager.SubmitTaskWithPriority` 将任务投递到对应优先级队列，并在 `taskqueue.Task.Reply.Queue` 中根据任务业务域指定目标回调队列：
- 订单类任务（`send:payment:notification`、`deduct:inventory`、`generate:report`、`export:data`）使用 `store.CallbackQueueOrder`（`nsp:taskqueue:callback:order`）
- 通知类任务（`send:email`、`send:notification`、`process:image`）使用 `store.CallbackQueueNotify`（`nsp:taskqueue:callback:notify`）

#### Scenario: 订单类任务携带 order 回调队列
- **WHEN** 投递 `send:payment:notification` 任务时
- **THEN** broker 收到的 `task.Queue` 为 `"nsp:taskqueue:high"`，`task.Reply.Queue` 为 `"nsp:taskqueue:callback:order"`

#### Scenario: 通知类任务携带 notify 回调队列
- **WHEN** 投递 `send:email` 任务时
- **THEN** broker 收到的 `task.Queue` 为 `"nsp:taskqueue:middle"`，`task.Reply.Queue` 为 `"nsp:taskqueue:callback:notify"`

### Requirement: Producer 启动双回调消费者分别监听两条回调队列
Producer SHALL 启动两个独立回调消费者，分别监听 `nsp:taskqueue:callback:order` 和 `nsp:taskqueue:callback:notify`，收到回调消息后通过 `TaskManager.HandleCallback` 更新 TaskStore 中的任务状态。

#### Scenario: order 回调消费者收到 completed 消息
- **WHEN** `nsp:taskqueue:callback:order` 队列收到 `status="completed"` 消息
- **THEN** TaskStore 中对应任务状态更新为 `"completed"`，Result 字段被写入

#### Scenario: notify 回调消费者收到 failed 消息且未超过重试上限
- **WHEN** `nsp:taskqueue:callback:notify` 队列收到 `status="failed"` 且 `RetryCount < MaxRetries`
- **THEN** 任务重新投递到 broker，RetryCount 递增

### Requirement: Consumer handler 读取 task.Reply.Queue 路由回调
每个 handler SHALL 执行完成后调用 `CallbackSender.Success` 或 `CallbackSender.Fail`；`CallbackSender` SHALL 读取 `task.Reply.Queue` 字段决定将结果发往哪条回调队列，不硬编码队列名称。

#### Scenario: handler 成功后回调发往消息携带的目标队列
- **WHEN** handler 正常返回且 `task.Reply.Queue = "nsp:taskqueue:callback:order"`
- **THEN** 回调消息投递到 `"nsp:taskqueue:callback:order"` 队列

#### Scenario: Reply 为空时不发送回调
- **WHEN** `task.Reply` 为 nil 或 `task.Reply.Queue` 为空字符串
- **THEN** `CallbackSender` 直接返回 nil，不发送任何消息

### Requirement: Consumer 使用权重队列消费三级优先级任务
Consumer SHALL 配置 `Queues` 权重：`high=30`、`middle=20`、`low=10`，并为每种任务类型注册 handler。

#### Scenario: 消费者按权重拉取任务
- **WHEN** 三个队列均有任务时启动消费者
- **THEN** 高优先级队列的任务被更频繁地拉取和处理

### Requirement: Consumer handler 执行完成后发送回调
每个 handler SHALL 在成功执行后调用 `CallbackSender.Success`，失败时调用 `CallbackSender.Fail`，将结果发送到 `task.Reply.Queue`。

#### Scenario: 成功执行发送 completed 回调
- **WHEN** handler 正常返回
- **THEN** 回调队列中出现 `status="completed"` 的消息

### Requirement: Trace 上下文从 context 传播到 handler
Producer SHALL 在启动时初始化 `TraceContext` 并注入 context；Consumer handler SHALL 通过 `trace.MustTraceFromContext(ctx)` 获取 trace 信息并记录日志。

#### Scenario: handler 日志包含 trace_id
- **WHEN** consumer 处理任务时
- **THEN** 日志输出包含非空的 `trace_id` 字段
