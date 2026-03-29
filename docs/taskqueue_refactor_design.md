# TaskQueue 简化设计

本文档描述当前已经落地的 `taskqueue` 设计，而不是历史方案。

## 背景

旧版 `taskqueue` 同时承担两类职责：

- broker 抽象
- workflow 编排

这导致消息消费接口被固定业务结构绑死，表现为：

- `HandlerFunc` 依赖 `TaskPayload`
- consumer 需要理解 `task_id/resource_id/task_params`
- broker 层无法作为纯通用消息 SDK 复用

本次简化后的目标是把 `taskqueue` 收敛为纯 broker 层。

## 当前设计结论

### 1. 统一消息模型

公共消息模型只有 `Task`：

```go
type Task struct {
    Type     string
    Payload  []byte
    Queue    string
    Reply    *ReplySpec
    Priority Priority
    Metadata map[string]string
}
```

含义如下：

- `Type`: handler 路由键
- `Payload`: broker 不解析的业务数据
- `Queue`: 投递目标
- `Reply`: 回调路由
- `Priority`: 业务层可读的优先级标记
- `Metadata`: 额外元数据

### 2. handler 只接收 broker 级任务

```go
type HandlerFunc func(ctx context.Context, task *Task) error
```

不再返回 `TaskResult`，不再依赖 `TaskPayload`。

这样做的结果是：

- handler 直接看到 `Reply`
- handler 直接看到 `Metadata`
- payload 的解析完全由业务自己决定

### 3. Reply 走显式字段，不走隐式上下文

```go
type ReplySpec struct {
    Queue string
}
```

`ReplySpec` 是平台级路由信息，不再藏在旧 callback 协议里，也不塞进 `context.Context`。

### 4. trace / reply / metadata 统一走 asynq envelope

`asynqbroker` 使用内部 envelope 透传：

- trace
- `ReplySpec`
- `Task.Metadata`

字段为：

- `_v`
- `_tid`
- `_sid`
- `_smpl`
- `_rto`
- `_meta`
- `payload`

消费端自动解包，业务 handler 看到的永远是原始业务 payload。

### 5. workflow 层彻底移除

以下内容不再属于 `taskqueue`：

- `Engine`
- `WorkflowDefinition`
- `StepDefinition`
- workflow 状态查询
- callback 状态机
- PostgreSQL workflow store

### 6. RocketMQ 实现不再随仓库维护

公共接口仍然允许后续增加新的 broker 后端，但当前仓库只保留 `asynqbroker`。

## 非目标

当前设计不做这些事情：

- 不在 `taskqueue` 内恢复 workflow
- 不自动根据 `Priority` 计算队列
- 不在公共接口中暴露后端私有消息类型
- 不为旧 `HandleRaw` 提供兼容入口

## 当前使用方式

生产端：

1. 构造 `Task`
2. 选择 `Queue`
3. 可选设置 `Reply`
4. 调用 `Broker.Publish`

消费端：

1. 用 `Consumer.Handle` 注册 `task.Type`
2. 在 handler 中解析 `task.Payload`
3. 根据 `task.Reply` 决定是否回发结果

巡检端：

1. 用 `Inspector` 查询队列和 worker
2. 通过接口断言获取任务级/队列级控制能力

## 迁移影响

从旧模型迁移到当前模型的主要变化：

- `TaskPayload` -> `Task`
- `TaskResult` -> 直接返回 `error`
- `HandleRaw` -> 删除
- workflow API -> 删除
- RocketMQ 实现 -> 删除

这套设计的重点不是“更复杂的消息协议”，而是把 broker 层从 workflow 业务假设里彻底解耦。
