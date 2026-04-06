## 1. 新建 store 包

- [x] 1.1 创建 `examples/taskqueue-priority-demo/store/store.go`，定义队列常量（`nsp:taskqueue:high/middle/low`、`nsp:taskqueue:callback:order`、`nsp:taskqueue:callback:notify`）、`MustRedisAddr()`、任务状态常量
- [x] 1.2 实现内存 `TaskStore`（`Create`、`GetByID`、`UpdateStatus`、`UpdateResult`、`IncrementRetry`、`UpdateBrokerTaskID`、`ListByStatus`），使用 `sync.RWMutex` 保证并发安全

## 2. 重构 producer

- [x] 2.1 重写 `cmd/producer/main.go`：初始化 trace 上下文（`TraceContext`、`logger.ContextWithTraceID`）
- [x] 2.2 实现 `TaskManager`（含 `SubmitTask`、`SubmitTaskWithPriority`、`HandleCallback`、`QueryTask`），复用 `store.TaskStore` 和 `asynqbroker.Broker`
- [x] 2.3 实现双回调消费者：分别启动监听 `nsp:taskqueue:callback:order` 和 `nsp:taskqueue:callback:notify` 的消费者，各自注册 `broker_task_callback` handler 调用 `manager.HandleCallback`
- [x] 2.4 将发送任务逻辑改为使用 `TaskManager.SubmitTaskWithPriority`，订单类任务（`send:payment:notification`、`deduct:inventory`、`generate:report`、`export:data`）的 `Reply.Queue` 设为 `store.CallbackQueueOrder`，通知类任务（`send:email`、`send:notification`、`process:image`）设为 `store.CallbackQueueNotify`

## 3. 重构 consumer

- [x] 3.1 重写 `cmd/consumer/main.go`：实现 `CallbackSender`（`Success`、`Fail`、`send`），`send` 方法读取 `task.Reply.Queue` 路由回调，不硬编码队列名
- [x] 3.2 注册所有任务类型 handler（`send:payment:notification`、`deduct:inventory`、`send:email`、`send:notification`、`process:image`、`generate:report`、`export:data`、`always:fail`），每个 handler 内使用 `trace.MustTraceFromContext` 记录日志，执行后发送回调
- [x] 3.3 配置 consumer 权重队列：`nsp:taskqueue:high=30`、`nsp:taskqueue:middle=20`、`nsp:taskqueue:low=10`

## 4. 清理旧代码

- [x] 4.1 删除 `internal/config/config.go` 和 `internal/handler/handler.go`（及空目录）

## 5. 编译与测试

- [x] 5.1 确认 `go build ./examples/taskqueue-priority-demo/...` 无编译错误
- [x] 5.2 启动 Redis，运行 consumer 和 producer，验证：任务按优先级被消费、订单类回调发往 `nsp:taskqueue:callback:order`、通知类回调发往 `nsp:taskqueue:callback:notify`、日志包含 trace_id
