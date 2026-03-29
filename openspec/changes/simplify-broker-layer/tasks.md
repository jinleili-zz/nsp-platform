## 1. 清理 workflow 层代码

- [ ] 1.1 删除 `taskqueue/engine.go`（Engine/Config/QueueRouterFunc/CallbackSender 及所有 workflow 编排逻辑）
- [ ] 1.2 删除 `taskqueue/store.go`（Store 接口定义）
- [ ] 1.3 删除 `taskqueue/pg_store.go`（PostgresStore 实现）
- [ ] 1.4 删除 `taskqueue/engine_test.go`（workflow 相关单元测试）
- [ ] 1.5 删除 `taskqueue/migrations/` 目录（数据库建表 SQL）

## 2. 清理 RocketMQ 实现

- [ ] 2.1 删除 `taskqueue/rocketmqbroker/` 整个目录（broker.go/consumer.go/inspector.go）
- [ ] 2.2 从 go.mod 中移除 `github.com/apache/rocketmq-client-go/v2` 依赖，运行 `go mod tidy`

## 3. 重构 broker 核心类型（taskqueue/task.go）

- [ ] 3.1 新增 `ReplySpec` 结构体（含 `Queue string` 字段）
- [ ] 3.2 重写 `Task` 结构体：保留 Type/Payload/Queue/Priority/Metadata，新增 `Reply *ReplySpec` 字段
- [ ] 3.3 保留 `TaskInfo` 结构体（BrokerTaskID/Queue）不变
- [ ] 3.4 保留 `Priority` 类型及四个常量（Low/Normal/High/Critical）
- [ ] 3.5 删除 `TaskPayload`、`TaskResult`、`CallbackPayload` 结构体
- [ ] 3.6 删除所有 Workflow 相关类型：`WorkflowStatus`、`StepStatus`、`Workflow`、`StepTask`、`StepStats`、`StepDefinition`、`WorkflowDefinition`、`WorkflowStatusResponse`、`WorkflowHooks`

## 4. 重构 HandlerFunc 签名（taskqueue/handler.go）

- [ ] 4.1 修改 `HandlerFunc` 签名为 `func(ctx context.Context, task *Task) error`，删除对 TaskPayload/TaskResult 的依赖

## 5. 更新 asynq trace envelope（taskqueue/asynqbroker/trace_envelope.go）

- [ ] 5.1 扩展 `taskEnvelope` 结构体，新增两个字段：`ReplyTo *json.RawMessage`（JSON tag: `_rto,omitempty`）和 `Meta map[string]string`（JSON tag: `_meta,omitempty`）
- [ ] 5.2 更新 `wrapWithTrace` 函数：接收 `*ReplySpec` 和 `Metadata` 参数，将 ReplySpec 序列化为 JSON 对象写入 `_rto`，Metadata 写入 `_meta`；触发条件为 TraceContext 存在 OR Reply 非 nil OR Metadata 非空
- [ ] 5.3 更新 `unwrapEnvelope` 函数：返回值扩展，分别提取 trace 字段、`_rto`（反序列化为 `*ReplySpec`）和 `_meta`（还原为 `map[string]string`）
- [ ] 5.4 更新 `trace_envelope_test.go`：覆盖 ReplySpec 序列化/反序列化、Metadata 序列化/反序列化、ReplySpec+Metadata 同时存在的往返测试

## 6. 更新 asynq Broker 实现（taskqueue/asynqbroker/broker.go）

- [ ] 6.1 更新 `Publish` 方法：将 `task.Reply` 和 `task.Metadata` 传递给 `wrapWithTrace`

## 7. 重构 asynq Consumer 实现（taskqueue/asynqbroker/consumer.go）

- [ ] 7.1 重写 `Handle` 方法：调用新的 `unwrapEnvelope` 获取 traceMeta/reply/businessMeta，恢复 TraceContext 并构造完整 `*taskqueue.Task`（含 Type/Payload/Queue/Reply/Metadata），直接传递给 HandlerFunc
- [ ] 7.2 删除 `HandleRaw` 方法（不再需要，HandlerFunc 已是通用签名）
- [ ] 7.3 删除对 `TaskPayload` 的 JSON 反序列化逻辑（`task_id`/`resource_id`/`task_params` 格式）

## 8. 更新 asynq Inspector（taskqueue/asynqbroker/inspector.go）

- [ ] 8.1 确认 `convertTaskInfo` 中的 envelope 解包逻辑适配新的 `unwrapEnvelope` 签名
- [ ] 8.2 验证所有四层接口实现编译通过

## 9. 编写测试

- [ ] 9.1 新增 `taskqueue/broker_test.go`：测试 Task/ReplySpec 结构体字段、Priority 常量
- [ ] 9.2 更新 `taskqueue/asynqbroker/trace_envelope_test.go`：覆盖 ReplySpec 在 envelope 中的往返测试
- [ ] 9.3 验证所有测试通过：`go test ./taskqueue/...`

## 10. 更新示例代码

- [ ] 10.1 删除 `examples/taskqueue-workflow-demo/` 目录
- [ ] 10.2 更新 `examples/taskqueue-broker-demo/`：handler 签名改为 `func(ctx, *taskqueue.Task) error`，producer 发送 Task 时使用 `Reply: &taskqueue.ReplySpec{Queue: "..."}`
- [ ] 10.3 更新 `examples/taskqueue-priority-demo/`：适配新的 HandlerFunc 签名

## 11. 清理与验证

- [ ] 11.1 运行 `go mod tidy`，确认 rocketmq-client-go 和 lib/pq 的直接依赖已移除
- [ ] 11.2 运行 `go build ./...` 确认全项目编译通过
- [ ] 11.3 运行 `go vet ./taskqueue/...` 确认无代码问题
- [ ] 11.4 运行 `go test ./taskqueue/...` 确认所有测试通过
