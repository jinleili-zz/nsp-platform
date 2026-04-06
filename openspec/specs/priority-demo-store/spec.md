# priority-demo-store Specification

## Purpose
TBD - created by archiving change refactor-priority-demo. Update Purpose after archive.
## Requirements
### Requirement: Store 包提供队列常量
`store` 包 SHALL 定义以下队列名称常量，多词用 `:` 分隔：
- `TaskQueueHigh          = "nsp:taskqueue:high"`
- `TaskQueueMiddle        = "nsp:taskqueue:middle"`
- `TaskQueueLow           = "nsp:taskqueue:low"`
- `CallbackQueueOrder     = "nsp:taskqueue:callback:order"`   // 订单类任务回调队列
- `CallbackQueueNotify    = "nsp:taskqueue:callback:notify"`  // 通知类任务回调队列
- `DefaultQueue           = TaskQueueMiddle`

#### Scenario: 任务队列常量值正确
- **WHEN** 代码引用 `store.TaskQueueHigh`
- **THEN** 其值为 `"nsp:taskqueue:high"`

#### Scenario: 双回调队列常量值正确
- **WHEN** 代码引用 `store.CallbackQueueOrder` 和 `store.CallbackQueueNotify`
- **THEN** 其值分别为 `"nsp:taskqueue:callback:order"` 和 `"nsp:taskqueue:callback:notify"`

### Requirement: MustRedisAddr 读取环境变量
`store.MustRedisAddr()` SHALL 读取环境变量 `REDIS_ADDR`，若为空则调用 `log.Fatal`。

#### Scenario: 环境变量已设置
- **WHEN** `REDIS_ADDR=127.0.0.1:6379` 时调用 `MustRedisAddr()`
- **THEN** 返回 `"127.0.0.1:6379"`

### Requirement: 内存 TaskStore 提供任务 CRUD
`TaskStore` SHALL 提供 `Create`、`GetByID`、`UpdateStatus`、`UpdateResult`、`IncrementRetry`、`UpdateBrokerTaskID`、`ListByStatus` 方法，并使用 `sync.RWMutex` 保证并发安全。

#### Scenario: 创建并查询任务
- **WHEN** 调用 `Create` 写入一条任务后调用 `GetByID`
- **THEN** 返回与写入内容一致的任务记录

#### Scenario: 更新任务状态
- **WHEN** 调用 `UpdateStatus` 设置状态为 `"running"`
- **THEN** 再次 `GetByID` 返回的任务 `Status` 字段为 `"running"`

