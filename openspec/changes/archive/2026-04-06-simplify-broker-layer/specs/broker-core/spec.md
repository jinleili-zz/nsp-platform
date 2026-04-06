## ADDED Requirements

### Requirement: Task structure is broker-level only
`Task` 结构体 SHALL 只包含消息投递层关注的字段，不包含任何业务语义字段（如 TaskID、ResourceID）。

字段定义：
- `Type string` — 任务类型标识，用于 consumer 端路由到对应 handler
- `Payload []byte` — 业务载荷（opaque bytes），broker 层不解析其内容
- `Queue string` — 目标队列名称
- `Reply *ReplySpec` — 回调路由规格，nil 表示 fire-and-forget
- `Priority Priority` — 执行优先级
- `Metadata map[string]string` — 可扩展的元数据键值对

`ReplySpec` 结构体 SHALL 定义如下：
```go
type ReplySpec struct {
    Queue string  // 回调队列名称
}
```

#### Scenario: Producer publishes a task with Reply
- **WHEN** producer 创建 Task 并设置 `Reply = &ReplySpec{Queue: "order-service:callback"}`
- **THEN** Task 结构体 SHALL 携带该 Reply 值，broker 实现 SHALL 将其持久化到消息中

#### Scenario: Producer publishes a fire-and-forget task
- **WHEN** producer 创建 Task 且 `Reply` 为 nil
- **THEN** broker SHALL 正常投递消息，consumer 收到的 Task 的 Reply 为 nil

### Requirement: TaskInfo returned after publish
`TaskInfo` 结构体 SHALL 包含 broker 分配的任务 ID 和实际队列名称。

字段定义：
- `BrokerTaskID string`
- `Queue string`

#### Scenario: Successful publish returns TaskInfo
- **WHEN** Broker.Publish 成功
- **THEN** 返回的 TaskInfo SHALL 包含非空的 BrokerTaskID 和实际投递的 Queue 名称

### Requirement: Broker interface for publishing
`Broker` 接口 SHALL 定义两个方法：
- `Publish(ctx context.Context, task *Task) (*TaskInfo, error)`
- `Close() error`

Publish SHALL 透传 context 中的 trace 信息（由具体实现决定机制）。

#### Scenario: Publish with trace context
- **WHEN** ctx 中包含 TraceContext 且调用 Broker.Publish
- **THEN** 实现 SHALL 将 trace 信息附加到消息中，使 consumer 端能恢复 trace 上下文

#### Scenario: Publish without trace context
- **WHEN** ctx 中不包含 TraceContext 且调用 Broker.Publish
- **THEN** 实现 SHALL 正常投递消息，不因缺少 trace 而失败（graceful degradation）

### Requirement: Consumer interface for consuming
`Consumer` 接口 SHALL 定义三个方法：
- `Handle(taskType string, handler HandlerFunc)`
- `Start(ctx context.Context) error`
- `Stop() error`

#### Scenario: Register handler and start consuming
- **WHEN** 调用 Handle 注册 handler 后调用 Start
- **THEN** consumer SHALL 开始从配置的队列消费消息，按 taskType 路由到对应 handler

#### Scenario: Context cancellation triggers graceful stop
- **WHEN** 传入 Start 的 ctx 被 cancel
- **THEN** consumer SHALL 优雅停止，等待当前正在处理的任务完成

### Requirement: HandlerFunc signature is broker-generic
`HandlerFunc` 的签名 SHALL 为 `func(ctx context.Context, task *Task) error`。

handler 接收的 `Task` SHALL 包含完整的消息元信息（Type、Payload、Queue、Reply、Priority、Metadata），业务层自行解析 Payload。

#### Scenario: Handler receives full Task with Reply
- **WHEN** consumer 收到一条携带 Reply 的消息
- **THEN** handler 收到的 Task.Reply SHALL 等于 producer 发送时设置的值

#### Scenario: Handler receives Task with trace context in ctx
- **WHEN** consumer 收到一条携带 trace 元数据的消息
- **THEN** handler 的 ctx 中 SHALL 可通过 `trace.TraceFromContext(ctx)` 获取恢复的 TraceContext

### Requirement: Priority levels
Priority SHALL 定义四个级别：
- `PriorityLow = 1`
- `PriorityNormal = 3`
- `PriorityHigh = 6`
- `PriorityCritical = 9`

#### Scenario: Tasks with higher priority are processed first
- **WHEN** 队列中同时存在 PriorityHigh 和 PriorityLow 的任务
- **THEN** broker 实现 SHALL 优先处理 PriorityHigh 的任务（具体调度策略取决于实现）

### Requirement: No workflow types in broker package
broker 层（`taskqueue` 包）SHALL NOT 包含以下类型：`TaskPayload`、`TaskResult`、`CallbackPayload`、`Workflow`、`StepTask`、`StepDefinition`、`WorkflowDefinition`、`WorkflowStatus`、`StepStatus`、`StepStats`、`WorkflowStatusResponse`、`WorkflowHooks`。

#### Scenario: Import taskqueue package
- **WHEN** 业务代码 import `taskqueue` 包
- **THEN** SHALL 不引入任何 workflow 编排相关的类型依赖

### Requirement: No database dependency in broker package
broker 层 SHALL NOT 依赖 `database/sql`、`lib/pq` 或任何数据库驱动。

#### Scenario: Compile taskqueue package
- **WHEN** 编译 `taskqueue` 包
- **THEN** SHALL 不产生对 PostgreSQL 驱动的传递依赖

### Requirement: Inspector interface hierarchy preserved
四层 Inspector 接口体系 SHALL 保持不变：
- Layer 1: `Inspector`（Queues/GetQueueStats/ListWorkers/Close）
- Layer 2: `TaskReader`（GetTaskInfo/ListTasks，可选）
- Layer 3: `TaskController`（DeleteTask/RunTask/ArchiveTask/CancelTask/Batch*，可选）
- Layer 4: `QueueController`（PauseQueue/UnpauseQueue/DeleteQueue，可选）

#### Scenario: Type assertion for optional layers
- **WHEN** 调用方持有 Inspector 接口并尝试断言 TaskReader
- **THEN** 支持 TaskReader 的实现 SHALL 通过断言，不支持的 SHALL 失败
