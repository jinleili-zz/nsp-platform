# TaskQueue Priority Demo

基于 TaskQueue 的优先级队列示例 Demo，包含生产者（Producer）、消费者（Consumer）、任务发送队列和结果回调队列。

## 功能特性

- **优先级队列**：支持高/中/低三个优先级队列，消费者优先消费高优先级任务
- **任务类型**：支持邮件发送、图片处理、数据导出、报表生成、通知发送等多种任务
- **结果回调**：任务执行完成后自动发送结果到回调队列
- **分布式追踪**：集成 trace 模块，支持全链路追踪
- **队列监控**：实时显示队列统计信息

## 目录结构

```
taskqueue-priority-demo/
├── cmd/
│   ├── producer/          # 生产者入口
│   │   └── main.go
│   └── consumer/          # 消费者入口
│       └── main.go
├── internal/
│   ├── config/            # 共享配置
│   │   └── config.go
│   └── handler/           # 任务处理器
│       └── handler.go
└── README.md
```

## 队列命名规范

遵循 Redis 命名习惯（小写、冒号分隔、语义清晰）：

| 队列类型 | 队列名称 |
|---------|---------|
| 高优先级任务队列 | `taskqueue:business:task:send:priority:high` |
| 中优先级任务队列 | `taskqueue:business:task:send:priority:medium` |
| 低优先级任务队列 | `taskqueue:business:task:send:priority:low` |
| 结果回调队列 | `taskqueue:business:result:callback` |

## 快速开始

### 前置条件

- Go 1.21+
- Redis 服务（默认地址：127.0.0.1:6379）

### 启动消费者

```bash
cd examples/taskqueue-priority-demo
go run cmd/consumer/main.go
```

消费者启动后会：
1. 监听三个优先级的任务队列
2. 按权重优先消费高优先级任务
3. 执行任务并发送结果到回调队列

### 启动生产者

```bash
cd examples/taskqueue-priority-demo
go run cmd/producer/main.go
```

生产者启动后会：
1. 发送示例任务到不同优先级队列
2. 启动回调消费者接收任务执行结果
3. 定时显示队列统计信息

## 配置说明

### 环境变量

| 变量名 | 说明 | 默认值 |
|-------|------|--------|
| `REDIS_ADDR` | Redis 服务器地址 | `127.0.0.1:6379` |
| `HOSTNAME` | 实例标识（K8s Pod 名称） | 自动获取 |

### 队列权重配置

消费者按权重优先消费队列中的任务：

```go
QueueWeights = map[string]int{
    QueueTaskHigh:   30,  // 高优先级权重最高
    QueueTaskMedium: 20,  // 中优先级
    QueueTaskLow:    10,  // 低优先级
}
```

## 任务类型

### 1. 邮件发送 (`email:send`)

```go
payload := map[string]interface{}{
    "to":      "user@example.com",
    "subject": "Test Email",
    "body":    "This is a test email",
}
```

### 2. 图片处理 (`image:process`)

```go
payload := map[string]interface{}{
    "image_url": "https://example.com/image.jpg",
    "operation": "resize",
    "width":     800,
    "height":    600,
}
```

### 3. 数据导出 (`data:export`)

```go
payload := map[string]interface{}{
    "format":    "csv",
    "date_from": "2024-01-01",
    "date_to":   "2024-12-31",
    "user_id":   "user_001",
}
```

### 4. 报表生成 (`report:generate`)

```go
payload := map[string]interface{}{
    "report_type": "monthly",
    "month":       "2024-12",
    "department":  "sales",
}
```

### 5. 通知发送 (`notification:send`)

```go
payload := map[string]interface{}{
    "channel":   "sms",
    "recipient": "+8613800138000",
    "content":   "Your verification code is 123456",
}
```

## 任务生命周期

```
┌─────────────┐    ┌─────────────────────────────────────┐    ┌─────────────┐
│  Producer   │───▶│  taskqueue:business:task:send:*     │───▶│  Consumer   │
│  (发送任务)  │    │  (按优先级路由到不同队列)              │    │  (执行任务)  │
└─────────────┘    └─────────────────────────────────────┘    └──────┬──────┘
       ▲                                                             │
       │                                                             │
       │    ┌─────────────────────────────────────┐                  │
       │    │  taskqueue:business:result:callback │◀─────────────────┘
       └───▶│  (任务执行结果回调)                   │
            └─────────────────────────────────────┘
```

## 扩展示例

### 自定义任务处理器

```go
import (
    "nsp/examples/taskqueue-priority-demo/internal/handler"
    "nsp/taskqueue"
)

// 定义自定义处理器
func CustomHandler(ctx context.Context, payload *taskqueue.TaskPayload) *taskqueue.TaskResult {
    // 处理任务逻辑
    return &taskqueue.TaskResult{
        Success: true,
        Message: "custom task processed",
        Data:    map[string]interface{}{"result": "ok"},
    }
}

// 注册处理器
handlers := map[string]handler.TaskHandler{
    "custom:task": CustomHandler,
}
```

### 发送带超时的任务

```go
result, err := producer.SendTaskWithTimeout(
    ctx,
    "report:generate",
    payload,
    "high",
    60*time.Second,  // 超时时间
)
```

## 运行示例输出

### 消费者输出

```
[Consumer] Starting TaskQueue Priority Demo Consumer...

========== Consumer Configuration ==========
Instance ID:     my-hostname
Redis Address:   127.0.0.1:6379
Concurrency:     5

Queue Weights (Priority Order):
  taskqueue:business:task:send:priority:high   weight=30
  taskqueue:business:task:send:priority:medium weight=20
  taskqueue:business:task:send:priority:low    weight=10

Registered Task Handlers:
  - email:send
  - image:process
  - data:export
  - report:generate
  - notification:send
============================================

[Consumer] Starting with concurrency=5...
[Consumer] Processing task: type=email:send, id=xxx, queue=high, trace_id=xxx
[Consumer] Task completed: type=email:send, id=xxx, success=true, duration=250ms
```

### 生产者输出

```
[Producer] Starting TaskQueue Priority Demo Producer...
[Producer] Task sent: type=email:send, queue=high, priority=high, task_id=xxx
[Producer] Task sent: type=image:process, queue=medium, priority=medium, task_id=xxx
[Producer] All tasks sent successfully

========== Queue Statistics ==========
Queue: taskqueue:business:task:send:priority:high   | Pending:   3 | Active:   0 | Completed:   0 | Failed:   0
Queue: taskqueue:business:task:send:priority:medium | Pending:   5 | Active:   0 | Completed:   0 | Failed:   0
Queue: taskqueue:business:task:send:priority:low    | Pending:   3 | Active:   0 | Completed:   0 | Failed:   0
Queue: taskqueue:business:result:callback           | Pending:   0 | Active:   0 | Completed:   0 | Failed:   0
======================================
```

## 注意事项

1. **Redis 连接**：确保 Redis 服务已启动并可访问
2. **并发配置**：根据实际业务需求调整 `Concurrency` 参数
3. **队列权重**：权重越高，消费者从该队列获取任务的概率越大
4. **优雅关闭**：消费者支持优雅关闭，会等待正在执行的任务完成
