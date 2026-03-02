# TaskQueue 使用指南

## 概述

TaskQueue 是一个基于消息队列的工作流编排框架，支持：
- 多步骤工作流编排
- 优先级队列路由
- 步骤失败自动重试
- 回调驱动的状态流转
- 分布式部署

## 架构设计

```
┌─────────────────────────────────────────────────────────────────────┐
│                         Orchestrator                                │
│  ┌────────────┐    ┌──────────────┐    ┌─────────────────────┐     │
│  │   Engine   │───→│    Broker    │───→│   Message Queue     │     │
│  │            │    │  (Publish)   │    │  (Redis/Kafka/...)  │     │
│  └────────────┘    └──────────────┘    └─────────────────────┘     │
│         │                                          │                │
│         │                                          │                │
│         ▼                                          ▼                │
│  ┌────────────┐                            ┌────────────────┐       │
│  │ PostgreSQL │                            │ Worker Queues  │       │
│  │  (State)   │                            │ ├─ tasks       │       │
│  └────────────┘                            │ ├─ tasks_high  │       │
│         ▲                                  │ └─ callbacks   │       │
│         │                                  └────────────────┘       │
└─────────┼──────────────────────────────────────────┬────────────────┘
          │                                          │
          │                                          ▼
          │                                  ┌────────────────┐
          │                                  │   Consumer     │
          │                                  │   (Worker)     │
          │                                  └───────┬────────┘
          │                                          │
          │  ┌───────────────────────────────────────┘
          │  │
          │  ▼
          │ ┌──────────────┐
          └─┤CallbackSender│
            │  (Publish)   │
            └──────────────┘
```

### 核心概念

| 概念 | 说明 |
|-----|------|
| **Workflow** | 工作流，由多个有序步骤组成 |
| **Step** | 单个任务步骤，包含任务类型、参数、队列标签 |
| **QueueTag** | 队列路由标签，用于将任务发送到特定队列 |
| **Priority** | 任务优先级：Low(1), Normal(3), High(6), Critical(9) |
| **Callback** | Worker执行完成后向Orchestrator发送的回调 |

### 状态流转

**Workflow状态**
```
pending → running → succeeded
                 └→ failed
```

**Step状态**
```
pending → queued → running → completed
                          └→ failed
```

## 快速开始

### 1. 环境准备

**依赖服务**
- PostgreSQL 13+
- Redis 6+ (单机) 或 Redis Cluster (集群)

**安装依赖**
```bash
go get github.com/hibiken/asynq
go get github.com/lib/pq
go get github.com/yourorg/nsp-common/pkg/taskqueue
```

**数据库初始化**
```sql
CREATE DATABASE taskqueue_test;
CREATE USER taskqueue_user WITH PASSWORD 'your_password';
GRANT ALL PRIVILEGES ON DATABASE taskqueue_test TO taskqueue_user;
```

### 2. 创建 Orchestrator（编排端）

```go
package main

import (
    "context"
    "log"
    "github.com/hibiken/asynq"
    "github.com/yourorg/nsp-common/pkg/taskqueue"
    "github.com/yourorg/nsp-common/pkg/taskqueue/asynqbroker"
)

func main() {
    // 1. 创建 Broker（消息队列客户端）
    redisOpt := asynq.RedisClientOpt{Addr: "127.0.0.1:6379"}
    broker := asynqbroker.NewBroker(redisOpt)
    defer broker.Close()

    // 2. 创建 Engine（编排引擎）
    engine, err := taskqueue.NewEngine(&taskqueue.Config{
        DSN:           "postgres://user:pass@localhost:5432/taskqueue_test?sslmode=disable",
        CallbackQueue: "task_callbacks",
        QueueRouter: taskqueue.DefaultQueueRouter, // 使用默认路由器
    }, broker)
    if err != nil {
        log.Fatal(err)
    }
    defer engine.Stop()

    // 3. 执行数据库迁移
    if err := engine.Migrate(context.Background()); err != nil {
        log.Fatal(err)
    }

    // 4. 提交工作流
    ctx := context.Background()
    workflowID, err := engine.SubmitWorkflow(ctx, &taskqueue.WorkflowDefinition{
        Name:         "create-vpc",
        ResourceType: "vpc",
        ResourceID:   "vpc-001",
        Steps: []taskqueue.StepDefinition{
            {
                TaskType:   "create_network",
                TaskName:   "Create Network",
                Params:     `{"vpc_name":"my-vpc","cidr":"10.0.0.0/16"}`,
                Priority:   taskqueue.PriorityNormal,
                MaxRetries: 3,
            },
            {
                TaskType:   "create_subnet",
                TaskName:   "Create Subnet",
                Params:     `{"subnet_name":"my-subnet","cidr":"10.0.1.0/24"}`,
                Priority:   taskqueue.PriorityNormal,
                MaxRetries: 3,
            },
        },
    })
    if err != nil {
        log.Fatal(err)
    }

    log.Printf("Workflow submitted: %s", workflowID)

    // 5. 查询工作流状态
    status, err := engine.QueryWorkflow(ctx, workflowID)
    if err != nil {
        log.Fatal(err)
    }
    log.Printf("Workflow status: %s", status.Workflow.Status)
}
```

### 3. 创建 Worker（工作端）

```go
package main

import (
    "context"
    "encoding/json"
    "log"
    "github.com/hibiken/asynq"
    "github.com/yourorg/nsp-common/pkg/taskqueue"
    "github.com/yourorg/nsp-common/pkg/taskqueue/asynqbroker"
)

func main() {
    redisOpt := asynq.RedisClientOpt{Addr: "127.0.0.1:6379"}
    broker := asynqbroker.NewBroker(redisOpt)
    defer broker.Close()

    // 1. 创建 Consumer
    consumer := asynqbroker.NewConsumer(redisOpt, asynqbroker.ConsumerConfig{
        Concurrency: 10,
        Queues: map[string]int{
            "tasks":           5, // 普通队列权重
            "tasks_high":      8, // 高优先级队列权重
            "task_callbacks": 10, // 回调队列权重
        },
    })

    // 2. 创建 CallbackSender
    callbackSender := taskqueue.NewCallbackSenderFromBroker(broker, "task_callbacks")

    // 3. 注册任务处理器
    consumer.Handle("create_network", func(ctx context.Context, payload *taskqueue.TaskPayload) (*taskqueue.TaskResult, error) {
        var params map[string]interface{}
        json.Unmarshal(payload.Params, &params)
        
        log.Printf("Creating network: %v", params["vpc_name"])
        
        // 执行实际业务逻辑
        // ...
        
        result := map[string]interface{}{
            "network_id": "net-12345",
            "status":     "created",
        }
        
        // 发送成功回调
        if err := callbackSender.Success(ctx, payload.TaskID, result); err != nil {
            return nil, err
        }
        
        return &taskqueue.TaskResult{Data: result}, nil
    })

    consumer.Handle("create_subnet", func(ctx context.Context, payload *taskqueue.TaskPayload) (*taskqueue.TaskResult, error) {
        var params map[string]interface{}
        json.Unmarshal(payload.Params, &params)
        
        log.Printf("Creating subnet: %v", params["subnet_name"])
        
        // 模拟失败场景
        // if someCondition {
        //     callbackSender.Fail(ctx, payload.TaskID, "CIDR conflict")
        //     return nil, fmt.Errorf("subnet creation failed")
        // }
        
        result := map[string]interface{}{
            "subnet_id": "subnet-67890",
            "status":    "created",
        }
        
        callbackSender.Success(ctx, payload.TaskID, result)
        return &taskqueue.TaskResult{Data: result}, nil
    })

    // 4. 启动消费者
    log.Println("Worker starting...")
    if err := consumer.Start(context.Background()); err != nil {
        log.Fatal(err)
    }
}
```

### 4. 处理回调（Orchestrator端）

```go
// 在 Orchestrator 端启动回调消费者
callbackConsumer := asynqbroker.NewConsumer(redisOpt, asynqbroker.ConsumerConfig{
    Concurrency: 5,
    Queues:      map[string]int{"task_callbacks": 10},
})

callbackConsumer.HandleRaw("task_callback", func(ctx context.Context, t *asynq.Task) error {
    var cb taskqueue.CallbackPayload
    if err := json.Unmarshal(t.Payload(), &cb); err != nil {
        return err
    }
    // Engine 自动处理状态流转
    return engine.HandleCallback(ctx, &cb)
})

go callbackConsumer.Start(context.Background())
```

## 高级功能

### 1. 自定义队列路由

```go
engine, err := taskqueue.NewEngine(&taskqueue.Config{
    DSN:           pgDSN,
    CallbackQueue: "callbacks",
    QueueRouter: func(queueTag string, priority taskqueue.Priority) string {
        // 根据设备类型路由到不同队列
        switch queueTag {
        case "switch":
            if priority == taskqueue.PriorityCritical {
                return "switch_critical"
            }
            return "switch_tasks"
        case "firewall":
            return "firewall_tasks"
        default:
            return "default_tasks"
        }
    },
}, broker)
```

### 2. 步骤失败重试

工作流失败后，可以手动重试失败的步骤：

```go
// 查询工作流状态
status, _ := engine.QueryWorkflow(ctx, workflowID)

// 找到失败的步骤
for _, step := range status.Steps {
    if step.Status == taskqueue.StepStatusFailed {
        // 重试该步骤
        err := engine.RetryStep(ctx, step.ID)
        if err != nil {
            log.Printf("Retry failed: %v", err)
        }
    }
}
```

### 3. 带优先级的任务

```go
Steps: []taskqueue.StepDefinition{
    {
        TaskType:   "critical_operation",
        Priority:   taskqueue.PriorityCritical, // 紧急任务
        MaxRetries: 5,
    },
    {
        TaskType:   "normal_operation",
        Priority:   taskqueue.PriorityNormal, // 普通任务
        MaxRetries: 3,
    },
    {
        TaskType:   "cleanup",
        Priority:   taskqueue.PriorityLow, // 低优先级
        MaxRetries: 1,
    },
}
```

### 4. 队列标签与路由

```go
Steps: []taskqueue.StepDefinition{
    {
        TaskType:   "configure_switch",
        QueueTag:   "switch",    // 路由到交换机队列
        Priority:   taskqueue.PriorityHigh,
    },
    {
        TaskType:   "configure_firewall",
        QueueTag:   "firewall",  // 路由到防火墙队列
        Priority:   taskqueue.PriorityNormal,
    },
}
```

在 Worker 端消费特定队列：

```go
// Switch Worker - 只消费 switch 队列
switchConsumer := asynqbroker.NewConsumer(redisOpt, asynqbroker.ConsumerConfig{
    Concurrency: 20,
    Queues: map[string]int{
        "switch_tasks":         5,
        "switch_tasks_high":    8,
        "switch_tasks_critical": 10,
    },
    StrictPriority: true, // 严格优先级，先处理高优先级
})

// Firewall Worker - 只消费 firewall 队列
firewallConsumer := asynqbroker.NewConsumer(redisOpt, asynqbroker.ConsumerConfig{
    Concurrency: 10,
    Queues: map[string]int{
        "firewall_tasks": 5,
    },
})
```

## 完整示例

查看代码库中的示例：

### taskqueue-simple - 入门示例（推荐新手）
最简化的示例，演示基本的工作流提交和执行：
- 单机 Redis（无需集群）
- 简单的两步工作流（创建记录 → 发送邮件）
- 完整的启动和关闭流程

运行方式：
```bash
cd nsp-demo/cmd/taskqueue-simple
go run main.go
```

### taskqueue-demo - 完整功能演示
演示完整的 VPC 创建工作流：
- 创建 VRF
- 创建 VLAN 子接口
- 创建防火墙区域
- Redis Cluster 集群模式
- 自定义队列路由器

运行方式：
```bash
cd nsp-demo/cmd/taskqueue-demo
go run main.go
```

### taskqueue-demo-fail - 失败重试场景
演示步骤失败和手动重试：
- 第二个步骤首次执行失败
- 工作流进入 failed 状态
- 手动重试失败步骤
- 重试成功，工作流变为 succeeded

运行方式：
```bash
cd nsp-demo/cmd/taskqueue-demo-fail
go run main.go
```

**详细说明**：参考 `nsp-demo/cmd/TASKQUEUE_DEMOS.md`

## 最佳实践

### 1. 幂等性设计

Worker 处理器必须设计为幂等操作：

```go
consumer.Handle("create_resource", func(ctx context.Context, payload *taskqueue.TaskPayload) (*taskqueue.TaskResult, error) {
    var params ResourceParams
    json.Unmarshal(payload.Params, &params)
    
    // ✅ 检查资源是否已存在
    existing, err := db.GetResourceByName(params.Name)
    if err == nil && existing != nil {
        // 资源已存在，直接返回成功
        log.Printf("Resource already exists: %s", params.Name)
        result := map[string]interface{}{"resource_id": existing.ID}
        callbackSender.Success(ctx, payload.TaskID, result)
        return &taskqueue.TaskResult{Data: result}, nil
    }
    
    // 创建新资源
    resource, err := createResource(params)
    if err != nil {
        callbackSender.Fail(ctx, payload.TaskID, err.Error())
        return nil, err
    }
    
    result := map[string]interface{}{"resource_id": resource.ID}
    callbackSender.Success(ctx, payload.TaskID, result)
    return &taskqueue.TaskResult{Data: result}, nil
})
```

### 2. 错误处理

区分可重试错误和永久性错误：

```go
consumer.Handle("api_call", func(ctx context.Context, payload *taskqueue.TaskPayload) (*taskqueue.TaskResult, error) {
    resp, err := httpClient.Do(request)
    if err != nil {
        // 网络错误 - 可重试
        callbackSender.Fail(ctx, payload.TaskID, "network error: "+err.Error())
        return nil, err // 返回错误，框架会重试
    }
    
    if resp.StatusCode >= 500 {
        // 服务端错误 - 可重试
        callbackSender.Fail(ctx, payload.TaskID, "server error")
        return nil, fmt.Errorf("server error: %d", resp.StatusCode)
    }
    
    if resp.StatusCode == 400 {
        // 请求参数错误 - 不可重试
        callbackSender.Fail(ctx, payload.TaskID, "invalid parameters")
        return nil, nil // 返回 nil 错误，避免无意义的重试
    }
    
    // 成功
    callbackSender.Success(ctx, payload.TaskID, result)
    return &taskqueue.TaskResult{Data: result}, nil
})
```

### 3. 超时控制

为长时间运行的任务设置合理的超时：

```go
consumer.Handle("long_running_task", func(ctx context.Context, payload *taskqueue.TaskPayload) (*taskqueue.TaskResult, error) {
    // 设置任务级别的超时
    taskCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
    defer cancel()
    
    result, err := performLongOperation(taskCtx, payload.Params)
    if err != nil {
        if errors.Is(err, context.DeadlineExceeded) {
            callbackSender.Fail(ctx, payload.TaskID, "operation timeout")
        } else {
            callbackSender.Fail(ctx, payload.TaskID, err.Error())
        }
        return nil, err
    }
    
    callbackSender.Success(ctx, payload.TaskID, result)
    return &taskqueue.TaskResult{Data: result}, nil
})
```

### 4. 监控与可观测性

记录关键指标和日志：

```go
consumer.Handle("monitored_task", func(ctx context.Context, payload *taskqueue.TaskPayload) (*taskqueue.TaskResult, error) {
    start := time.Now()
    
    log.Printf("[Worker] Task started: type=%s, task_id=%s", payload.TaskType, payload.TaskID)
    
    result, err := doWork(ctx, payload)
    
    duration := time.Since(start)
    log.Printf("[Worker] Task finished: type=%s, task_id=%s, duration=%v, success=%v",
        payload.TaskType, payload.TaskID, duration, err == nil)
    
    // 上报指标到监控系统
    // metrics.RecordTaskDuration(payload.TaskType, duration)
    // metrics.IncrementTaskCount(payload.TaskType, err == nil)
    
    if err != nil {
        callbackSender.Fail(ctx, payload.TaskID, err.Error())
        return nil, err
    }
    
    callbackSender.Success(ctx, payload.TaskID, result)
    return &taskqueue.TaskResult{Data: result}, nil
})
```

### 5. 分布式部署

**Orchestrator 部署**
- 单实例部署即可（通过 PostgreSQL 保证状态一致性）
- 高可用场景可部署多实例（主备模式或主主模式）

**Worker 部署**
- 水平扩展，根据负载动态增减实例
- 不同 Worker 可以消费不同的队列（按设备类型分组）
- 使用 StrictPriority 确保高优先级任务优先处理

```bash
# Orchestrator (单实例)
./orchestrator -config config.yaml

# Switch Workers (多实例)
./switch-worker -concurrency 20 -queues switch_tasks,switch_tasks_high &
./switch-worker -concurrency 20 -queues switch_tasks,switch_tasks_high &

# Firewall Workers (多实例)
./firewall-worker -concurrency 10 -queues firewall_tasks &
./firewall-worker -concurrency 10 -queues firewall_tasks &
```

## 故障排查

### 1. 工作流卡住不前进

**可能原因**
- Callback Consumer 未启动或崩溃
- Worker 未消费对应队列
- Redis 连接断开

**排查步骤**
```go
// 1. 查询工作流详细状态
status, _ := engine.QueryWorkflow(ctx, workflowID)
log.Printf("Workflow: %+v", status.Workflow)
for _, step := range status.Steps {
    log.Printf("Step %d: status=%s, broker_id=%s", step.StepOrder, step.Status, step.BrokerTaskID)
}

// 2. 检查 Redis 队列积压
// 使用 redis-cli 或 Asynq Web UI 查看队列长度

// 3. 检查 PostgreSQL 数据
SELECT * FROM tq_workflows WHERE id = 'xxx';
SELECT * FROM tq_steps WHERE workflow_id = 'xxx' ORDER BY step_order;
```

### 2. 任务重复执行

**原因**：Worker 处理器不是幂等的

**解决**：参考"幂等性设计"章节

### 3. 回调丢失

**原因**：回调发送失败但 Worker 认为任务成功

**解决**：确保回调发送成功后才返回成功
```go
// ❌ 错误做法
result := doWork()
callbackSender.Success(ctx, payload.TaskID, result) // 忽略错误
return &taskqueue.TaskResult{Data: result}, nil

// ✅ 正确做法
result := doWork()
if err := callbackSender.Success(ctx, payload.TaskID, result); err != nil {
    return nil, err // 返回错误，任务会重试
}
return &taskqueue.TaskResult{Data: result}, nil
```

## API 参考

### Engine API

| 方法 | 说明 |
|-----|------|
| `NewEngine(cfg, broker)` | 创建引擎实例 |
| `Migrate(ctx)` | 执行数据库迁移 |
| `SubmitWorkflow(ctx, def)` | 提交工作流 |
| `HandleCallback(ctx, cb)` | 处理回调（内部调用） |
| `QueryWorkflow(ctx, workflowID)` | 查询工作流状态 |
| `RetryStep(ctx, stepID)` | 重试失败步骤 |
| `Stop()` | 停止引擎 |

### CallbackSender API

| 方法 | 说明 |
|-----|------|
| `Success(ctx, taskID, result)` | 发送成功回调 |
| `Fail(ctx, taskID, errorMsg)` | 发送失败回调 |

### Consumer API

| 方法 | 说明 |
|-----|------|
| `Handle(taskType, handler)` | 注册任务处理器 |
| `HandleRaw(taskType, handler)` | 注册原始处理器 |
| `Start(ctx)` | 启动消费者 |
| `Stop()` | 停止消费者 |

## 性能调优

### 1. 并发控制

```go
consumer := asynqbroker.NewConsumer(redisOpt, asynqbroker.ConsumerConfig{
    Concurrency: 50, // 根据 CPU 和 IO 情况调整
    Queues: map[string]int{
        "cpu_intensive": 5,  // CPU 密集型任务，低并发
        "io_intensive":  10, // IO 密集型任务，高并发
    },
})
```

### 2. 数据库连接池

```go
db.SetMaxOpenConns(100)    // 最大连接数
db.SetMaxIdleConns(10)     // 最大空闲连接
db.SetConnMaxLifetime(5 * time.Minute)
```

### 3. Redis 连接池

Asynq 会自动管理 Redis 连接池，默认配置已优化。

### 4. 批量操作

提交多个独立的工作流可以并发进行：

```go
var wg sync.WaitGroup
for i := 0; i < 100; i++ {
    wg.Add(1)
    go func(idx int) {
        defer wg.Done()
        _, err := engine.SubmitWorkflow(ctx, createWorkflowDef(idx))
        if err != nil {
            log.Printf("Submit failed: %v", err)
        }
    }(i)
}
wg.Wait()
```

## 常见问题

**Q: TaskQueue 与 SAGA 的区别？**

A: 
- TaskQueue：异步任务编排，步骤之间通过回调驱动，失败后可手动重试
- SAGA：分布式事务，支持自动补偿回滚，适合需要强一致性的场景

**Q: 如何保证任务不丢失？**

A: 
- Asynq 基于 Redis 持久化任务
- PostgreSQL 持久化工作流状态
- Worker 崩溃后，任务会自动重新入队

**Q: 能否取消正在执行的工作流？**

A: 当前版本不支持取消，可以通过以下方式实现：
- Worker 检查 context.Done()
- 在数据库中标记工作流为已取消
- Worker 发现工作流已取消时跳过后续步骤

**Q: 如何实现任务的顺序依赖？**

A: 工作流中的步骤是严格顺序执行的，前一个步骤成功后才会执行下一个步骤。

**Q: 支持哪些消息队列？**

A: 当前内置支持 Asynq (Redis)，可以通过实现 `Broker` 和 `Consumer` 接口扩展支持其他消息队列（Kafka、RabbitMQ等）。

## 下一步

- 阅读源码：`nsp-common/pkg/taskqueue/`
- 运行示例：`demo/main.go` 和 `demo_fail/main.go`
- 集成到项目：参考"快速开始"章节
- 性能调优：参考"性能调优"章节

## 技术支持

遇到问题？
1. 查看日志输出
2. 检查 PostgreSQL 和 Redis 连接
3. 查阅本文档的"故障排查"章节
4. 提交 Issue 到代码仓库
