## MODIFIED Requirements

### Requirement: Asynq Broker publishes with trace envelope
asynq Broker 实现在 Publish 时，当以下任意条件成立时 SHALL 包装 trace envelope：
- ctx 中存在 TraceContext，OR
- `Task.Reply` 非 nil，OR
- `Task.Metadata` 非空

只有三者均不满足时，才降级为直接发送原始 payload。

envelope 格式为 JSON，包含以下字段：
- `_v: 1` — 版本号
- `_tid` — TraceID（omitempty）
- `_sid` — 发送方 SpanId（omitempty）
- `_smpl` — 采样标志
- `_rto` — `ReplySpec` 的 JSON 序列化（omitempty），格式为 `{"queue":"name"}`；旧格式为纯字符串，unwrap 时需兼容
- `_meta` — Task.Metadata（omitempty）
- `payload` — 原始业务 Payload

#### Scenario: Publish with Reply set
- **WHEN** Task.Reply = &ReplySpec{Queue: "callback-q"}
- **THEN** envelope 中 `_rto` SHALL 为 `{"queue":"callback-q"}`

#### Scenario: Publish with Reply nil and no TraceContext and no Metadata
- **WHEN** Task.Reply 为 nil，ctx 无 TraceContext，Metadata 为空
- **THEN** 实际写入 asynq 的 payload SHALL 为原始内容（不包装 envelope）

### Requirement: Asynq Consumer unwraps envelope and restores context
asynq Consumer 实现在收到消息时 SHALL：
1. 尝试解析 trace envelope（检查 `_v == 1`）
2. 若为 envelope 格式：
   a. 提取 trace 字段，恢复 TraceContext 到 ctx
   b. 从 `_rto` 字段还原 Task.Reply：先尝试解析为 JSON 对象 `{"queue":"..."}` 得到 `*ReplySpec`；若失败则尝试将值作为纯字符串解析，构造 `&ReplySpec{Queue: stringValue}`（兼容旧格式）
   c. 从 `_meta` 字段还原 Task.Metadata
   d. 将 `payload` 字段作为 Task.Payload 传递给 handler
3. 若非 envelope 格式，直接将整个 payload 传递，Task.Reply 为 nil，Metadata 为空

#### Scenario: Consumer receives envelope with ReplySpec JSON
- **WHEN** envelope `_rto = {"queue":"callback-q"}`
- **THEN** handler 收到的 Task.Reply SHALL 为 `&ReplySpec{Queue: "callback-q"}`

#### Scenario: Consumer receives envelope with legacy string _rto
- **WHEN** envelope `_rto = "callback-q"`（旧格式纯字符串）
- **THEN** handler 收到的 Task.Reply SHALL 为 `&ReplySpec{Queue: "callback-q"}`（兼容回退）

#### Scenario: Consumer receives envelope without _rto
- **WHEN** envelope 中 `_rto` 字段缺失
- **THEN** handler 收到的 Task.Reply SHALL 为 nil

### Requirement: Asynq Consumer delivers full Task to handler
asynq Consumer 在调用 HandlerFunc 时 SHALL 构造完整的 `Task` 结构体传递给 handler，包含：
- `Type` — 从 asynq task type 读取
- `Payload` — 解包后的业务载荷
- `Queue` — 消息所在队列名称
- `Reply` — 从 envelope `_rto` 字段恢复为 `*ReplySpec`；非 envelope 消息为 nil
- `Metadata` — 从 envelope `_meta` 字段恢复；非 envelope 消息为空 map

#### Scenario: Handler receives Task with Reply populated
- **WHEN** producer 发送 Task{Reply: &ReplySpec{Queue: "callback-q"}, Metadata: {"tenant": "acme"}}
- **THEN** handler 收到的 Task.Reply SHALL 为 &ReplySpec{Queue: "callback-q"}，Task.Metadata SHALL 为 {"tenant": "acme"}
