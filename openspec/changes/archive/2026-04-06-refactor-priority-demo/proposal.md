## Why

`taskqueue-priority-demo` 基于旧版 API（自定义 `config` 包、`Inspector`、独立的 `internal/config` / `internal/handler` 目录）实现，与 `taskqueue-broker-demo` 重构后的代码风格不一致。需要将其改造为与最新 `broker-demo` 一致的实现模式，统一项目内 demo 的代码风格与最佳实践。

同时，新增双回调队列设计：producer 侧设置两条回调队列，任务消息在发送时携带目标回调队列信息，consumer 处理完成后根据消息中携带的 `Reply` 信息将结果发回对应队列，演示一对多回调路由能力。

## What Changes

- 删除 `internal/config/config.go` 中对 `github.com/hibiken/asynq` 的直接依赖，改用 `store` 包统一管理队列常量和 Redis 连接
- 删除 `internal/handler/handler.go` 中独立的 `TaskHandler` 类型定义，改为在 consumer 内联注册 handler 函数
- 重构 `cmd/producer/main.go`：参照 `broker-demo` 引入 `store.TaskStore`、`TaskManager`、回调消费者、trace 上下文初始化
- 重构 `cmd/consumer/main.go`：参照 `broker-demo` 直接在 main 注册 handler，支持 `CallbackSender` 发送结果回调
- 队列命名改为与 `broker-demo` 一致的 `nsp:taskqueue:*` 命名空间，并保留三级优先级（high / middle / low），多词用 `:` 分隔
- **新增双回调队列**：定义 `nsp:taskqueue:callback:order`（订单类任务回调）和 `nsp:taskqueue:callback:notify`（通知类任务回调）两条独立回调队列，取代单一 callback 队列
- 发送任务时在 `taskqueue.Task.Reply.Queue` 中携带目标回调队列名称，consumer 处理完成后读取 `task.Reply.Queue` 路由回调
- 新增 `store/store.go`，提供内存 `TaskStore` 实现和所有队列常量
- 功能与原 demo 保持一致：三优先级队列按权重消费、多种任务类型处理

## Capabilities

### New Capabilities

- `priority-demo-store`: 为 priority-demo 新增 `store` 包，提供内存任务存储、队列常量（含双回调队列）、Redis 连接工具函数
- `priority-demo-refactor`: 将 producer 和 consumer 改造为与 broker-demo 一致的代码模式，含 trace 集成、双回调队列路由、TaskManager

### Modified Capabilities

（无已有 spec 需要修改）

## Impact

- 影响目录：`examples/taskqueue-priority-demo/`
- 新增文件：`examples/taskqueue-priority-demo/store/store.go`
- 重写文件：`cmd/producer/main.go`、`cmd/consumer/main.go`
- 删除文件：`internal/config/config.go`、`internal/handler/handler.go`（以及 `internal/` 目录）
- 依赖无变化，复用 `go.mod` 中已有依赖（`asynq`、`uuid`、`trace`、`logger`）
