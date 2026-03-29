## 1. 定义 ReplySpec 结构体

- [ ] 1.1 在 `taskqueue/task.go` 中新增 `ReplySpec` 结构体（含 `Queue string` 字段及 godoc 注释）
- [ ] 1.2 将 `Task.ReplyTo string` 字段改为 `Task.Reply *ReplySpec`

## 2. 更新 asynq trace envelope

- [ ] 2.1 更新 `taskqueue/asynqbroker/trace_envelope.go` 中的 `taskEnvelope` 结构体：`ReplyTo string` → `Reply json.RawMessage`（存储序列化后的 ReplySpec JSON）
- [ ] 2.2 更新 `wrapWithTrace`：将 `Task.Reply` 序列化为 JSON 对象写入 `_rto`；envelope 触发条件保持「TraceContext 存在 OR Reply 非 nil OR Metadata 非空」
- [ ] 2.3 更新 `unwrapEnvelope`：从 `_rto` 恢复 `*ReplySpec`，先尝试解析为 JSON 对象 `{"queue":"..."}` ，失败则尝试解析为纯字符串并构造 `&ReplySpec{Queue: s}`（兼容旧格式）

## 3. 更新 asynq Consumer

- [ ] 3.1 更新 `taskqueue/asynqbroker/consumer.go` 中的 `Handle` 方法：构造 Task 时将 `ReplyTo string` 改为 `Reply *ReplySpec`

## 4. 更新测试

- [ ] 4.1 更新 `taskqueue/asynqbroker/trace_envelope_test.go`：覆盖 ReplySpec JSON 格式的序列化/反序列化测试
- [ ] 4.2 新增旧格式兼容测试：`_rto` 为纯字符串时能正确恢复为 `*ReplySpec`
- [ ] 4.3 运行 `go test ./taskqueue/...` 确认全部通过

## 5. 更新示例代码

- [ ] 5.1 将 `examples/` 中所有 `Task.ReplyTo = "..."` 改为 `Task.Reply = &taskqueue.ReplySpec{Queue: "..."}`
- [ ] 5.2 将 handler 内 `task.ReplyTo` 的读取改为 `task.Reply.Queue`（注意 nil 判断）

## 6. 更新 simplify-broker-layer 变更中的 spec

- [ ] 6.1 更新 `openspec/changes/simplify-broker-layer/specs/broker-core/spec.md`：将 `ReplyTo string` 描述改为 `Reply *ReplySpec` 及 ReplySpec 定义
- [ ] 6.2 更新 `openspec/changes/simplify-broker-layer/specs/broker-asynq/spec.md`：将所有 `_rto` 格式描述改为 JSON 对象格式，补充兼容策略

## 7. 验证

- [ ] 7.1 运行 `go build ./...` 确认全项目编译通过
- [ ] 7.2 运行 `go vet ./taskqueue/...` 确认无代码问题
