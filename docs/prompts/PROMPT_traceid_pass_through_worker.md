# Trace ID 跨消息队列透传实现提示词

> 本文件记录 az-nsp → worker → az-nsp callback 全链路 Trace ID 透传的实现方案提示词。
> 设计背景和 Review 过程见本目录下相关文档。

---

```
你是一名 Go 工程师，需要为 nsp-common/pkg/taskqueue/asynqbroker/ 包实现
跨消息队列的 Trace ID 透传机制。以下是完整的实现要求。

## 背景与约束

系统使用 B3 Trace 协议（nsp-common/pkg/trace），HTTP 链路已支持 trace 传播，
现需将同样的 trace 上下文通过 asynq（Redis 消息队列）透传到异步 worker，
以及从 worker 透传回 az-nsp 的 callback 消费路径。

核心约束：
- 所有改动仅限于 nsp-common/pkg/taskqueue/asynqbroker/ 和 nsp-common/pkg/trace/
- vpc_workflow_demo 目录的业务代码不得修改
- trace 注入/提取必须对调用方完全透明（如同 TracedClient 对 HTTP 的处理方式）

## 现有代码结构（只读，不得修改）

### pkg/trace/context.go
  TraceContext{ TraceID, SpanId, ParentSpanId, InstanceId, Sampled }
  ContextWithTrace(ctx, tc) context.Context
  TraceFromContext(ctx) (*TraceContext, bool)

### pkg/trace/generator.go
  NewSpanId() string
  GetInstanceId() string

### pkg/logger（与 trace 联动，必须同步注入）
  logger.ContextWithTraceID(ctx, traceID) context.Context
  logger.ContextWithSpanID(ctx, spanID)   context.Context

### asynqbroker/broker.go（当前）
  Publish(ctx, task) 中 ctx 里的 trace 信息被完全丢弃，
  只有 task.Payload 原样进入 asynq。

### asynqbroker/consumer.go（当前）
  Handle()    中 asynq 给 handler 的是空 ctx，无任何 trace 信息。
  HandleRaw() 中直接注册原始 handler，无任何 trace 处理。

### taskqueue/task.go（当前）
  Task 结构体已有 Metadata map[string]string 字段（当前未使用）。

## 任务一：新建 asynqbroker/trace_envelope.go

在包内创建该文件。文件职责：定义 envelope 格式、提供 wrap/unwrap 两个内部函数，
以及一个导出的 UnwrapEnvelope 用于需要手动处理原始 payload 的场景。

### 1. Envelope 结构体（包内私有）

  type taskEnvelope struct {
      Version  int               `json:"_v"`               // 固定为 1，用于可靠检测
      TraceID  string            `json:"_tid,omitempty"`
      SpanID   string            `json:"_sid,omitempty"`   // 发布方 SpanId → 消费方 ParentSpanId
      Sampled  bool              `json:"_smpl"`
      Payload  json.RawMessage   `json:"payload"`          // 原始业务 payload
  }

  检测逻辑说明：通过 Version == 1 判断是否为 envelope 格式，
  避免用 payload != nil 误判（业务 JSON 中可能存在 "payload" 字段）。

### 2. wrapWithTrace(ctx context.Context, payload []byte) []byte（包内私有）

  逻辑：
  1. 调用 trace.TraceFromContext(ctx) 尝试提取 TraceContext
  2. 若提取失败（ctx 无 trace），直接返回原始 payload（降级，不影响消息投递）
  3. 若成功，构造 taskEnvelope{Version:1, TraceID, SpanID:tc.SpanId, Sampled, Payload:payload}
  4. json.Marshal 后返回；若序列化失败，降级返回原始 payload

### 3. UnwrapEnvelope(data []byte) (payload []byte, metadata map[string]string)（导出）

  逻辑：
  1. 尝试将 data 反序列化为 taskEnvelope
  2. 若失败或 env.Version != 1，说明是旧格式 payload，返回 (data, nil)（向后兼容）
  3. 若成功，从 envelope 提取 metadata：
       metadata = map[string]string{
           "trace_id": env.TraceID,
           "span_id":  env.SpanID,
           "sampled":  "1",  // 默认
       }
       if !env.Sampled { metadata["sampled"] = "0" }
  4. 返回 (env.Payload, metadata)

### 4. injectTraceFromMetadata(ctx context.Context, metadata map[string]string) context.Context
   （包内私有）

  逻辑：
  1. 若 metadata 为空或 metadata["trace_id"] == ""，直接返回原 ctx（无 trace 场景降级）
  2. 构造新 TraceContext：
       tc := &trace.TraceContext{
           TraceID:      metadata["trace_id"],
           ParentSpanId: metadata["span_id"],     // 发布方 SpanId 成为当前的 ParentSpanId
           SpanId:       trace.NewSpanId(),        // 消费方自己生成新 SpanId
           InstanceId:   trace.GetInstanceId(),
           Sampled:      metadata["sampled"] != "0",
       }
  3. 必须同时注入 trace 包和 logger 包的 context（二者缺一不可）：
       ctx = trace.ContextWithTrace(ctx, tc)
       ctx = logger.ContextWithTraceID(ctx, tc.TraceID)  // ← logger 结构化日志必须
       ctx = logger.ContextWithSpanID(ctx, tc.SpanId)    // ← logger 结构化日志必须
  4. 返回新 ctx

  重要说明：logger.ContextWithTraceID/SpanID 与 trace.ContextWithTrace 必须同时调用，
  否则 logger.InfoContext(ctx, ...) 的日志输出中不会携带 trace_id/span_id 字段，
  trace 透传在传输层成功但在日志可观测层是断的。
  参考：pkg/trace/middleware.go TraceMiddleware 的完整做法。

## 任务二：修改 asynqbroker/broker.go

在 Publish 方法中，将原来的：
  asynqTask := asynq.NewTask(task.Type, task.Payload)
改为：
  wrappedPayload := wrapWithTrace(ctx, task.Payload)
  asynqTask := asynq.NewTask(task.Type, wrappedPayload)

其余逻辑不变。

原理：Broker 自动从 ctx 提取 TraceContext 并包装，调用方无需感知，
与 TracedClient 对 HTTP 出站请求的处理方式保持一致。

## 任务三：修改 asynqbroker/consumer.go

### 3.1 修改 Handle 方法

在 mux.HandleFunc 内部，将原来直接 json.Unmarshal(t.Payload(), &raw) 改为：
先 UnwrapEnvelope，再 injectTraceFromMetadata，最后 Unmarshal 业务 payload：

原代码：
  c.mux.HandleFunc(taskType, func(ctx context.Context, t *asynq.Task) error {
      var raw struct { TaskID string...; ResourceID string...; TaskParams string... }
      if err := json.Unmarshal(t.Payload(), &raw); err != nil { ... }
      ...
      result, err := handler(ctx, payload)

改为：
  c.mux.HandleFunc(taskType, func(ctx context.Context, t *asynq.Task) error {
      rawBytes, metadata := UnwrapEnvelope(t.Payload())     // ← 新增：拆 envelope
      ctx = injectTraceFromMetadata(ctx, metadata)           // ← 新增：注入 trace（含 logger 同步）
      var raw struct { TaskID string...; ResourceID string...; TaskParams string... }
      if err := json.Unmarshal(rawBytes, &raw); err != nil { ... }
      ...
      result, err := handler(ctx, payload)                   // ctx 已含完整 TraceContext

### 3.2 修改 HandleRaw 方法

将直接注册改为包一层 wrapper，逻辑与 Handle 一致，
避免将 envelope/trace 细节泄漏给 main.go 等业务调用方：

原代码：
  func (c *Consumer) HandleRaw(taskType string, handler func(context.Context, *asynq.Task) error) {
      c.mux.HandleFunc(taskType, handler)
  }

改为：
  func (c *Consumer) HandleRaw(taskType string, handler func(context.Context, *asynq.Task) error) {
      c.mux.HandleFunc(taskType, func(ctx context.Context, t *asynq.Task) error {
          rawBytes, metadata := UnwrapEnvelope(t.Payload())
          ctx = injectTraceFromMetadata(ctx, metadata)
          return handler(ctx, asynq.NewTask(t.Type(), rawBytes))
      })
  }

说明：HandleRaw 的调用方（如 az-nsp/main.go）收到的 ctx 已自动注入 trace，
t.Payload() 是原始业务 payload，无需了解 envelope 细节。

## 任务四（可选但推荐）：在 pkg/trace/propagator.go 新增两个 helper

目的：为未来可能需要手动操作 metadata 的场景提供统一入口，
避免各处重复写 trace 提取/还原逻辑。

// MetadataFromContext 从 ctx 提取 trace 信息为 map，用于跨消息队列透传。
// ctx 无 TraceContext 时返回 nil。
func MetadataFromContext(ctx context.Context) map[string]string {
    tc, ok := TraceFromContext(ctx)
    if !ok || tc == nil {
        return nil
    }
    m := map[string]string{
        "trace_id": tc.TraceID,
        "span_id":  tc.SpanId,
        "sampled":  "1",
    }
    if !tc.Sampled {
        m["sampled"] = "0"
    }
    return m
}

// TraceFromMetadata 从 metadata map 还原 TraceContext。
// instanceId 由调用方传入（调用 GetInstanceId() 获取）。
// metadata 为空或无 trace_id 时返回 nil。
func TraceFromMetadata(metadata map[string]string, instanceId string) *TraceContext {
    if len(metadata) == 0 {
        return nil
    }
    traceID := metadata["trace_id"]
    if traceID == "" {
        return nil
    }
    return &TraceContext{
        TraceID:      traceID,
        ParentSpanId: metadata["span_id"],
        SpanId:       NewSpanId(),
        InstanceId:   instanceId,
        Sampled:      metadata["sampled"] != "0",
    }
}

注：asynqbroker 内部的 injectTraceFromMetadata 可调用 TraceFromMetadata 避免重复。

## 改动文件汇总

  文件                                    操作    说明
  ──────────────────────────────────────  ──────  ─────────────────────────────────────────
  asynqbroker/trace_envelope.go           新增    envelope 结构体 + 4个函数
  asynqbroker/broker.go                   修改    Publish 中 1 行：自动 wrap trace
  asynqbroker/consumer.go                 修改    Handle + HandleRaw 各加 2 行
  pkg/trace/propagator.go（可选）         修改    新增 MetadataFromContext/TraceFromMetadata

  vpc_workflow_demo/ 下所有文件：不得修改

## 验证要点

1. 向后兼容：旧格式 payload（无 _v 字段）必须被 UnwrapEnvelope 原样返回，
   不 panic，不干扰正常消费。

2. 降级安全：wrapWithTrace 在序列化失败时返回原始 payload，
   消息投递不受影响。

3. logger 同步（必须验证）：injectTraceFromMetadata 必须同时调用
   trace.ContextWithTrace + logger.ContextWithTraceID + logger.ContextWithSpanID，
   三个调用缺一不可。

4. Span 语义：消费方生成新 SpanId，发布方 SpanId 存为 ParentSpanId，
   TraceID 原样继承。

5. Sampled 一致性：wrapWithTrace（写入）和 injectTraceFromMetadata（读取）
   对 Sampled 的处理逻辑必须对称：写端存 "0" 表示不采样，读端 != "0" 为 true，
   默认（字段缺失）视为采样。

6. 双向完整链路（无需改动业务代码即可验证）：
   - 正向：az-nsp broker.Publish(ctx 含 trace)
           → asynq envelope
           → worker Handle → handler(ctx 含 trace) → logger 日志含 trace_id
   - 回调：worker cbSender.Success(ctx 含 trace)
           → broker.Publish(ctx 含 trace) → envelope
           → az-nsp HandleRaw → ctx 含 trace → HandleTaskCallback 日志含 trace_id

7. HandleRaw 透明性：az-nsp/main.go 的回调消费函数不需要调用
   UnwrapEnvelope 或手动构建 TraceContext，收到的 ctx 和 payload 均已处理完毕。
```
