# TaskQueue Inspector 设计文档

## 背景

taskqueue 包定位为一个**消息队列平台抽象层**，屏蔽 asynq、RocketMQ 等不同后端的实现差异。
当前已有 `Broker`（发布）和 `Consumer`（消费）两层抽象，但缺少 **Inspector（运维监控）** 层。

目前 demo 中直接依赖 `asynq.Inspector`，破坏了抽象封装。本文档设计统一的 Inspector 接口层。

---

## 现有架构

```
taskqueue/
├── Broker    interface    → Publish + Close
├── Consumer  interface    → Handle + Start + Stop
├── Store     interface    → 持久化层（PostgreSQL）
├── Engine    struct       → 编排核心（提交/回调/查询 Workflow）
│
├── asynqbroker/           → asynq 实现（Broker + Consumer）
└── rocketmqbroker/        → RocketMQ 实现（Broker + Consumer）
```

---

## 核心设计难点

asynq 与 RocketMQ 的运维能力存在本质差异：

| 能力 | asynq | RocketMQ |
|------|-------|----------|
| 队列统计 | `GetQueueInfo` 返回各状态精确计数 | Admin API 查 Topic 消费进度（lag） |
| 任务列表查询 | 按状态分类（pending/active/retry/archived/completed） | 无"任务状态"概念，通过 MsgId/offset 查 |
| 单任务操作 | Delete / Run / Archive / CancelProcessing | 无原生等价物 |
| 批量操作 | DeleteAll / RunAll / ArchiveAll 等 | 不适用 |
| 暂停/恢复 | PauseQueue / UnpauseQueue | 通过关闭 Consumer 实现，无队列级 API |
| 服务器信息 | `Servers()` 列出在线 worker 详情 | 通过 NameServer Admin 查询 |

---

## 设计原则

1. **最大公约数** — 核心接口只包含所有后端都能合理实现的方法，调用方无需处理 `ErrNotSupported`
2. **按职责拆分** — 不做一个巨型接口，按能力分为多个独立接口
3. **可选实现** — 通过接口断言检测后端是否支持某项能力，调用方自行决策
4. **独立于 Broker** — 不修改现有 `Broker` 接口，各 broker 包单独提供 `NewInspector()`
5. **职责单一** — Inspector 是基础设施运维工具，与 Engine（业务编排）分离，各自独立使用

---

## 接口设计

### 整体分层

```
                      业务层
                         │
          ┌──────────────┼──────────────┐
          ▼              ▼              ▼
       Engine        Inspector      Consumer
    (Workflow编排)   (运维监控)       (消费)
          │              │
          │              ├── TaskReader      (任务查询，可选)
          │              ├── TaskController  (任务操作，可选)
          │              └── QueueController (队列操作，可选)
          │
          └─────► Broker (发布)
```

**关键设计**：Inspector 与 Engine 是平级的独立组件，不存在注入关系。
- Engine 负责 Workflow 层面的编排（submit / callback / query workflow）
- Inspector 负责 Broker 层面的运维（队列统计 / 任务查询 / 服务器信息）

---

### 第 1 层：Inspector（核心，所有后端必须完整实现）

```go
// Inspector 提供对消息队列运行状态的只读查询能力。
// 核心接口只包含所有后端都能合理实现的方法。
// 所有方法都应正常返回结果，调用方无需处理 ErrNotSupported。
type Inspector interface {
    // Queues 返回所有队列名称列表。
    Queues(ctx context.Context) ([]string, error)

    // GetQueueStats 返回指定队列的实时统计快照。
    GetQueueStats(ctx context.Context, queue string) (*QueueStats, error)

    // ListWorkers 返回当前在线的 worker 实例信息。
    // 若后端返回的信息不完整（如缺少 PID），相应字段填零值。
    ListWorkers(ctx context.Context) ([]*WorkerInfo, error)

    // Close 释放 Inspector 持有的连接资源。
    // 实现必须保证幂等：多次调用不应 panic 或返回错误。
    Close() error
}
```

---

### 第 2 层：TaskReader（可选，支持任务级查询的后端实现）

```go
// TaskReader 提供任务级别的查询能力。
// 仅支持此能力的后端（如 asynq）实现此接口。
// 调用方通过接口断言检测：
//   if tr, ok := inspector.(taskqueue.TaskReader); ok { ... }
type TaskReader interface {
    // GetTaskInfo 查询指定任务的详细信息。
    GetTaskInfo(ctx context.Context, queue, taskID string) (*TaskDetail, error)

    // ListTasks 按状态分页列出任务。
    // opts 为 nil 时使用默认分页（第 1 页，每页 30 条）。
    ListTasks(ctx context.Context, queue string, state TaskState, opts *ListOptions) (*TaskListResult, error)
}
```

---

### 第 3 层：TaskController（可选，支持任务级写操作的后端实现）

```go
// TaskController 提供对单个或批量任务的状态变更操作。
// 仅支持此能力的后端（如 asynq）实现此接口。
// 调用方通过接口断言检测：
//   if tc, ok := inspector.(taskqueue.TaskController); ok { ... }
type TaskController interface {
    // DeleteTask 删除指定任务（不可删除 active 状态的任务）。
    DeleteTask(ctx context.Context, queue, taskID string) error

    // RunTask 将 scheduled/retry/failed 状态的任务立即提升为 pending。
    RunTask(ctx context.Context, queue, taskID string) error

    // ArchiveTask 将 pending/scheduled/retry 的任务移入死信（归档）。
    ArchiveTask(ctx context.Context, queue, taskID string) error

    // CancelTask 向正在执行的任务发送取消信号（尽力而为，不保证成功）。
    CancelTask(ctx context.Context, taskID string) error

    // BatchDeleteTasks 批量删除指定状态的所有任务，返回删除数量。
    // 注意：状态与 TaskState 枚举一一对应，不做合并（见 TaskState 说明）。
    BatchDeleteTasks(ctx context.Context, queue string, state TaskState) (int, error)

    // BatchRunTasks 批量将指定状态的所有任务提升为 pending，返回操作数量。
    BatchRunTasks(ctx context.Context, queue string, state TaskState) (int, error)

    // BatchArchiveTasks 批量将指定状态的所有任务归档，返回操作数量。
    BatchArchiveTasks(ctx context.Context, queue string, state TaskState) (int, error)
}
```

---

### 第 4 层：QueueController（可选，支持队列级操作的后端实现）

```go
// QueueController 提供队列级别的管理操作。
// 仅支持此能力的后端（如 asynq）实现此接口。
// 调用方通过接口断言检测：
//   if qc, ok := inspector.(taskqueue.QueueController); ok { ... }
type QueueController interface {
    // PauseQueue 暂停队列，停止消费新任务。
    PauseQueue(ctx context.Context, queue string) error

    // UnpauseQueue 恢复队列消费。
    UnpauseQueue(ctx context.Context, queue string) error

    // DeleteQueue 删除队列。
    // force=false 时队列非空会返回错误；force=true 时强制删除。
    DeleteQueue(ctx context.Context, queue string, force bool) error
}
```

---

## 数据模型

### QueueStats — 队列统计快照

```go
// QueueStats 是某队列在某一时刻的统计快照。
type QueueStats struct {
    Queue     string    // 队列名称
    Pending   int       // 等待执行的任务数（不含 Scheduled）
    Scheduled int       // 延迟调度中的任务数
    Active    int       // 正在执行的任务数
    Retry     int       // 等待重试的任务数
    Failed    int       // 已失败的任务数（死信/归档）
    Completed int       // 已完成的任务数（需后端开启 Retention）

    // Paused 队列是否已暂停。
    // 仅部分后端（如 asynq）支持队列暂停功能，
    // 不支持的后端固定返回 false。
    Paused bool

    Timestamp time.Time // 统计时间点
}
```

各后端字段映射：

| QueueStats 字段 | asynq 映射 | RocketMQ 映射 |
|----------------|-----------|---------------|
| Pending | `QueueInfo.Pending` | Consumer offset lag |
| Scheduled | `QueueInfo.Scheduled` | 0（不适用） |
| Active | `QueueInfo.Active` | 0（无法获取正在处理的任务数） |
| Retry | `QueueInfo.Retry` | 重试队列消息数 |
| Failed | `QueueInfo.Archived` | 死信队列（DLQ）消息数 |
| Completed | `QueueInfo.Completed` | 已 ACK 消息累计数 |
| Paused | `QueueInfo.Paused` | 固定为 false |

---

### TaskState — 统一任务状态

```go
// TaskState 是所有后端统一的任务状态枚举。
// 各状态语义明确，不做合并，保证 Batch 操作行为清晰。
type TaskState string

const (
    // TaskStatePending 任务等待立即执行。
    TaskStatePending TaskState = "pending"

    // TaskStateScheduled 任务延迟调度中（有 NextProcessAt）。
    TaskStateScheduled TaskState = "scheduled"

    // TaskStateActive 任务正在被 worker 执行。
    TaskStateActive TaskState = "active"

    // TaskStateRetry 任务执行失败，等待重试。
    TaskStateRetry TaskState = "retry"

    // TaskStateFailed 任务已进入死信/归档，不再自动重试。
    TaskStateFailed TaskState = "failed"

    // TaskStateCompleted 任务已成功完成（需后端开启结果保留）。
    TaskStateCompleted TaskState = "completed"
)
```

asynq 状态映射：

| asynq 状态 | TaskState | 说明 |
|-----------|-----------|------|
| pending | TaskStatePending | |
| scheduled | TaskStateScheduled | |
| aggregating | TaskStatePending | 聚合等待中，业务视角属于待处理 |
| active | TaskStateActive | |
| retry | TaskStateRetry | |
| archived | TaskStateFailed | |
| completed | TaskStateCompleted | |

**设计说明**：保留 `TaskStateScheduled` 作为独立状态而非合并进 `Pending`，确保 `BatchDeleteTasks(ctx, queue, TaskStatePending)` 的语义清晰——只删除立即可执行的任务，不影响定时任务。

---

### TaskDetail — 任务详情

```go
// TaskDetail 是单个任务的完整信息视图。
type TaskDetail struct {
    ID            string     // Broker 分配的任务 ID
    Queue         string     // 所在队列
    Type          string     // 任务类型
    State         TaskState  // 当前状态
    MaxRetry      int        // 最大重试次数
    Retried       int        // 已重试次数
    LastError     string     // 最近一次错误信息
    NextProcessAt *time.Time // 下次执行时间（scheduled/retry 时有值）
    CreatedAt     *time.Time // 任务创建时间
    CompletedAt   *time.Time // 任务完成时间（completed 时有值）

    // Payload 是业务原始数据（JSON），不含内部 wrapper。
    // 实现方应在返回前去除 trace envelope 等内部封装，
    // 保证调用方看到的是可读的业务 payload。
    Payload []byte
}
```

---

### ListOptions / TaskListResult — 分页

```go
// ListOptions 任务列表查询的分页参数。
type ListOptions struct {
    Page     int // 页码，从 1 开始，默认 1
    PageSize int // 每页条数，默认 30，最大 100
}

// TaskListResult 分页查询结果。
type TaskListResult struct {
    Tasks []*TaskDetail
    Total int // 符合条件的总数
}
```

---

### WorkerInfo — Worker 实例信息

```go
// WorkerInfo 描述一个在线的 worker 服务实例。
type WorkerInfo struct {
    ID          string    // 实例唯一标识
    Host        string    // 主机名或 Pod 名称
    PID         int       // 进程 ID（不支持时为 0）
    Queues      []string  // 监听的队列列表
    StartedAt   time.Time // 启动时间
    ActiveTasks int       // 当前正在处理的任务数
}
```

---

### 错误定义

```go
var (
    // ErrQueueNotFound 队列不存在。
    ErrQueueNotFound = errors.New("taskqueue: queue not found")

    // ErrTaskNotFound 任务不存在。
    ErrTaskNotFound = errors.New("taskqueue: task not found")
)
```

**注意**：核心 `Inspector` 接口不返回 `ErrNotSupported`，因为所有方法都要求后端完整实现。
可选接口（TaskReader/TaskController/QueueController）通过接口断言检测，不存在返回 `ErrNotSupported` 的场景。

---

## 各后端实现能力矩阵

| 接口 / 方法 | asynq | RocketMQ |
|------------|-------|----------|
| **Inspector（核心，必须实现）** | | |
| `Queues` | ✅ | ✅ |
| `GetQueueStats` | ✅ 全部字段 | ✅ 部分字段（Pending/Completed，其余为 0） |
| `ListWorkers` | ✅ 全部字段 | ✅ 部分字段（无 PID） |
| `Close` | ✅ | ✅ |
| **TaskReader（可选）** | | |
| `GetTaskInfo` | ✅ | ❌ 不实现此接口 |
| `ListTasks` | ✅ | ❌ 不实现此接口 |
| **TaskController（可选）** | | |
| `DeleteTask` | ✅ | ❌ 不实现此接口 |
| `RunTask` | ✅ | ❌ 不实现此接口 |
| `ArchiveTask` | ✅ | ❌ 不实现此接口 |
| `CancelTask` | ✅ | ❌ 不实现此接口 |
| `BatchDeleteTasks` | ✅ | ❌ 不实现此接口 |
| `BatchRunTasks` | ✅ | ❌ 不实现此接口 |
| `BatchArchiveTasks` | ✅ | ❌ 不实现此接口 |
| **QueueController（可选）** | | |
| `PauseQueue` | ✅ | ❌ 不实现此接口 |
| `UnpauseQueue` | ✅ | ❌ 不实现此接口 |
| `DeleteQueue` | ✅ | ❌ 不实现此接口 |

---

## 目录结构变更

```
taskqueue/
├── broker.go                        # 现有（不变）
├── consumer.go                      # 现有（不变）
├── store.go                         # 现有（不变）
├── task.go                          # 现有（不变）
├── engine.go                        # 现有（不变）
├── inspector.go                     # 新增：接口定义 + 数据模型
│
├── asynqbroker/
│   ├── broker.go                    # 不变
│   ├── consumer.go                  # 不变
│   └── inspector.go                 # 新增：实现全部 4 个接口
│
└── rocketmqbroker/
    ├── broker.go                    # 不变
    ├── consumer.go                  # 不变
    └── inspector.go                 # 新增：仅实现 Inspector 核心接口
```

---

## 使用示例

### 基本使用：队列统计与 Worker 信息

```go
// 创建 Inspector（独立于 Engine）
inspector := asynqbroker.NewInspector(redisOpt)
defer inspector.Close()

// 列出所有队列及统计
queues, _ := inspector.Queues(ctx)
for _, q := range queues {
    stats, _ := inspector.GetQueueStats(ctx, q)
    fmt.Printf("%s: pending=%d scheduled=%d active=%d retry=%d failed=%d\n",
        q, stats.Pending, stats.Scheduled, stats.Active, stats.Retry, stats.Failed)
}

// 查看在线 worker
workers, _ := inspector.ListWorkers(ctx)
for _, w := range workers {
    fmt.Printf("Worker %s (PID %d): active=%d, queues=%v\n",
        w.Host, w.PID, w.ActiveTasks, w.Queues)
}
```

### 可选能力探测：任务查询

```go
// 探测 TaskReader 能力
if tr, ok := inspector.(taskqueue.TaskReader); ok {
    // 分页列出失败任务
    result, _ := tr.ListTasks(ctx, queue, taskqueue.TaskStateFailed,
        &taskqueue.ListOptions{Page: 1, PageSize: 20})
    for _, t := range result.Tasks {
        fmt.Printf("TaskID: %s, Type: %s, Error: %s\n", t.ID, t.Type, t.LastError)
    }

    // 查询单个任务详情
    task, _ := tr.GetTaskInfo(ctx, queue, taskID)
    fmt.Printf("Payload: %s\n", string(task.Payload))
} else {
    fmt.Println("current backend does not support task-level queries")
}
```

### 可选能力探测：任务操作

```go
// 探测 TaskController 能力
if tc, ok := inspector.(taskqueue.TaskController); ok {
    // 将所有失败任务重新投入执行
    n, _ := tc.BatchRunTasks(ctx, queue, taskqueue.TaskStateFailed)
    fmt.Printf("Requeued %d failed tasks\n", n)

    // 删除所有已完成任务
    n, _ = tc.BatchDeleteTasks(ctx, queue, taskqueue.TaskStateCompleted)
    fmt.Printf("Deleted %d completed tasks\n", n)

    // 取消正在执行的任务
    tc.CancelTask(ctx, taskID)
}
```

### 可选能力探测：队列操作

```go
// 探测 QueueController 能力
if qc, ok := inspector.(taskqueue.QueueController); ok {
    // 高峰期暂停低优先级队列
    qc.PauseQueue(ctx, "taskqueue:business:task:send:priority:low")

    // 恢复队列
    qc.UnpauseQueue(ctx, "taskqueue:business:task:send:priority:low")

    // 强制删除废弃队列
    qc.DeleteQueue(ctx, "taskqueue:deprecated:old_queue", true)
}
```

### 业务层同时使用 Engine 和 Inspector

```go
// Engine 和 Inspector 是平级独立组件，业务层各自持有
engine, _ := taskqueue.NewEngine(&taskqueue.Config{
    DSN:           dsn,
    CallbackQueue: "callbacks",
}, broker)

inspector := asynqbroker.NewInspector(redisOpt)

// Engine 负责工作流编排
workflowID, _ := engine.SubmitWorkflow(ctx, def)
status, _ := engine.QueryWorkflow(ctx, workflowID)

// Inspector 负责运维监控
stats, _ := inspector.GetQueueStats(ctx, "tasks_high")
workers, _ := inspector.ListWorkers(ctx)
```

---

## 实现注意事项

### asynq 实现

1. `asynqbroker.Inspector` 内部持有 `*asynq.Inspector`，同时实现四个接口
2. `ListTasks` 每个 `TaskState` 独立查询对应的 asynq 方法：
   - `TaskStatePending` → `ListPendingTasks` + `ListAggregatingTasks`（合并结果）
   - `TaskStateScheduled` → `ListScheduledTasks`
   - `TaskStateFailed` → `ListArchivedTasks`
   - 等等
3. `GetTaskInfo` 返回的 `TaskDetail.Payload` 需调用 `unwrapEnvelope()` 去除 trace envelope，保证返回纯业务数据
4. `GetTaskInfo` 对于 asynq aggregating 状态的任务，`TaskDetail.State` 返回 `TaskStatePending`
5. `BatchDeleteTasks(queue, TaskStatePending)` 对应 `DeleteAllPendingTasks` + `DeleteAllAggregatingTasks`
6. `BatchRunTasks(queue, TaskStateFailed)` 对应 `RunAllArchivedTasks`

### RocketMQ 实现

1. `rocketmqbroker.Inspector` 只实现 `Inspector` 核心接口，不实现可选接口
2. `GetQueueStats` 通过 RocketMQ Admin API 的 `examineConsumeStats` 获取 lag（Pending）和 TPS
3. `ListWorkers` 通过 `examineConsumerConnectionInfo` 获取消费者连接信息
4. `Scheduled` / `Active` / `Retry` / `Paused` 等字段固定返回零值（RocketMQ 无对应概念）

### 编译期接口检查

在各实现文件中加入编译期断言，确保接口实现完整：

```go
// asynqbroker/inspector.go
var (
    _ taskqueue.Inspector       = (*Inspector)(nil)
    _ taskqueue.TaskReader      = (*Inspector)(nil)
    _ taskqueue.TaskController  = (*Inspector)(nil)
    _ taskqueue.QueueController = (*Inspector)(nil)
)

// rocketmqbroker/inspector.go
var _ taskqueue.Inspector = (*Inspector)(nil)
```

---

## 未来方向

### 统一工厂创建

当前各 broker 包独立提供 `NewInspector()` 工厂函数，调用方需感知具体后端：

```go
asynqbroker.NewInspector(redisOpt)
rocketmqbroker.NewInspector(nameServer)
```

未来 Platform 门面层可提供统一工厂，根据配置自动选择后端实现：

```go
// 未来方向（当前不实现）
inspector, _ := taskqueue.NewInspector(taskqueue.InspectorConfig{
    Backend: "asynq",
    Redis:   redisOpt,
})
```

---

## 设计决策记录

| 问题 | 决策 | 原因 |
|------|------|------|
| GetTaskInfo/ListTasks 放哪 | 独立为 TaskReader 可选接口 | 遵循"最大公约数"原则，核心接口不含 ErrNotSupported |
| Inspector 与 Engine 的关系 | 平级独立组件 | Engine 负责 Workflow 编排，Inspector 负责 Broker 运维，职责分离 |
| TaskState 是否合并 | 不合并，各状态独立 | 确保 Batch 操作语义清晰，如 BatchDeleteTasks(Pending) 不会误删 Scheduled |
| aggregating 状态处理 | 映射到 TaskStatePending | 从业务视角，聚合等待中的任务属于"待处理" |
| Payload 是否包含 envelope | 返回 unwrap 后的纯业务数据 | 保证运维人员可读 |
| QueueStats.Paused 字段 | 保留，godoc 标注部分后端不支持 | 便捷性优先，调用方知情 |
| Close() 幂等性 | 要求实现方保证幂等 | Go 资源清理惯例 |
