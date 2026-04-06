# broker-asynq Specification

## Purpose
TBD - created by archiving change simplify-broker-layer. Update Purpose after archive.
## Requirements
### Requirement: Asynq Broker publishes with trace envelope
asynq Broker 实现在 Publish 时，当以下任意条件成立时 SHALL 包装 trace envelope：
- ctx 中存在 TraceContext，OR
- `Task.Reply` 非 nil，OR
- `Task.Metadata` 非空

只有三者均不满足时，才降级为直接发送原始 payload（graceful degradation，确保旧消息兼容性）。

envelope 格式为 JSON，包含以下字段：
- `_v: 1` — 版本号，用于可靠识别 envelope 格式
- `_tid` — TraceID（omitempty）
- `_sid` — 发送方 SpanId，consumer 端用作 ParentSpanId（omitempty）
- `_smpl` — 采样标志
- `_rto` — ReplySpec 的 JSON 序列化（omitempty），格式为 `{"queue":"name"}`
- `_meta` — Task.Metadata 业务元数据，`map[string]string`（omitempty）
- `payload` — 原始业务 Payload

#### Scenario: Publish with TraceContext and Reply
- **WHEN** ctx 包含 TraceContext 且 Task.Reply = &ReplySpec{Queue: "my-callback-queue"}
- **THEN** 实际写入 asynq 的 payload SHALL 为 envelope JSON，`_rto` 字段值为 `{"queue":"my-callback-queue"}`，`_tid`/`_sid` 字段携带 trace 信息

#### Scenario: Publish with Reply but no TraceContext
- **WHEN** ctx 不包含 TraceContext，但 Task.Reply = &ReplySpec{Queue: "order-svc:callback"}
- **THEN** 实际写入 asynq 的 payload SHALL 仍为 envelope JSON（`_v=1`），`_rto` 字段值为 `{"queue":"order-svc:callback"}`，trace 字段为空

#### Scenario: Publish with Metadata but no TraceContext and no Reply
- **WHEN** ctx 不包含 TraceContext，Task.Reply 为 nil，但 Task.Metadata = {"tenant": "acme"}
- **THEN** 实际写入 asynq 的 payload SHALL 为 envelope JSON，`_meta` 字段包含 {"tenant": "acme"}

#### Scenario: Publish with no TraceContext, no Reply, no Metadata
- **WHEN** ctx 不包含 TraceContext，Task.Reply 为 nil，Task.Metadata 为空
- **THEN** 实际写入 asynq 的 payload SHALL 为 Task.Payload 原始内容（不包装 envelope）

### Requirement: Asynq Consumer unwraps envelope and restores context
asynq Consumer 实现在收到消息时 SHALL：
1. 尝试解析 trace envelope（检查 `_v == 1`）
2. 若为 envelope 格式：
   a. 提取 `_tid`/`_sid`/`_smpl`，恢复 TraceContext 到 ctx
   b. 从 `_rto` 字段反序列化还原 Task.Reply 为 `*ReplySpec`
   c. 从 `_meta` 字段还原 Task.Metadata
   d. 将 `payload` 字段作为 Task.Payload 传递给 handler
3. 若非 envelope 格式（兼容旧消息），直接将整个 payload 传递给 handler，Reply 为 nil，Metadata 为空

#### Scenario: Consumer receives envelope message with Reply and Metadata
- **WHEN** consumer 收到包含 envelope 的消息，envelope 中 `_rto = {"queue":"callback-q"}`，`_meta = {"tenant": "acme"}`
- **THEN** handler 收到的 Task SHALL 满足：Payload 为原始业务数据（不含 envelope wrapper），Reply = &ReplySpec{Queue: "callback-q"}，Metadata = {"tenant": "acme"}，ctx 中包含恢复的 TraceContext

#### Scenario: Consumer receives envelope without _rto
- **WHEN** consumer 收到 envelope（`_v=1`），但 `_rto` 字段缺失
- **THEN** handler 收到的 Task.Reply SHALL 为 nil

#### Scenario: Consumer receives legacy message without envelope
- **WHEN** consumer 收到不含 envelope 的旧格式消息（`_v` 字段不存在或不等于 1）
- **THEN** handler 收到的 Task.Payload SHALL 为消息原始内容，Task.Reply SHALL 为 nil，Task.Metadata SHALL 为空

### Requirement: Asynq Consumer delivers full Task to handler
asynq Consumer 在调用 HandlerFunc 时 SHALL 构造完整的 `Task` 结构体传递给 handler，包含：
- `Type` — 从 asynq task type 读取
- `Payload` — 解包后的业务载荷
- `Queue` — 消息所在队列名称
- `Reply` — 从 envelope `_rto` 字段恢复为 `*ReplySpec`；非 envelope 消息为 nil
- `Metadata` — 从 envelope `_meta` 字段恢复；非 envelope 消息为空 map

#### Scenario: Handler receives Task with all fields populated
- **WHEN** producer 发送 Task{Type: "send_email", Payload: {...}, Queue: "high", Reply: &ReplySpec{Queue: "callback-q"}, Metadata: {"tenant": "acme"}}
- **THEN** consumer handler 收到的 Task SHALL 包含 Type="send_email"、原始 Payload、Reply=&ReplySpec{Queue: "callback-q"}、Metadata={"tenant":"acme"}

### Requirement: Asynq Inspector unwraps envelope for task detail
asynq Inspector 在返回 TaskDetail 时 SHALL 对 payload 进行 envelope 解包，返回纯业务数据。

#### Scenario: GetTaskInfo returns unwrapped payload
- **WHEN** 通过 Inspector.GetTaskInfo 查询一个任务
- **THEN** 返回的 TaskDetail.Payload SHALL 为原始业务数据，不含 trace envelope wrapper

### Requirement: Asynq Consumer supports configurable concurrency and queue weights
asynq Consumer SHALL 通过 `ConsumerConfig` 配置：
- `Concurrency int` — 并发 worker goroutine 数量
- `Queues map[string]int` — 队列名到优先级权重的映射
- `StrictPriority bool` — 是否启用严格优先级模式

#### Scenario: Consumer processes high-weight queue preferentially
- **WHEN** Queues 配置为 `{"high": 30, "low": 10}` 且两个队列都有任务
- **THEN** consumer SHALL 以约 3:1 的比例优先处理 high 队列的任务

### Requirement: No RocketMQ implementation in codebase
`taskqueue/rocketmqbroker/` 目录 SHALL 被完整删除。Broker/Consumer/Inspector 接口保持实现无关，未来可在独立的 Go module 中提供 RocketMQ 实现。

#### Scenario: Build taskqueue module
- **WHEN** 编译 taskqueue 及其子包
- **THEN** SHALL 不产生对 `github.com/apache/rocketmq-client-go` 的任何依赖

