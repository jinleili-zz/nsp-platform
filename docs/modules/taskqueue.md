# TaskQueue 模块

> 包路径：`github.com/paic/nsp-common/pkg/taskqueue`

## 功能说明

TaskQueue 模块解决以下问题：

- **异步任务处理**：将耗时操作异步化，提升接口响应速度
- **工作流编排**：多步骤任务按顺序执行，支持失败重试
- **优先级队列**：支持 4 级优先级，关键任务优先处理
- **消息队列抽象**：Broker 接口支持 Asynq、RocketMQ 等多种实现
- **回调机制**：Worker 执行完成后通知编排器推进流程

---

## 核心概念

### 优先级

| 优先级 | 值 | 说明 |
|--------|---|------|
| `PriorityLow` | 1 | 低优先级 |
| `PriorityNormal` | 3 | 普通优先级（默认） |
| `PriorityHigh` | 6 | 高优先级 |
| `PriorityCritical` | 9 | 关键优先级 |

### 工作流状态

| 状态 | 说明 |
|------|------|
| `pending` | 工作流已创建，等待执行 |
| `running` | 工作流正在执行 |
| `succeeded` | 所有步骤执行成功 |
| `failed` | 步骤失败且重试耗尽 |

### 步骤状态

| 状态 | 说明 |
|------|------|
| `pending` | 步骤等待入队 |
| `queued` | 步骤已入队，等待消费 |
| `running` | 步骤正在执行 |
| `completed` | 步骤执行成功 |
| `failed` | 步骤执行失败 |

---

## 核心接口

```go
// Broker 消息发布接口
type Broker interface {
    Publish(ctx context.Context, task *Task) (*TaskInfo, error)
    Close() error
}

// Consumer 消息消费接口
type Consumer interface {
    Handle(taskType string, handler HandlerFunc)
    Start(ctx context.Context) error
    Stop() error
}

// HandlerFunc 任务处理函数
type HandlerFunc func(ctx context.Context, payload *TaskPayload) (*TaskResult, error)

// Engine 编排引擎
type Engine struct { /* ... */ }

func NewEngine(cfg *Config, broker Broker) (*Engine, error)
func (e *Engine) SubmitWorkflow(ctx context.Context, def *WorkflowDefinition) (string, error)
func (e *Engine) HandleCallback(ctx context.Context, cb *CallbackPayload) error
func (e *Engine) QueryWorkflow(ctx context.Context, workflowID string) (*WorkflowStatusResponse, error)
func (e *Engine) RetryStep(ctx context.Context, stepID string) error

// CallbackSender Worker 回调发送器
type CallbackSender struct { /* ... */ }

func (s *CallbackSender) Success(ctx context.Context, taskID string, result interface{}) error
func (s *CallbackSender) Fail(ctx context.Context, taskID string, errorMsg string) error
```

---

## 配置项

### Config

| 字段名 | 类型 | 默认值 | 说明 |
|--------|------|--------|------|
| `DSN` | `string` | **必填** | PostgreSQL 连接串 |
| `CallbackQueue` | `string` | - | 回调队列名称 |
| `QueueRouter` | `QueueRouterFunc` | DefaultQueueRouter | 队列路由函数 |

### WorkflowDefinition

| 字段名 | 类型 | 说明 |
|--------|------|------|
| `Name` | `string` | 工作流名称 |
| `ResourceType` | `string` | 资源类型（如 "vrf"） |
| `ResourceID` | `string` | 资源 ID |
| `Metadata` | `map[string]string` | 扩展元数据 |
| `Steps` | `[]StepDefinition` | 步骤定义列表 |

### StepDefinition

| 字段名 | 类型 | 默认值 | 说明 |
|--------|------|--------|------|
| `TaskType` | `string` | **必填** | 任务类型标识 |
| `TaskName` | `string` | - | 任务名称（日志用） |
| `Params` | `string` | - | JSON 格式参数 |
| `QueueTag` | `string` | - | 队列路由标签 |
| `Priority` | `Priority` | `PriorityNormal` | 优先级 |
| `MaxRetries` | `int` | `3` | 最大重试次数 |

---

## 快速使用

### 编排端（Orchestrator）

```go
package main

import (
    "context"
    "encoding/json"
    "log"

    _ "github.com/lib/pq"
    "github.com/paic/nsp-common/pkg/taskqueue"
    "github.com/paic/nsp-common/pkg/taskqueue/asynqbroker"
)

func main() {
    ctx := context.Background()

    // 创建 Asynq Broker
    broker, err := asynqbroker.NewBroker(asynqbroker.BrokerConfig{
        RedisAddr: "localhost:6379",
    })
    if err != nil {
        log.Fatal(err)
    }
    defer broker.Close()

    // 创建引擎
    engine, err := taskqueue.NewEngine(&taskqueue.Config{
        DSN:           "postgres://user:pass@localhost:5432/nsp?sslmode=disable",
        CallbackQueue: "task_callbacks",
    }, broker)
    if err != nil {
        log.Fatal(err)
    }
    defer engine.Stop()

    // 运行数据库迁移
    if err := engine.Migrate(ctx); err != nil {
        log.Fatal(err)
    }

    // 提交工作流
    params, _ := json.Marshal(map[string]string{
        "vrf_name": "VRF-001",
        "vlan_id":  "100",
    })

    workflowID, err := engine.SubmitWorkflow(ctx, &taskqueue.WorkflowDefinition{
        Name:         "create-vrf",
        ResourceType: "vrf",
        ResourceID:   "VRF-001",
        Steps: []taskqueue.StepDefinition{
            {
                TaskType:   "create_vrf_on_switch",
                TaskName:   "创建 VRF",
                Params:     string(params),
                QueueTag:   "huawei",  // 路由到 tasks_huawei 队列
                Priority:   taskqueue.PriorityHigh,
                MaxRetries: 3,
            },
            {
                TaskType:   "bind_vrf_interface",
                TaskName:   "绑定接口",
                Params:     string(params),
                QueueTag:   "huawei",
                Priority:   taskqueue.PriorityNormal,
                MaxRetries: 3,
            },
        },
    })
    if err != nil {
        log.Fatal(err)
    }

    log.Printf("工作流已提交: %s", workflowID)
}
```

### 消费端（Worker）

```go
package main

import (
    "context"
    "encoding/json"
    "log"
    "os"
    "os/signal"
    "syscall"

    "github.com/paic/nsp-common/pkg/taskqueue"
    "github.com/paic/nsp-common/pkg/taskqueue/asynqbroker"
)

func main() {
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    // 创建 Broker（用于发送回调）
    broker, err := asynqbroker.NewBroker(asynqbroker.BrokerConfig{
        RedisAddr: "localhost:6379",
    })
    if err != nil {
        log.Fatal(err)
    }

    // 创建回调发送器
    callbackSender := taskqueue.NewCallbackSenderFromBroker(broker, "task_callbacks")

    // 创建 Consumer
    consumer, err := asynqbroker.NewConsumer(asynqbroker.ConsumerConfig{
        RedisAddr:   "localhost:6379",
        Queues:      map[string]int{"tasks_huawei": 10},
        Concurrency: 10,
    })
    if err != nil {
        log.Fatal(err)
    }

    // 注册任务处理器
    consumer.Handle("create_vrf_on_switch", func(ctx context.Context, payload *taskqueue.TaskPayload) (*taskqueue.TaskResult, error) {
        log.Printf("处理任务: %s, 资源: %s", payload.TaskType, payload.ResourceID)

        var params map[string]string
        if err := json.Unmarshal(payload.Params, &params); err != nil {
            callbackSender.Fail(ctx, payload.TaskID, err.Error())
            return nil, err
        }

        // 执行业务逻辑
        if err := createVRFOnSwitch(params); err != nil {
            callbackSender.Fail(ctx, payload.TaskID, err.Error())
            return nil, err
        }

        // 发送成功回调
        result := map[string]string{"vrf_id": "VRF-001-CREATED"}
        callbackSender.Success(ctx, payload.TaskID, result)

        return &taskqueue.TaskResult{
            Data:    result,
            Message: "VRF 创建成功",
        }, nil
    })

    // 启动消费
    go func() {
        if err := consumer.Start(ctx); err != nil {
            log.Printf("Consumer 停止: %v", err)
        }
    }()

    // 优雅关闭
    quit := make(chan os.Signal, 1)
    signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
    <-quit

    consumer.Stop()
    broker.Close()
}

func createVRFOnSwitch(params map[string]string) error {
    // 实际业务逻辑
    return nil
}
```

### 查询工作流状态

```go
func queryWorkflow(engine *taskqueue.Engine, ctx context.Context, workflowID string) {
    status, err := engine.QueryWorkflow(ctx, workflowID)
    if err != nil {
        log.Printf("查询失败: %v", err)
        return
    }
    if status == nil {
        log.Printf("工作流不存在: %s", workflowID)
        return
    }

    log.Printf("状态: %s, 总步骤: %d, 已完成: %d, 失败: %d",
        status.Workflow.Status,
        status.Stats.Total,
        status.Stats.Completed,
        status.Stats.Failed,
    )

    for _, step := range status.Steps {
        log.Printf("  步骤 %d [%s]: %s", step.StepOrder, step.TaskName, step.Status)
    }
}
```

### 重试失败步骤

```go
func retryFailedStep(engine *taskqueue.Engine, ctx context.Context, stepID string) error {
    // 仅 failed 状态的步骤可以重试
    if err := engine.RetryStep(ctx, stepID); err != nil {
        return fmt.Errorf("重试失败: %w", err)
    }
    log.Printf("步骤 %s 已重新入队", stepID)
    return nil
}
```

---

## 队列路由

默认路由规则 `DefaultQueueRouter`：

| QueueTag | Priority | 队列名 |
|----------|----------|--------|
| `""` | Normal | `tasks` |
| `""` | High | `tasks_high` |
| `""` | Critical | `tasks_critical` |
| `"huawei"` | Normal | `tasks_huawei` |
| `"huawei"` | High | `tasks_huawei_high` |
| `"cisco"` | Critical | `tasks_cisco_critical` |

自定义路由：

```go
engine, _ := taskqueue.NewEngine(&taskqueue.Config{
    DSN: "...",
    QueueRouter: func(queueTag string, priority taskqueue.Priority) string {
        // 自定义路由逻辑
        return fmt.Sprintf("my_queue_%s_%d", queueTag, priority)
    },
}, broker)
```

---

## 与其他模块集成

### 结合 Logger 记录任务日志

```go
consumer.Handle("create_vrf", func(ctx context.Context, p *taskqueue.TaskPayload) (*taskqueue.TaskResult, error) {
    logger.InfoContext(ctx, "开始处理任务",
        logger.FieldTaskType, p.TaskType,
        logger.FieldTaskID, p.TaskID,
        "resource_id", p.ResourceID,
    )

    result, err := doWork(p)
    if err != nil {
        logger.ErrorContext(ctx, "任务执行失败",
            logger.FieldTaskID, p.TaskID,
            logger.FieldError, err,
        )
        return nil, err
    }

    logger.InfoContext(ctx, "任务执行成功",
        logger.FieldTaskID, p.TaskID,
    )
    return result, nil
})
```

---

## 数据库表结构

```sql
CREATE TABLE IF NOT EXISTS workflows (
    id              VARCHAR(64)  PRIMARY KEY,
    name            VARCHAR(128) NOT NULL,
    resource_type   VARCHAR(64)  NOT NULL,
    resource_id     VARCHAR(128) NOT NULL,
    status          VARCHAR(20)  NOT NULL DEFAULT 'pending',
    total_steps     INT          NOT NULL DEFAULT 0,
    completed_steps INT          NOT NULL DEFAULT 0,
    failed_steps    INT          NOT NULL DEFAULT 0,
    error_message   TEXT,
    metadata        JSONB,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_workflows_status
    ON workflows(status);
CREATE INDEX IF NOT EXISTS idx_workflows_resource
    ON workflows(resource_type, resource_id);

CREATE TABLE IF NOT EXISTS step_tasks (
    id              VARCHAR(64)  PRIMARY KEY,
    workflow_id     VARCHAR(64)  NOT NULL REFERENCES workflows(id),
    step_order      INT          NOT NULL,
    task_type       VARCHAR(128) NOT NULL,
    task_name       VARCHAR(256),
    params          TEXT,
    status          VARCHAR(20)  NOT NULL DEFAULT 'pending',
    priority        INT          NOT NULL DEFAULT 3,
    queue_tag       VARCHAR(64),
    broker_task_id  VARCHAR(128),
    result          TEXT,
    error_message   TEXT,
    retry_count     INT          NOT NULL DEFAULT 0,
    max_retries     INT          NOT NULL DEFAULT 3,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    queued_at       TIMESTAMPTZ,
    started_at      TIMESTAMPTZ,
    completed_at    TIMESTAMPTZ,
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_step_tasks_workflow
    ON step_tasks(workflow_id, step_order);
CREATE INDEX IF NOT EXISTS idx_step_tasks_status
    ON step_tasks(status);
```

---

## 注意事项

### 任务幂等性

Worker 可能收到重复任务（网络超时重试等），必须保证幂等：

```go
consumer.Handle("create_vrf", func(ctx context.Context, p *taskqueue.TaskPayload) (*taskqueue.TaskResult, error) {
    // 使用 TaskID 作为幂等键
    if alreadyProcessed(p.TaskID) {
        return &taskqueue.TaskResult{Message: "已处理"}, nil
    }

    result, err := doWork(p)
    if err != nil {
        return nil, err
    }

    markProcessed(p.TaskID)
    return result, nil
})
```

### 回调可靠性

回调消息可能丢失，编排端应有超时检测机制。

### 性能提示

- Consumer 的 Concurrency 根据任务类型调整（CPU 密集型用小值，I/O 密集型用大值）
- 队列权重合理配置，确保高优先级任务不被饿死

---

## 常量

```go
const (
    PriorityLow      Priority = 1
    PriorityNormal   Priority = 3
    PriorityHigh     Priority = 6
    PriorityCritical Priority = 9
)
```
