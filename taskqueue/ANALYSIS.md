# TaskQueue 模块分析报告

## 一、代码实现分析

### 1.1 核心架构

TaskQueue 采用 **编排器-工作者（Orchestrator-Worker）** 分离架构：

```
┌──────────────────────────────────────────────────────────────┐
│                      Orchestrator                            │
│  ┌────────────┐    ┌──────────────┐    ┌─────────────────┐  │
│  │   Engine   │───→│    Broker    │───→│  Message Queue  │  │
│  │            │    │  (Publish)   │    │  (Redis/Kafka)  │  │
│  └────────────┘    └──────────────┘    └─────────────────┘  │
│         │                                        │            │
│         │          ┌──────────────┐              │            │
│         │          │  Inspector   │←─────────────┤            │
│         │          │  (Monitor)   │              │            │
│         │          └──────────────┘              │            │
│         ▼                                        ▼            │
│  ┌────────────┐                          ┌────────────────┐  │
│  │ PostgreSQL │                          │ Worker Queues  │  │
│  │  (State)   │                          └────────────────┘  │
│  └────────────┘                                  │            │
└──────────────────────────────────────────────────┼────────────┘
                                                   │
                ┌──────────────────────────────────┘
                │
                ▼
┌──────────────────────────────────────────────────────────────┐
│                         Worker                               │
│  ┌────────────┐    ┌──────────────┐    ┌─────────────────┐  │
│  │  Consumer  │◄───│     Queue    │    │CallbackSender   │  │
│  │  (Handle)  │    │              │    │  (Publish)      │  │
│  └────────────┘    └──────────────┘    └─────────────────┘  │
└──────────────────────────────────────────────────────────────┘
```

**核心组件职责**：
| 组件 | 职责 |
|------|------|
| Engine | 工作流编排引擎，状态管理和任务调度 |
| Broker | 消息发布抽象层 |
| Consumer | 消息消费抽象层 |
| Store | 持久化工作流状态（PostgreSQL） |
| CallbackSender | Worker 向 Orchestrator 发送执行结果 |
| **Inspector** | 运维监控组件（队列统计、任务查询、Worker 状态） |

### 1.2 核心组件

#### 1.2.1 Engine (engine.go)
**职责**：工作流编排引擎，负责状态管理和任务调度

**关键方法**：
```go
// 提交工作流：创建 Workflow 和 Steps 记录，入队第一个步骤
SubmitWorkflow(ctx, def) (workflowID, error)

// 处理回调：接收 Worker 回调，驱动状态流转
HandleCallback(ctx, cb) error
  ├─ 成功 → IncrementCompletedSteps → 入队下一步骤
  ├─ 失败 → IncrementFailedSteps → 标记 Workflow 为 failed
  └─ 所有步骤完成 → 标记 Workflow 为 succeeded

// 查询状态：返回 Workflow 和所有 Steps 的详细信息
QueryWorkflow(ctx, workflowID) (*WorkflowStatusResponse, error)

// 重试失败步骤：重置步骤状态并重新入队
RetryStep(ctx, stepID) error
```

**状态机逻辑**：
```
Workflow: pending → running → succeeded / failed
Step:     pending → queued → running → completed / failed
```

#### 1.2.2 Broker (broker.go + asynqbroker/broker.go)
**职责**：消息发布抽象层

```go
type Broker interface {
    Publish(ctx, task) (*TaskInfo, error)  // 发布任务到队列
    Close() error                          // 关闭连接
}
```

**Asynq 实现**：
- 基于 Redis（支持单机和集群模式）
- 自动序列化 Payload
- 返回 Broker 分配的任务 ID

#### 1.2.3 Consumer (consumer.go + asynqbroker/consumer.go)
**职责**：消息消费抽象层

```go
type Consumer interface {
    Handle(taskType, handler)  // 注册任务处理器
    Start(ctx) error           // 启动消费（阻塞）
    Stop() error               // 停止消费
}
```

**Asynq 实现特性**：
- 并发控制（Concurrency）
- 多队列支持（Queues + 权重）
- 严格优先级模式（StrictPriority）

#### 1.2.4 Store (store.go + pg_store.go)
**职责**：持久化工作流状态

**表结构**：
```sql
-- tq_workflows: 工作流元数据
id, name, resource_type, resource_id, status, 
total_steps, completed_steps, failed_steps, 
error_message, metadata, created_at, updated_at

-- tq_steps: 步骤详情
id, workflow_id, step_order, task_type, task_name, 
params, status, priority, queue_tag, 
broker_task_id, result, error_message,
retry_count, max_retries, created_at, queued_at, 
started_at, completed_at, updated_at
```

**关键操作**：
- `CreateWorkflow / CreateSteps`：事务创建
- `UpdateWorkflowStatus`：状态更新
- `IncrementCompletedSteps / IncrementFailedSteps`：原子计数
- `GetNextPendingStep`：查找下一个待执行步骤
- `GetStepStats`：聚合统计

#### 1.2.5 CallbackSender (engine.go:386-436)
**职责**：Worker 向 Orchestrator 发送执行结果

```go
sender := engine.NewCallbackSender()
sender.Success(ctx, taskID, result)  // 成功回调
sender.Fail(ctx, taskID, errorMsg)   // 失败回调
```

**实现机制**：
- 将回调封装为 `task_callback` 任务
- 发布到专用的 `CallbackQueue`
- Orchestrator 消费回调队列，调用 `Engine.HandleCallback()`

#### 1.2.6 Inspector (inspector.go + asynqbroker/inspector.go)
**职责**：运维监控组件，提供队列统计、任务查询、Worker 状态等功能

**设计原则**：
- **独立于 Engine**：Inspector 与 Engine 是平级的独立组件，不存在依赖关系
- **最大公约数**：核心接口只包含所有后端都能实现的方法
- **按职责拆分**：分层设计，可选能力通过接口断言探测

**接口分层**：
```
┌─────────────────────────────────────────────────────────────┐
│                       Inspector（核心）                      │
│  Queues() / GetQueueStats() / ListWorkers() / Close()       │
├─────────────────────────────────────────────────────────────┤
│                    TaskReader（可选）                         │
│  GetTaskInfo() / ListTasks()                                 │
├─────────────────────────────────────────────────────────────┤
│                   TaskController（可选）                      │
│  DeleteTask() / RunTask() / ArchiveTask() / CancelTask()    │
│  BatchDeleteTasks() / BatchRunTasks() / BatchArchiveTasks() │
├─────────────────────────────────────────────────────────────┤
│                   QueueController（可选）                     │
│  PauseQueue() / UnpauseQueue() / DeleteQueue()              │
└─────────────────────────────────────────────────────────────┘
```

**核心接口**（所有后端必须实现）：
```go
type Inspector interface {
    Queues(ctx context.Context) ([]string, error)
    GetQueueStats(ctx context.Context, queue string) (*QueueStats, error)
    ListWorkers(ctx context.Context) ([]*WorkerInfo, error)
    Close() error
}
```

**可选接口**（通过接口断言探测）：
```go
// 任务级查询
if tr, ok := inspector.(taskqueue.TaskReader); ok {
    task, _ := tr.GetTaskInfo(ctx, queue, taskID)
    result, _ := tr.ListTasks(ctx, queue, state, opts)
}

// 任务级操作
if tc, ok := inspector.(taskqueue.TaskController); ok {
    tc.RunTask(ctx, queue, taskID)
    tc.BatchRunTasks(ctx, queue, taskqueue.TaskStateFailed)
}

// 队列级操作
if qc, ok := inspector.(taskqueue.QueueController); ok {
    qc.PauseQueue(ctx, queue)
    qc.DeleteQueue(ctx, queue, force)
}
```

**后端能力矩阵**：

| 接口 | asynq | RocketMQ |
|------|-------|----------|
| **Inspector（核心）** | ✅ 全部字段 | ✅ 部分字段（Pending/Completed，其余为 0） |
| **TaskReader** | ✅ | ❌ 不实现 |
| **TaskController** | ✅ | ❌ 不实现 |
| **QueueController** | ✅ | ❌ 不实现 |

**数据模型**：
```go
// 队列统计
type QueueStats struct {
    Queue     string    // 队列名称
    Pending   int       // 等待执行
    Scheduled int       // 延迟调度中
    Active    int       // 正在执行
    Retry     int       // 等待重试
    Failed    int       // 已失败（死信）
    Completed int       // 已完成
    Paused    bool      // 是否暂停
    Timestamp time.Time // 统计时间
}

// 任务状态枚举
type TaskState string
const (
    TaskStatePending   TaskState = "pending"
    TaskStateScheduled TaskState = "scheduled"
    TaskStateActive    TaskState = "active"
    TaskStateRetry     TaskState = "retry"
    TaskStateFailed    TaskState = "failed"
    TaskStateCompleted TaskState = "completed"
)
```

**asynq 状态映射**：
| asynq 状态 | TaskState | 说明 |
|-----------|-----------|------|
| pending | TaskStatePending | |
| scheduled | TaskStateScheduled | |
| aggregating | TaskStatePending | 聚合等待中，业务视角属于待处理 |
| active | TaskStateActive | |
| retry | TaskStateRetry | |
| archived | TaskStateFailed | |
| completed | TaskStateCompleted | |

### 1.3 工作流程

#### 提交阶段
```
1. engine.SubmitWorkflow(def)
   ├─ 生成 WorkflowID
   ├─ 插入 tq_workflows (status=pending)
   ├─ 批量插入 tq_steps (status=pending)
   ├─ 更新 workflow.status = running
   └─ enqueueStep(steps[0])  # 入队第一个步骤
```

#### 执行阶段
```
2. Worker.Handle("task_type")
   ├─ Consumer 从队列拉取任务
   ├─ 反序列化 TaskPayload
   ├─ 调用用户注册的 HandlerFunc
   ├─ 执行业务逻辑
   └─ 发送回调
       ├─ callbackSender.Success(taskID, result)
       └─ callbackSender.Fail(taskID, errorMsg)
```

#### 状态流转阶段
```
3. engine.HandleCallback(cb)
   ├─ 读取 step 信息
   ├─ 更新 step 结果和状态
   ├─ 根据回调状态分支处理：
   │   ├─ completed → handleStepSuccess()
   │   │   ├─ IncrementCompletedSteps
   │   │   ├─ GetNextPendingStep
   │   │   ├─ 有下一步 → enqueueStep(nextStep)
   │   │   └─ 无下一步 → checkAndCompleteWorkflow()
   │   └─ failed → handleStepFailure()
   │       ├─ IncrementFailedSteps
   │       └─ UpdateWorkflowStatus(failed)
   └─ 返回
```

### 1.4 关键设计

#### 队列路由器（QueueRouter）
```go
type QueueRouterFunc func(queueTag string, priority Priority) string

// 默认路由器
func DefaultQueueRouter(queueTag string, priority Priority) string {
    base := "tasks"
    if queueTag != "" {
        base = "tasks_" + queueTag
    }
    switch priority {
    case PriorityCritical: return base + "_critical"
    case PriorityHigh:     return base + "_high"
    case PriorityLow:      return base + "_low"
    default:               return base
    }
}
```

**示例**：
- `("", Normal)` → `tasks`
- `("switch", High)` → `tasks_switch_high`
- `("firewall", Critical)` → `tasks_firewall_critical`

#### 幂等性保证
- Worker 使用 `X-Idempotency-Key: step.ID` 确保重复执行的幂等性
- 回调发送失败时任务会重试，Orchestrator 通过步骤状态去重

#### 分布式部署
- **Orchestrator**：单实例或多实例（通过 PostgreSQL 保证状态一致性）
- **Worker**：水平扩展，按队列分组（如 switch-worker, firewall-worker）

## 二、现有 Demo 分析

### 2.1 taskqueue-demo（成功场景）

**位置**：`nsp-demo/cmd/taskqueue-demo/main.go`

**场景**：VPC 创建工作流
- Step 1: 创建 VRF
- Step 2: 创建 VLAN 子接口
- Step 3: 创建防火墙区域

**特点**：
- 使用 Redis Cluster（3节点）
- 自定义队列路由器（switch / firewall）
- 所有步骤正常执行
- 演示完整的提交→执行→完成流程

### 2.2 taskqueue-demo-fail（失败重试场景）

**位置**：`nsp-demo/cmd/taskqueue-demo-fail/main.go`

**场景**：模拟步骤失败
- Step 1: 成功
- Step 2: 首次执行失败，返回错误
- Step 3: 未执行

**流程**：
1. 提交工作流
2. Step 2 失败 → Workflow 进入 `failed` 状态
3. 调用 `engine.RetryStep(failedStepID)`
4. Step 2 重试成功
5. Step 3 继续执行
6. Workflow 最终 `succeeded`

**重试机制**：
```go
failCount[taskID]++
if attempt == 1 {
    callbackSender.Fail(ctx, taskID, "simulated error")
    return nil, fmt.Errorf("simulated failure")
}
// 第二次重试成功
callbackSender.Success(ctx, taskID, result)
```

### 2.3 taskqueue-simple（入门示例）

**位置**：`nsp-demo/cmd/taskqueue-simple/main.go`

**目的**：降低学习门槛，提供最简化的示例

**特点**：
- 单机 Redis（无需集群）
- 单队列模式
- 简单的两步工作流：创建记录 → 发送邮件
- 完整的启动和关闭流程

**数据库要求**：
```sql
CREATE DATABASE taskqueue_simple;
```

**运行方式**：
```bash
# 确保 Redis 和 PostgreSQL 运行
redis-server &
psql -c "CREATE DATABASE taskqueue_simple;"

# 运行示例
cd nsp-demo/cmd/taskqueue-simple
go run main.go
```

**预期输出**：
```
[Setup] Broker created
[Setup] Database migrated
[Setup] Consumers started
[Demo] Workflow submitted: id=xxx
[Worker] Creating record: user_registration (task_id=yyy)
[Worker] Record created: user_registration
[Worker] Sending email to: user@example.com (task_id=zzz)
[Worker] Email sent to: user@example.com
[Demo] Status: succeeded (completed=2/2, failed=0)
[Demo] ✅ Workflow SUCCEEDED!
```

### 4.1 快速开始步骤

#### 步骤1：准备环境
```bash
# 启动 PostgreSQL
docker run -d --name postgres \
  -e POSTGRES_PASSWORD=postgres \
  -p 5432:5432 postgres:13

# 启动 Redis
docker run -d --name redis \
  -p 6379:6379 redis:6-alpine

# 创建数据库
psql -h localhost -U postgres -c "CREATE DATABASE taskqueue_simple;"
```

#### 步骤2：编写 Orchestrator
```go
// 创建 Engine
engine, _ := taskqueue.NewEngine(&taskqueue.Config{
    DSN:           pgDSN,
    CallbackQueue: "callbacks",
}, broker)

// 初始化表结构
engine.Migrate(ctx)

// 提交工作流
workflowID, _ := engine.SubmitWorkflow(ctx, &taskqueue.WorkflowDefinition{
    Name:  "my-workflow",
    Steps: []taskqueue.StepDefinition{
        {TaskType: "step1", TaskName: "Step 1", Params: `{}`},
        {TaskType: "step2", TaskName: "Step 2", Params: `{}`},
    },
})
```

#### 步骤3：编写 Worker
```go
// 创建 Consumer
consumer := asynqbroker.NewConsumer(redisOpt, asynqbroker.ConsumerConfig{
    Concurrency: 10,
    Queues:      map[string]int{"tasks": 5},
})

// 注册处理器
callbackSender := taskqueue.NewCallbackSenderFromBroker(broker, "callbacks")
consumer.Handle("step1", func(ctx context.Context, payload *taskqueue.TaskPayload) (*taskqueue.TaskResult, error) {
    // 执行业务逻辑
    result := map[string]interface{}{"status": "done"}
    
    // 发送成功回调
    callbackSender.Success(ctx, payload.TaskID, result)
    return &taskqueue.TaskResult{Data: result}, nil
})

// 启动消费
consumer.Start(ctx)
```

#### 步骤4：处理回调
```go
// Orchestrator 端消费回调队列
callbackConsumer := asynqbroker.NewConsumer(redisOpt, asynqbroker.ConsumerConfig{
    Concurrency: 5,
    Queues:      map[string]int{"callbacks": 10},
})

callbackConsumer.HandleRaw("task_callback", func(ctx context.Context, t *asynq.Task) error {
    var cb taskqueue.CallbackPayload
    json.Unmarshal(t.Payload(), &cb)
    return engine.HandleCallback(ctx, &cb)
})

go callbackConsumer.Start(ctx)
```

### 4.2 测试验证

#### 编译测试
```bash
# 测试 demo 编译
cd nsp-common/pkg/taskqueue/demo
go build -o demo

# 测试 demo_fail 编译
cd ../demo_fail
go build -o demo_fail

# 测试 example_simple 编译
cd ../example_simple
go build -o example_simple
```

#### 功能测试清单

| 测试项 | 验证点 | 预期结果 |
|-------|--------|---------|
| **数据库迁移** | `engine.Migrate(ctx)` | 成功创建 `tq_workflows` 和 `tq_steps` 表 |
| **工作流提交** | `SubmitWorkflow(def)` | 返回 workflowID，数据库插入记录 |
| **任务入队** | 观察 Redis 队列 | `LLEN tasks` 大于 0 |
| **Worker 消费** | Worker 日志 | 输出 `[Worker] Creating ...` |
| **回调发送** | Callback Queue | `LLEN task_callbacks` 大于 0 |
| **状态流转** | `QueryWorkflow()` | Step 状态: pending→queued→running→completed |
| **工作流完成** | 最终状态 | Workflow.status = succeeded |
| **步骤失败** | demo_fail | Workflow.status = failed, 可重试 |
| **重试成功** | `RetryStep()` | failed step 重新执行并成功 |

#### Redis 调试命令
```bash
redis-cli

# 查看所有队列
KEYS *tasks*

# 查看队列长度
LLEN tasks
LLEN task_callbacks

# 查看队列内容（仅调试用）
LRANGE tasks 0 -1
```

#### PostgreSQL 调试命令
```sql
-- 查看工作流
SELECT id, name, status, total_steps, completed_steps, failed_steps 
FROM tq_workflows 
ORDER BY created_at DESC LIMIT 10;

-- 查看步骤
SELECT workflow_id, step_order, task_name, status, broker_task_id
FROM tq_steps 
WHERE workflow_id = '<your_workflow_id>'
ORDER BY step_order;

-- 查看失败的工作流
SELECT id, name, error_message, created_at
FROM tq_workflows
WHERE status = 'failed';
```

## 五、输出文件清单

### 5.1 文档
- **GUIDE.md**：完整使用指南（已生成）
  - 架构设计
  - 快速开始
  - 高级功能（含 Inspector 运维监控）
  - 最佳实践
  - 故障排查
  - API 参考（含 Inspector API）

### 5.2 核心代码
- **inspector.go**：Inspector 接口定义和数据模型
  - Inspector（核心接口）
  - TaskReader / TaskController / QueueController（可选接口）
  - QueueStats / TaskState / TaskDetail / WorkerInfo（数据模型）

- **asynqbroker/inspector.go**：asynq 实现
  - 实现全部 4 层接口
  - 状态映射（aggregating → pending, archived → failed）
  - Payload 自动 unwrap trace envelope

- **rocketmqbroker/inspector.go**：RocketMQ 实现
  - 仅实现核心 Inspector 接口
  - 部分字段返回零值（API 限制）

### 5.3 示例代码
- **example_simple/main.go**：简化示例（已生成）
  - 单机 Redis
  - 两步工作流
  - 完整的启动关闭流程

### 5.4 已有示例
- **demo/main.go**：完整成功场景
- **demo_fail/main.go**：失败重试场景
- **taskqueue-priority-demo/**：优先级队列示例（使用 Inspector 抽象）

## 六、建议与改进

### 6.1 文档改进
✅ 已完成：
- 详细的架构图
- 逐步教程
- 故障排查指南
- API 参考
- Inspector 使用指南

### 6.2 代码改进建议
✅ 已完成：
- Inspector 运维监控抽象层
- 多后端支持（asynq 完整实现，RocketMQ 核心实现）
- 分层接口设计（核心接口 + 可选接口）

🔲 待完成：
1. **增加更多示例**
   - 优先级队列示例
   - 自定义队列路由示例
   - 长时间运行任务示例

2. **增强可观测性**
   - 添加 Prometheus 指标导出
   - 增加结构化日志（集成 logger 模块）
   - 增加 OpenTelemetry tracing

3. **功能增强**
   - 支持工作流取消
   - 支持步骤超时控制
   - 支持条件分支（基于前序步骤结果）

4. **测试覆盖**
   - 单元测试覆盖率 80%+
   - 集成测试（使用 testcontainers）
   - 压力测试工具

## 七、总结

### 7.1 代码质量评估
| 维度 | 评分 | 说明 |
|-----|------|------|
| **架构设计** | ⭐⭐⭐⭐⭐ | 清晰的分层架构，职责明确 |
| **代码规范** | ⭐⭐⭐⭐⭐ | 命名规范，注释完整，错误处理得当 |
| **扩展性** | ⭐⭐⭐⭐☆ | 接口抽象良好，易于扩展新的 Broker/Store |
| **可维护性** | ⭐⭐⭐⭐☆ | 代码简洁，逻辑清晰 |
| **文档完善度** | ⭐⭐⭐⭐⭐ | 完整的使用指南和示例 |

### 7.2 核心优势
1. **分布式友好**：基于 PostgreSQL 状态管理，支持多实例部署
2. **解耦设计**：Orchestrator 和 Worker 完全分离
3. **灵活路由**：支持队列标签、优先级、自定义路由
4. **生产就绪**：完善的错误处理、重试机制、状态持久化

### 7.3 使用建议
- **小规模系统**：使用 example_simple 作为起点
- **生产系统**：参考 demo/main.go，增加监控和告警
- **高可用部署**：Worker 水平扩展 + Orchestrator 主备
- **性能调优**：根据任务类型调整并发数和队列权重

---

**完成时间**：2026-03-02  
**代码版本**：nsp-common/pkg/taskqueue (latest)  
**测试状态**：✅ 编译通过，示例可运行
