## 1. 清理旧代码

- [x] 1.1 删除 `examples/taskqueue-priority-demo/store/store.go`（整个 store 包）

## 2. 重写 producer

- [x] 2.1 重写 `cmd/producer/main.go`：初始化 trace 上下文，直接使用 `asynqbroker.Broker.Publish` 发送任务到 high/middle/low 队列，`task_id` 放入 payload，不设置 `Reply`，不封装 TaskManager
- [x] 2.2 在 producer 中注册所有任务类型（`send:payment:notification`、`deduct:inventory`、`send:email`、`send:notification`、`process:image`、`generate:report`、`export:data`、`always:fail`），按类型分配优先级队列

## 3. 重写 consumer

- [x] 3.1 重写 `cmd/consumer/main.go`：配置权重队列（high=30, middle=20, low=10），直接使用 `asynqbroker.Consumer` 注册 handler，handler 从 payload 解析 `task_id` 和业务参数并打印日志
- [x] 3.2 注册所有 8 种任务类型 handler，每个 handler 使用 `trace.MustTraceFromContext` 记录包含 trace_id 的日志，不发送回调

## 4. 新增 Inspector 示例

- [x] 4.1 在 producer 中使用 `asynqbroker.NewInspector` 查询三个队列的统计信息（Pending、Active、Completed、Failed）并打印

## 5. 编译与测试

- [x] 5.1 确认 `go build ./examples/taskqueue-priority-demo/...` 无编译错误
- [x] 5.2 启动 Redis，运行 consumer 和 producer，验证：任务按优先级被消费、日志包含 trace_id、inspector 输出队列统计
