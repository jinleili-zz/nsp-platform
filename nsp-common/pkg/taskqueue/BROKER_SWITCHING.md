# 如何切换 TaskQueue 的消息队列实现

TaskQueue 框架设计为支持多种消息队列，目前支持：
- **Asynq** (Redis 后端)
- **RocketMQ**

切换消息队列实现非常简单，只需修改 Broker 和 Consumer 的创建方式。

## 方案对比

| 特性 | Asynq (Redis) | RocketMQ |
|-----|--------------|----------|
| **部署复杂度** | ⭐⭐ 简单 | ⭐⭐⭐⭐ 中等 |
| **性能** | 高 (10万+ TPS) | 非常高 (百万+ TPS) |
| **消息可靠性** | 高 (基于 Redis 持久化) | 非常高 (事务消息、持久化) |
| **功能** | 基础 (优先级、延时、重试) | 丰富 (顺序消息、事务消息、过滤) |
| **适用场景** | 中小规模、快速开发 | 大规模、高可靠性要求 |
| **语言支持** | Go only | 多语言 (Java/Go/C++/Python) |

## 从 Asynq 切换到 RocketMQ

### 1. 修改依赖

```go
// 原来 (Asynq)
import (
    "github.com/hibiken/asynq"
    "github.com/paic/nsp-common/pkg/taskqueue/asynqbroker"
)

// 改为 (RocketMQ)
import (
    "github.com/paic/nsp-common/pkg/taskqueue/rocketmqbroker"
)
```

### 2. 修改 Broker 创建

```go
// 原来 (Asynq)
redisOpt := asynq.RedisClientOpt{Addr: "127.0.0.1:6379"}
broker := asynqbroker.NewBroker(redisOpt)

// 改为 (RocketMQ)
broker, err := rocketmqbroker.NewBroker(&rocketmqbroker.BrokerConfig{
    NameServer: "127.0.0.1:9876",
    GroupName:  "my_producer_group",
    RetryTimes: 2,
})
if err != nil {
    log.Fatal(err)
}
```

### 3. 修改 Consumer 创建

```go
// 原来 (Asynq)
consumer := asynqbroker.NewConsumer(redisOpt, asynqbroker.ConsumerConfig{
    Concurrency: 10,
    Queues: map[string]int{
        "tasks":      5,
        "tasks_high": 8,
    },
    StrictPriority: true,
})

// 改为 (RocketMQ)
consumer, err := rocketmqbroker.NewConsumer(&rocketmqbroker.ConsumerConfig{
    NameServer: "127.0.0.1:9876",
    GroupName:  "my_consumer_group",
    Queues: map[string]string{
        "tasks":      "*",  // "*" 表示订阅所有 tag
        "tasks_high": "*",
    },
    Concurrency:       10,
    MaxReconsumeTimes: 3,
})
if err != nil {
    log.Fatal(err)
}
```

### 4. Handler 注册 - 保持不变

```go
// 两种实现的 Handler 注册方式完全一致，无需修改
consumer.Handle("send_email", func(ctx context.Context, payload *taskqueue.TaskPayload) (*taskqueue.TaskResult, error) {
    // 业务逻辑
    return &taskqueue.TaskResult{}, nil
})
```

### 5. Engine 创建 - 保持不变

```go
// Engine 创建方式不变，只需传入不同的 broker
engine, err := taskqueue.NewEngine(&taskqueue.Config{
    DSN:           pgDSN,
    CallbackQueue: "callbacks",
    QueueRouter:   taskqueue.DefaultQueueRouter,
}, broker) // broker 可以是 asynqbroker 或 rocketmqbroker
```

## 完整示例对比

### Asynq 示例

```go
package main

import (
    "github.com/hibiken/asynq"
    "github.com/paic/nsp-common/pkg/taskqueue"
    "github.com/paic/nsp-common/pkg/taskqueue/asynqbroker"
)

func main() {
    // Redis 配置
    redisOpt := asynq.RedisClientOpt{Addr: "127.0.0.1:6379"}
    
    // 创建 Broker
    broker := asynqbroker.NewBroker(redisOpt)
    defer broker.Close()
    
    // 创建 Engine
    engine, _ := taskqueue.NewEngine(&taskqueue.Config{
        DSN:           "postgres://...",
        CallbackQueue: "callbacks",
    }, broker)
    
    // 创建 Consumer
    consumer := asynqbroker.NewConsumer(redisOpt, asynqbroker.ConsumerConfig{
        Concurrency: 10,
        Queues:      map[string]int{"tasks": 5},
    })
    
    // 注册 Handler (代码完全相同)
    consumer.Handle("my_task", myHandler)
    
    // 启动 Consumer
    consumer.Start(ctx)
}
```

### RocketMQ 示例

```go
package main

import (
    "github.com/paic/nsp-common/pkg/taskqueue"
    "github.com/paic/nsp-common/pkg/taskqueue/rocketmqbroker"
)

func main() {
    // RocketMQ 配置
    brokerCfg := &rocketmqbroker.BrokerConfig{
        NameServer: "127.0.0.1:9876",
        GroupName:  "my_producer_group",
    }
    
    // 创建 Broker
    broker, _ := rocketmqbroker.NewBroker(brokerCfg)
    defer broker.Close()
    
    // 创建 Engine (代码完全相同)
    engine, _ := taskqueue.NewEngine(&taskqueue.Config{
        DSN:           "postgres://...",
        CallbackQueue: "callbacks",
    }, broker)
    
    // 创建 Consumer
    consumer, _ := rocketmqbroker.NewConsumer(&rocketmqbroker.ConsumerConfig{
        NameServer:  "127.0.0.1:9876",
        GroupName:   "my_consumer_group",
        Queues:      map[string]string{"tasks": "*"},
        Concurrency: 10,
    })
    
    // 注册 Handler (代码完全相同)
    consumer.Handle("my_task", myHandler)
    
    // 启动 Consumer
    consumer.Start(ctx)
}
```

## RocketMQ 部署

### Docker 单机部署

```bash
# 启动 NameServer
docker run -d \
  --name rmqnamesrv \
  -p 9876:9876 \
  apache/rocketmq:5.1.4 \
  sh mqnamesrv

# 启动 Broker
docker run -d \
  --name rmqbroker \
  -p 10909:10909 -p 10911:10911 \
  --link rmqnamesrv:namesrv \
  -e "NAMESRV_ADDR=namesrv:9876" \
  apache/rocketmq:5.1.4 \
  sh mqbroker -n namesrv:9876
```

### Docker Compose 部署

```yaml
version: '3.8'
services:
  namesrv:
    image: apache/rocketmq:5.1.4
    container_name: rmqnamesrv
    ports:
      - 9876:9876
    command: sh mqnamesrv

  broker:
    image: apache/rocketmq:5.1.4
    container_name: rmqbroker
    ports:
      - 10909:10909
      - 10911:10911
    environment:
      - NAMESRV_ADDR=rmqnamesrv:9876
    command: sh mqbroker
    depends_on:
      - namesrv
```

## 队列映射差异

### Asynq (Redis)
- **Queue** → Redis List
- 队列名称：`asynq:{queue_name}:pending`
- 优先级通过多个队列 + 权重实现

### RocketMQ
- **Queue** → RocketMQ Topic
- **Task Type** → Message Tag
- 订阅表达式：`*` (所有 tag) 或 `tag1||tag2` (指定 tag)

## 运行示例

### Asynq 示例
```bash
cd nsp-demo/cmd/taskqueue-simple
go run main.go
```

### RocketMQ 示例
```bash
# 确保 RocketMQ 运行
docker ps | grep rmq

# 运行示例
cd nsp-demo/cmd/taskqueue-rocketmq
go run main.go
```

## 注意事项

1. **回调队列**
   - Asynq: 使用单独的 Redis 队列
   - RocketMQ: 使用单独的 Topic

2. **消息重试**
   - Asynq: 内置重试，支持指数退避
   - RocketMQ: 支持自动重试 (MaxReconsumeTimes)

3. **消息顺序**
   - Asynq: 不保证顺序
   - RocketMQ: 支持顺序消息 (需额外配置)

4. **延时消息**
   - Asynq: 原生支持延时任务
   - RocketMQ: 支持延时消息 (18个延时等级)

5. **性能考虑**
   - 小规模 (<10万 TPS): Asynq 足够且更简单
   - 大规模 (>10万 TPS): RocketMQ 更适合

## 扩展其他消息队列

要支持其他消息队列（如 Kafka、RabbitMQ），只需实现两个接口：

```go
// 实现 Broker 接口
type Broker interface {
    Publish(ctx context.Context, task *Task) (*TaskInfo, error)
    Close() error
}

// 实现 Consumer 接口
type Consumer interface {
    Handle(taskType string, handler HandlerFunc)
    Start(ctx context.Context) error
    Stop() error
}
```

参考 `asynqbroker` 和 `rocketmqbroker` 的实现即可。

## 总结

TaskQueue 框架的接口抽象设计使得切换消息队列实现非常简单：

1. ✅ 修改 Broker 和 Consumer 的创建代码
2. ✅ Handler 逻辑完全不变
3. ✅ Engine 和 Store 完全不变
4. ✅ 业务代码 99% 不变

这种设计让你可以：
- 在开发阶段使用 Asynq (简单快速)
- 在生产阶段切换到 RocketMQ (高性能高可靠)
- 根据业务需求灵活选择
