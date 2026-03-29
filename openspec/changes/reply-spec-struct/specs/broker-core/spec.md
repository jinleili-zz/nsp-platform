## MODIFIED Requirements

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
