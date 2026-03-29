# TaskQueue 模块

> 包路径：`github.com/jinleili-zz/nsp-platform/taskqueue`

`taskqueue` 现在是一个纯 broker 抽象层，不再包含 workflow 编排、数据库状态机或 RocketMQ 实现。仓库内当前提供的生产级后端只有 `asynqbroker`。

## 模块定位

- 统一任务消息模型：`Task`
- 统一发布接口：`Broker`
- 统一消费接口：`Consumer`
- 统一巡检接口：`Inspector` 四层体系
- 基于 `trace` 模块透传链路上下文
- 通过 `ReplySpec` 支持 worker 把结果回发到指定队列

不再提供以下能力：

- `Engine`
- `WorkflowDefinition`
- `TaskPayload`
- `TaskResult`
- `CallbackPayload`
- `HandleRaw`
- `rocketmqbroker/`

## 核心类型

```go
type Priority int

const (
    PriorityLow      Priority = 1
    PriorityNormal   Priority = 3
    PriorityHigh     Priority = 6
    PriorityCritical Priority = 9
)

type ReplySpec struct {
    Queue string
}

type Task struct {
    Type     string
    Payload  []byte
    Queue    string
    Reply    *ReplySpec
    Priority Priority
    Metadata map[string]string
}

type TaskInfo struct {
    BrokerTaskID string
    Queue        string
}
```

说明：

- `Payload` 是 broker 不解析的业务数据，通常直接放 JSON。
- `Reply` 为 `nil` 表示 fire-and-forget。
- `Priority` 在当前实现中只是保留字段。
  现在的 asynq 适配并不会自动根据 `Priority` 选队列，业务方应自己决定 `Task.Queue`，消费者再通过 `ConsumerConfig.Queues` 权重体现优先级。

## 接口

```go
type Broker interface {
    Publish(ctx context.Context, task *Task) (*TaskInfo, error)
    Close() error
}

type HandlerFunc func(ctx context.Context, task *Task) error

type Consumer interface {
    Handle(taskType string, handler HandlerFunc)
    Start(ctx context.Context) error
    Stop() error
}
```

## Asynq 实现

包路径：`github.com/jinleili-zz/nsp-platform/taskqueue/asynqbroker`

### 发布行为

`asynqbroker.Broker.Publish` 会在以下任一条件满足时，自动把 `Task.Payload` 包装成内部 envelope：

- `ctx` 中存在 `trace.TraceContext`
- `Task.Reply != nil`
- `len(Task.Metadata) > 0`

内部 envelope 字段：

- `_v`: 固定为 `1`
- `_tid`: TraceID
- `_sid`: 发布方 SpanId
- `_smpl`: Sampled
- `_rto`: `ReplySpec` 的 JSON
- `_meta`: `Task.Metadata`
- `payload`: 原始业务 payload

如果三者都不存在，则直接发送原始 payload，兼容旧消息。

### 消费行为

`asynqbroker.Consumer` 在消费时会：

1. 解包 envelope
2. 恢复 trace 上下文
3. 还原 `ReplySpec`
4. 还原 `Metadata`
5. 构造完整 `*taskqueue.Task` 传给 handler

因此 handler 看到的总是 broker 级模型，而不是 asynq 私有类型。

## 快速开始

### 发布任务

```go
package main

import (
    "context"
    "encoding/json"

    "github.com/hibiken/asynq"

    "github.com/jinleili-zz/nsp-platform/taskqueue"
    "github.com/jinleili-zz/nsp-platform/taskqueue/asynqbroker"
)

func main() {
    broker := asynqbroker.NewBroker(asynq.RedisClientOpt{Addr: "127.0.0.1:6379"})
    defer broker.Close()

    body, _ := json.Marshal(map[string]any{
        "order_id": "ORD-001",
        "user_id":  "U-001",
    })

    _, _ = broker.Publish(context.Background(), &taskqueue.Task{
        Type:    "order:create",
        Payload: body,
        Queue:   "orders:high",
        Reply:   &taskqueue.ReplySpec{Queue: "orders:callback"},
        Metadata: map[string]string{
            "tenant": "acme",
        },
    })
}
```

### 消费任务

```go
package main

import (
    "context"
    "encoding/json"

    "github.com/hibiken/asynq"

    "github.com/jinleili-zz/nsp-platform/taskqueue"
    "github.com/jinleili-zz/nsp-platform/taskqueue/asynqbroker"
)

func main() {
    consumer := asynqbroker.NewConsumer(asynq.RedisClientOpt{Addr: "127.0.0.1:6379"}, asynqbroker.ConsumerConfig{
        Concurrency: 4,
        Queues: map[string]int{
            "orders:high": 30,
            "orders:low":  10,
        },
    })

    consumer.Handle("order:create", func(ctx context.Context, task *taskqueue.Task) error {
        var payload map[string]any
        if err := json.Unmarshal(task.Payload, &payload); err != nil {
            return err
        }

        // task.Reply / task.Metadata / trace 都已恢复完成
        _ = payload
        return nil
    })

    _ = consumer.Start(context.Background())
}
```

## Inspector

`taskqueue` 保留四层巡检接口体系：

- `Inspector`
- `TaskReader`
- `TaskController`
- `QueueController`

当前 `asynqbroker.Inspector` 实现了全部四层。调用方通过接口断言获取可选能力。

```go
inspector := asynqbroker.NewInspector(asynq.RedisClientOpt{Addr: "127.0.0.1:6379"})
defer inspector.Close()

queues, _ := inspector.Queues(ctx)
stats, _ := inspector.GetQueueStats(ctx, "orders:high")

if tr, ok := inspector.(taskqueue.TaskReader); ok {
    detail, _ := tr.GetTaskInfo(ctx, "orders:high", "task-id")
    _ = detail
}
```

`TaskDetail.Payload` 返回的是已解包后的业务 payload，不包含内部 trace envelope。

## 迁移说明

旧接口与新接口的对应关系：

- `HandlerFunc(ctx, *TaskPayload) (*TaskResult, error)` -> `HandlerFunc(ctx, *Task) error`
- `payload.Params` -> `task.Payload`
- `payload.TaskType` -> `task.Type`
- 回调队列字符串/旧 callback 协议 -> `task.Reply`
- `HandleRaw` -> 直接用普通 `Handle` 消费 broker 级消息
- workflow 编排 -> 已移出 `taskqueue`

## 当前约束

- 仓库内只保留 `asynqbroker`
- 不再提供 workflow API
- 不再提供 RocketMQ 实现
- `Priority` 不会自动映射到队列

如果需要新的 broker 后端，应复用 `Task` / `Broker` / `Consumer` / `Inspector` 这些公共接口，而不是重新引入 workflow 语义。
