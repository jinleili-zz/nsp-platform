## Context

在 `simplify-broker-layer` 变更中，`Task.ReplyTo` 被设计为 `string` 类型，存放回调队列名称。这满足当前需求，但随着平台演进，回调路由可能需要携带更多信息（例如：routing key、消息优先级、回调超时等），届时修改 `string` → 结构体将是破坏性变更，影响面广。

当前 `simplify-broker-layer` 还在提案阶段，尚未实施，是引入此变更的最低成本时机。

## Goals / Non-Goals

**Goals:**
- 将 `Task.ReplyTo string` 替换为 `Task.Reply *ReplySpec`，`ReplySpec` 当前只有 `Queue string` 一个字段
- nil `Reply` 指针语义等同于原来的空字符串（fire-and-forget）
- asynq envelope 的 `_rto` 字段适配 `ReplySpec`，并向后兼容旧的纯字符串格式

**Non-Goals:**
- 不在本次变更中为 `ReplySpec` 添加任何扩展字段（保持最小化）
- 不改动 Broker / Consumer / Inspector 接口签名
- 不影响 trace 透传逻辑

## Decisions

### Decision 1: 使用指针类型 `*ReplySpec` 而非值类型 `ReplySpec`

**选择**：`Task.Reply *ReplySpec`，nil 表示不需要回调

**替代方案**：值类型 `ReplySpec`，用 `Queue == ""` 表示 fire-and-forget

**理由**：指针的 nil 零值语义比空字符串更清晰，避免调用方需要判断 `task.Reply.Queue != ""`；同时与 Go 惯例一致（optional 字段用指针）。

### Decision 2: envelope `_rto` 字段序列化为 JSON 对象而非字符串

**选择**：`_rto` 存储 `{"queue":"name"}` JSON 对象

**兼容策略**：unwrap 时先尝试解析为 JSON 对象，失败则尝试解析为纯字符串（兼容旧消息），将字符串值作为 `Queue` 字段填入

**理由**：结构化存储便于未来添加字段，向后兼容策略确保滚动部署时新旧 producer/consumer 混跑不出错。

## Risks / Trade-offs

**[风险] 与 simplify-broker-layer 的顺序依赖** → 本变更需在 simplify-broker-layer 实施前或同批次实施，否则会出现中间状态。迁移方案：将两个变更合并为一个实施批次，或先实施本变更再实施 simplify-broker-layer。

**[取舍] 多一层指针解引用** → 调用方从 `task.ReplyTo` 改为 `task.Reply.Queue`，略显冗长。但语义更明确，且为未来扩展铺路，值得。

## Migration Plan

1. 修改 `taskqueue/task.go`：新增 `ReplySpec`，`Task.ReplyTo` → `Task.Reply *ReplySpec`
2. 更新 `asynqbroker/trace_envelope.go`：wrap 时序列化 `ReplySpec` 为 JSON 对象；unwrap 时兼容旧字符串格式
3. 更新示例代码中所有 `Task.ReplyTo = "..."` 为 `Task.Reply = &taskqueue.ReplySpec{Queue: "..."}`
4. 更新 `simplify-broker-layer` 变更中相关 spec 文本

## Open Questions

（无）
