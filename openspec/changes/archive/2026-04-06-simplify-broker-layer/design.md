## Context

当前 `taskqueue` 包承担了两个职责：
1. **Broker 层**：消息投递抽象（Broker/Consumer/Inspector 接口 + asynq/rocketmq 实现）
2. **Workflow 层**：编排状态机（Engine + PostgresStore + 数据库迁移）

两层通过 `TaskPayload`（含 TaskID/ResourceID 业务字段）紧耦合。consumer 实现（asynqbroker/consumer.go:61-74）硬编码了 `task_id`/`resource_id`/`task_params` 的 JSON 反序列化，使得非 workflow 场景下使用 broker 需要伪造这些字段或使用 `HandleRaw` 绕过。

团队决定去掉 workflow 层，将 broker 层打造为纯粹的消息投递 SDK。同时增加 `Reply *ReplySpec` 回调队列能力（使用结构体而非 string，为未来扩展预留空间），解决多 producer 场景下 worker 需要将结果投递到正确队列的问题。

## Goals / Non-Goals

**Goals:**
- broker 层成为零业务假设的通用消息投递抽象，handler 接收完整 Task 而非预解析的 TaskPayload
- Task 结构体增加 `Reply *ReplySpec` 字段（指针类型，nil 表示 fire-and-forget），作为平台级属性在 envelope 中透传
- 保留 trace ID 透传（通过 asynq trace envelope 机制）
- 保留四层 Inspector 接口体系
- 删除所有 workflow/编排/PostgreSQL 相关代码
- 删除 rocketmqbroker 实现代码，减少 go.mod 依赖

**Non-Goals:**
- 不实现新的 workflow/编排框架
- 不新增 RocketMQ/Kafka 等其他 broker 实现（接口保持开放，但不在此次变更中提供）
- 不改动 trace 模块本身（`trace/` 包保持不变）
- 不改动 Inspector 接口定义及数据模型（QueueStats/TaskDetail/WorkerInfo 等保持不变）

## Decisions

### Decision 1: HandlerFunc 签名改为 `func(ctx, *Task) error`

**选择**: handler 直接接收 `*Task`，业务层自行解析 `Payload []byte`

**替代方案**:
- A) 保留 `TaskPayload` 但去掉 TaskID/ResourceID 字段 → 仍然强制了一层多余的解析
- B) handler 签名为 `func(ctx, []byte) error` → 丢失了 Type/Queue/Reply/Metadata 等有用信息

**理由**: `*Task` 既是发送侧的输入也是接收侧的输出，一个结构体贯穿生产-消费全链路，概念统一。handler 可以直接读取 Reply 决定回调目标，也可以读取 Metadata 获取扩展信息，无需额外的上下文传递。

### Decision 2: 使用 `*ReplySpec` 结构体而非 `string`

**选择**: `Task.Reply *ReplySpec`，ReplySpec 当前只有 `Queue string` 字段，nil 表示不需要回调

**替代方案**:
- A) `Task.ReplyTo string`，空字符串表示 fire-and-forget
- B) 将回调队列名放入 `Metadata["reply_to"]`

**理由**:
- 结构体形式可在不破坏接口的情况下添加新字段（如 routing key、回调超时等）
- 指针的 nil 零值语义比空字符串更清晰，与 Go 惯例一致（optional 字段用指针）
- Reply 是消息路由信息，与 Queue 同层级，不是业务数据，应作为一等字段
- envelope 中 `_rto` 存储 ReplySpec JSON 对象 `{"queue":"name"}`

### Decision 3: envelope 扩展 `_rto` 和 `_meta` 字段

**选择**: 扩展现有 trace envelope 结构体，新增 `_rto`（ReplySpec JSON）和 `_meta`（Metadata map）字段

**理由**:
- envelope 已经是 asynq 实现对 payload 的唯一包装层，扩展此层自然且无额外开销
- 触发 envelope 包装的条件扩展为：TraceContext 存在 OR Reply 非 nil OR Metadata 非空
- consumer 解包 envelope 时同时恢复 trace context、Reply 和 Metadata，逻辑集中
- `_rto` 是全新字段，不存在旧格式兼容问题；非 envelope 格式的旧消息自动降级为 Reply=nil

### Decision 4: 删除 rocketmqbroker 而非保留空壳

**选择**: 完整删除 `taskqueue/rocketmqbroker/` 目录

**替代方案**: 保留目录但清空实现，只留 TODO 注释

**理由**:
- 空壳目录会产生编译依赖（import path 存在即引入 go.mod 依赖）
- Broker/Consumer/Inspector 接口已保证实现可插拔，未来可在独立 module 中提供 RocketMQ 实现
- 减少 go.mod 中 rocketmq-client-go 及其传递依赖

### Decision 5: 物理删除 workflow 代码

**选择**: 删除 engine.go/store.go/pg_store.go/engine_test.go/migrations/

**理由**:
- 团队已明确不再使用 workflow 编排
- 保留 deprecated 代码会增加认知负担和维护成本
- 业务代码迁移为直接使用 broker 层 API

## Risks / Trade-offs

**[风险] 破坏性变更影响现有 consumer 代码** → 所有使用 `HandlerFunc(ctx, *TaskPayload)` 签名的代码需要迁移。迁移方式：将 `TaskPayload.Params` 的使用改为 `Task.Payload`，将 `TaskPayload.TaskID` 的使用改为从 Payload 中自行解析或通过 Metadata 传递。

**[风险] 旧格式消息兼容性** → 部署期间新旧版本共存时，旧 producer 发送的消息不含 `_rto`/`_meta` 字段。consumer 的 envelope 解包逻辑已处理此情况：字段缺失时 Reply=nil、Metadata=nil。

**[取舍] 删除 RocketMQ 实现** → 短期内无法使用 RocketMQ 作为 broker。但团队目前只使用 Redis/asynq，且接口保持开放。

**[取舍] handler 需要自行解析 Payload** → 相比之前的 `TaskPayload` 预解析，业务代码多了一步 `json.Unmarshal`。但换来的是 broker 层零业务假设，payload 格式完全由业务方控制。
