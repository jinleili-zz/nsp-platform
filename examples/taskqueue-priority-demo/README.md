# TaskQueue Priority Demo

这个示例展示当前 `taskqueue` 设计下，如何用 `asynqbroker` 做多队列权重消费。

它不再依赖旧的 `TaskPayload` / `TaskResult` / workflow API，消费者直接处理 `*taskqueue.Task`。

## 演示内容

- Producer 直接发送业务 JSON payload
- Consumer 根据 `Task.Type` 路由 handler
- 三个业务队列按权重消费
- Inspector 查询队列状态

## 队列

| 作用 | 队列名 |
|---|---|
| 高优先级任务 | `taskqueue:business:task:send:priority:high` |
| 中优先级任务 | `taskqueue:business:task:send:priority:medium` |
| 低优先级任务 | `taskqueue:business:task:send:priority:low` |
| 预留回调队列 | `taskqueue:business:result:callback` |

说明：

- 当前 demo 重点展示优先级消费。
- 回调队列常量仍保留在配置里，但这个 demo 本身不依赖 reply 流程。

## 快速开始

准备 Redis：

```bash
docker run --rm -p 6379:6379 redis:7
```

启动 consumer：

```bash
cd examples/taskqueue-priority-demo
REDIS_ADDR=127.0.0.1:6379 go run ./cmd/consumer
```

启动 producer：

```bash
cd examples/taskqueue-priority-demo
REDIS_ADDR=127.0.0.1:6379 go run ./cmd/producer
```

## 代码结构

```text
examples/taskqueue-priority-demo/
├── cmd/consumer/          消费端入口
├── cmd/producer/          生产端入口
├── internal/config/       队列和运行参数
└── internal/handler/      任务处理函数
```

## 当前处理模型

handler 签名：

```go
func(ctx context.Context, task *taskqueue.Task) error
```

示例：

```go
consumer.Handle(config.TaskTypeEmailSend, func(ctx context.Context, task *taskqueue.Task) error {
    var payload map[string]any
    if err := json.Unmarshal(task.Payload, &payload); err != nil {
        return err
    }
    return nil
})
```

## 优先级如何体现

这个 demo 的优先级不是靠 `Task.Priority` 自动生效，而是靠：

1. producer 选择目标队列
2. consumer 的 `Queues` 权重配置

例如：

```go
Queues: map[string]int{
    config.QueueTaskHigh:   30,
    config.QueueTaskMedium: 20,
    config.QueueTaskLow:    10,
}
```

这意味着高优先级队列会被更频繁地拉取。

## 适合拿来参考什么

- 如何构造 `taskqueue.Task`
- 如何直接消费 `*taskqueue.Task`
- 如何用 asynq 的多队列权重机制表达优先级
- 如何用 Inspector 查看队列状态
