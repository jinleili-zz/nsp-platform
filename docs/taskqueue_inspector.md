# TaskQueue Inspector

`taskqueue` 的巡检能力被拆成四层接口，核心目标是让调用方只依赖稳定的公共抽象，同时允许不同 broker 后端按能力渐进实现。

当前仓库内只有 `asynqbroker.Inspector`，并且它实现了全部四层。

## 四层接口

### 1. `Inspector`

所有后端都应该实现的核心只读接口：

- `Queues(ctx)`
- `GetQueueStats(ctx, queue)`
- `ListWorkers(ctx)`
- `Close()`

### 2. `TaskReader`

任务级只读接口：

- `GetTaskInfo(ctx, queue, taskID)`
- `ListTasks(ctx, queue, state, opts)`

### 3. `TaskController`

任务级写操作接口：

- `DeleteTask`
- `RunTask`
- `ArchiveTask`
- `CancelTask`
- `BatchDeleteTasks`
- `BatchRunTasks`
- `BatchArchiveTasks`

### 4. `QueueController`

队列级操作接口：

- `PauseQueue`
- `UnpauseQueue`
- `DeleteQueue`

## 使用方式

```go
inspector := asynqbroker.NewInspector(asynq.RedisClientOpt{Addr: "127.0.0.1:6379"})
defer inspector.Close()

queues, err := inspector.Queues(ctx)
stats, err := inspector.GetQueueStats(ctx, "orders:high")
workers, err := inspector.ListWorkers(ctx)
```

可选能力通过接口断言获取：

```go
if tr, ok := inspector.(taskqueue.TaskReader); ok {
    detail, _ := tr.GetTaskInfo(ctx, "orders:high", "task-id")
    result, _ := tr.ListTasks(ctx, "orders:high", taskqueue.TaskStateFailed, nil)
    _ = detail
    _ = result
}

if tc, ok := inspector.(taskqueue.TaskController); ok {
    _ = tc.RunTask(ctx, "orders:high", "task-id")
}

if qc, ok := inspector.(taskqueue.QueueController); ok {
    _ = qc.PauseQueue(ctx, "orders:low")
}
```

## 数据模型

核心返回结构：

- `QueueStats`
- `TaskDetail`
- `TaskListResult`
- `WorkerInfo`

其中 `TaskDetail.Payload` 有一个明确约束：

- 返回的是业务原始 payload
- 不包含内部 trace envelope
- 调用方无需再自行解包 `_v/_tid/_rto/_meta`

这是当前 `asynqbroker.Inspector` 已保证的行为。

## 状态语义

统一状态枚举：

- `pending`
- `scheduled`
- `active`
- `retry`
- `failed`
- `completed`

在 asynq 适配中：

- `archived` 映射为 `failed`
- `aggregating` 视为 `pending`

## 错误

公共错误：

- `taskqueue.ErrQueueNotFound`
- `taskqueue.ErrTaskNotFound`

`asynqbroker.Inspector` 会尽量把后端私有错误转换成这两个公共错误，方便调用方统一处理。

## 当前实现说明

当前仓库内只有 `asynqbroker.Inspector`：

- 实现四层全部接口
- 支持队列暂停/恢复/删除
- 支持任务查询和批量操作
- 返回的 payload 已自动去 wrapper

仓库内不再提供 RocketMQ 的巡检实现。
