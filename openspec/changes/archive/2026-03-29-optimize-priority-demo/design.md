## Context

`taskqueue-priority-demo` 是 NSP 平台中演示多优先级任务队列的示例。当前实现包含 `TaskStore`（内存任务状态管理）、`TaskManager`（任务生命周期管理含重试）、`CallbackSender`（回调发送）等 workflow 机制，这些代码与 broker 层演示无关，且使用 `Metadata` 传递业务字段 `task_id` 不符合职责分离原则。

demo 应聚焦展示：通过 `asynqbroker.Broker` 发布任务到不同优先级队列、通过 `asynqbroker.Consumer` 按权重消费、通过 `asynqbroker.Inspector` 查询队列状态。

## Goals / Non-Goals

**Goals:**
- 聚焦演示 `asynqbroker` 三大核心能力：Broker（发布）、Consumer（消费）、Inspector（查询）
- `task_id` 放入 payload，不使用 Metadata 传递业务数据
- consumer 处理任务后直接打印结果，无回调机制
- producer 发送完任务后用 inspector 展示队列统计

**Non-Goals:**
- 不演示任务状态管理（TaskStore/TaskManager）
- 不演示回调机制（CallbackSender/HandleCallback）
- 不演示重试逻辑
- 不引入数据库或持久化存储

## Decisions

### 移除 store 包和 TaskManager
删除 `store/store.go` 整个包，producer 直接使用 `broker.Publish` 发送任务。consumer 直接处理任务。

**原因**：demo 目标是展示 broker 能力，workflow 机制会让用户误以为 taskqueue 库包含状态管理。

### task_id 放入 payload
所有任务的 `task_id` 作为 payload JSON 的一个字段传递，不再使用 `taskqueue.Task.Metadata`。

**原因**：`task_id` 是业务标识，属于业务数据，应放 payload。Metadata 留给平台级扩展（如 trace 信息）。

### 移除 Reply/Callback 机制
不再在 `taskqueue.Task` 中设置 `Reply` 字段，consumer 处理完任务后直接 log 结果。

**原因**：回调路由是 workflow 级别的机制，不属于 broker 层演示范畴。

### 新增 Inspector 示例
producer 发送完所有任务后，使用 `asynqbroker.NewInspector` 查询各队列的统计信息（Pending、Active、Completed、Failed）。

**原因**：Inspector 是 asynqbroker 的三大核心组件之一，当前 demo 缺少其使用示例。

### 队列常量直接在 main 中定义
移除 store 包后，队列名称常量在 producer 和 consumer 的 main 中直接定义（或各文件顶部 const 块）。

**原因**：demo 代码量小，无需额外包来管理常量。两个 main 文件中的队列名称保持一致即可。

## Risks / Trade-offs

- [风险] 移除回调机制后无法演示请求-响应模式 → 可接受，demo 目标是 broker 层能力，非 workflow
- [取舍] 两个 main 文件中重复定义队列常量 → demo 代码量小，重复可接受，比引入额外包更清晰
- [取舍] 不演示重试逻辑 → 重试属于 asynq 库自带能力，demo 不需要额外封装
