# AsynqBroker 优化方案：移除 Payload 中的 task_id/resource_id 封装

## 一、需求澄清

### 1.1 当前问题

`engine.go:enqueueStep()` 强制将框架元数据塞入 payload：

```go
// engine.go:314-318
payload := map[string]interface{}{
    "task_id":     step.ID,        // 框架内部概念，不应污染业务 payload
    "resource_id": wf.ResourceID,  // 业务方已经知道，不需要框架塞
    "task_params": step.Params,    // 业务参数被包了一层
}
```

导致 Consumer 收到的是这样的结构：
```json
{
    "task_id": "step-uuid-xxx",
    "resource_id": "vpc-001",
    "task_params": "{\"device_id\":\"xxx\"}"  // 业务数据被二次转义
}
```

### 1.2 期望目标

- **`Task.Payload`** = 业务方定义的 `StepDefinition.Params`，原样传递
- **`task_id`** 通过其他机制传递（不在 payload 中）
- **`resource_id`** 框架不再自动添加，如果 handler 需要，业务方自己放在 Params 里

### 1.3 核心约束

`task_id` 对 callback 流程**必需**：
- Worker 执行完成后调用 `CallbackSender.Success(ctx, taskID, result)`
- Orchestrator 的 `HandleCallback` 通过 `taskID` 找到对应的 `StepTask`
- 因此框架必须将 `task_id` 传递给 Worker，但不应通过污染 payload 的方式

---

## 二、方案设计

### 2.1 核心思路

| 数据 | 当前 | 改动后 |
|------|------|--------|
| `task_id` | payload 内部 | **Envelope 层** / **Metadata** |
| `resource_id` | payload 内部 | **移除**，业务方按需自己放 Params |
| 业务参数 | `task_params` 字段 | **直接作为 payload** |

### 2.2 数据流对比

**改动前**：
```
StepDefinition.Params = '{"device_id":"xxx"}'
                ↓
        engine.enqueueStep()
                ↓
Task.Payload = '{"task_id":"...", "resource_id":"...", "task_params":"{\"device_id\":\"xxx\"}"}'
                ↓
        wrapWithTrace()
                ↓
Envelope = {"_v":1, "_tid":"...", "payload": <上面那坨>}
                ↓
        Consumer.Handle()
                ↓
TaskPayload.Params = []byte('{"device_id":"xxx"}')  // 需要从 task_params 字段提取
```

**改动后**：
```
StepDefinition.Params = '{"device_id":"xxx"}'
                ↓
        engine.enqueueStep()
                ↓
Task.Payload = '{"device_id":"xxx"}'  // 原样传递，不包装
Task.Metadata = {"task_id": "step-uuid"}
                ↓
        wrapWithMetadata()
                ↓
Envelope = {"_v":2, "_tid":"...", "_task_id":"step-uuid", "payload": '{"device_id":"xxx"}'}
                ↓
        Consumer.Handle()
                ↓
TaskPayload.TaskID = "step-uuid"  // 从 envelope 提取
TaskPayload.Params = []byte('{"device_id":"xxx"}')  // 直接就是业务数据
```

### 2.3 关键设计决策

#### 决策1：task_id 传递方式

**选项对比**：

| 方案 | 实现方式 | 优点 | 缺点 |
|------|---------|------|------|
| A. asynq TaskID | `asynq.TaskID(step.ID)` | 原生支持，Inspector 可查 | 与 asynq 自动生成的 ID 冲突 |
| B. Envelope 字段 | `_task_id` 放 envelope 层 | 统一封装，RocketMQ 也能用 | 需要改 envelope 结构 |
| C. Metadata | `Task.Metadata["task_id"]` | 语义清晰，扩展性好 | asynq 无原生 metadata 透传 |

**推荐：方案 B（Envelope 字段）**

理由：
- 统一处理，asynq 和 rocketmq 都能支持
- 不依赖 broker 特性，框架可控
- 已有 trace envelope 机制，扩展即可

#### 决策2：resource_id 处理

**移除自动添加**。理由：
- 业务方在定义 `WorkflowDefinition` 时已经知道 `ResourceID`
- 如果 handler 需要，业务方在 `StepDefinition.Params` 中自己包含
- 减少框架的隐式行为

#### 决策3：TaskPayload 结构变化

```go
// 当前
type TaskPayload struct {
    TaskID     string  // 框架提供
    TaskType   string  // 框架提供
    ResourceID string  // 框架提供 ← 移除
    Params     []byte  // 业务参数
}

// 改动后
type TaskPayload struct {
    TaskID   string  // 框架提供（从 envelope 提取）
    TaskType string  // 框架提供（从 asynq.Task.Type()）
    Params   []byte  // 业务参数（原样传递）
}
```

---

## 三、接口变更分析

### 3.1 Breaking Change：TaskPayload.ResourceID 移除

**影响范围**：所有使用 `payload.ResourceID` 的 handler 代码

**迁移方案**：业务方在 `StepDefinition.Params` 中自行包含 resource_id

```go
// 迁移前
func myHandler(ctx context.Context, payload *taskqueue.TaskPayload) (*taskqueue.TaskResult, error) {
    resourceID := payload.ResourceID  // 框架提供
    // ...
}

// 迁移后
type MyParams struct {
    ResourceID string `json:"resource_id"`  // 业务方自己定义
    DeviceID   string `json:"device_id"`
}

func myHandler(ctx context.Context, payload *taskqueue.TaskPayload) (*taskqueue.TaskResult, error) {
    var params MyParams
    json.Unmarshal(payload.Params, &params)
    resourceID := params.ResourceID  // 从业务参数中取
    // ...
}
```

### 3.2 不变的接口

| 接口 | 说明 |
|------|------|
| `Broker.Publish(ctx, *Task)` | 签名不变，Payload 语义变化（纯业务数据） |
| `Consumer.Handle(taskType, HandlerFunc)` | 签名不变 |
| `HandlerFunc` | 签名不变 |
| `Engine.SubmitWorkflow()` | 完全不变 |
| `WorkflowDefinition` / `StepDefinition` | 完全不变 |
| `TaskPayload.TaskID` | 保留，框架从 envelope 提取 |
| `TaskPayload.Params` | 保留，语义更清晰（直接是业务数据） |

### 3.3 变更的内部行为

| 组件 | 变更 |
|------|------|
| `engine.enqueueStep()` | 不再构造混合 payload，直接使用 `step.Params` |
| `taskEnvelope` | 新增 `_task_id` 字段 |
| `asynqbroker.Consumer.Handle()` | 从 envelope 提取 task_id，不再解析内层 JSON |
| `rocketmqbroker.Consumer` | 同步改动 |

---

## 四、详细实现改动

### 4.1 文件变更列表

| 文件 | 变更类型 | 说明 |
|------|---------|------|
| `taskqueue/task.go` | **Breaking** | 移除 `TaskPayload.ResourceID` 字段 |
| `taskqueue/engine.go` | 修改 | `enqueueStep()` 简化，直接使用 Params |
| `asynqbroker/trace_envelope.go` | 重构 | 升级为 v2，支持 `_task_id` |
| `asynqbroker/broker.go` | 修改 | 传递 task_id 到 Metadata |
| `asynqbroker/consumer.go` | 修改 | 简化解析逻辑 |
| `rocketmqbroker/consumer.go` | 修改 | 同步简化 |
| `*_test.go` | 修改 | 适配新结构 |

### 4.2 task.go — 移除 ResourceID

```go
// ---- 改动前 ----
type TaskPayload struct {
    TaskID     string
    TaskType   string
    ResourceID string  // ← 移除
    Params     []byte
}

// ---- 改动后 ----
type TaskPayload struct {
    TaskID   string // 从 envelope 提取，用于 callback
    TaskType string // 从 broker task type 获取
    Params   []byte // 纯业务参数，原样传递
}
```

新增 Metadata 常量：

```go
const (
    // MetadataKeyTaskID is the key for task ID in metadata/envelope
    MetadataKeyTaskID = "task_id"
)
```

### 4.3 engine.go — enqueueStep() 简化

```go
// ---- 改动前 (engine.go:304-332) ----
func (e *Engine) enqueueStep(ctx context.Context, step *StepTask) error {
    wf, err := e.store.GetWorkflow(ctx, step.WorkflowID)
    if err != nil {
        return fmt.Errorf("failed to get workflow for resource_id: %w", err)
    }
    if wf == nil {
        return fmt.Errorf("workflow not found: %s", step.WorkflowID)
    }

    payload := map[string]interface{}{
        "task_id":     step.ID,
        "resource_id": wf.ResourceID,
        "task_params": step.Params,
    }
    payloadData, err := json.Marshal(payload)
    if err != nil {
        return fmt.Errorf("failed to marshal step payload: %w", err)
    }

    queueName := e.queueRouter(step.QueueTag, step.Priority)

    task := &Task{
        Type:     step.TaskType,
        Payload:  payloadData,
        Queue:    queueName,
        Priority: step.Priority,
    }
    // ...
}

// ---- 改动后 ----
func (e *Engine) enqueueStep(ctx context.Context, step *StepTask) error {
    queueName := e.queueRouter(step.QueueTag, step.Priority)

    task := &Task{
        Type:     step.TaskType,
        Payload:  []byte(step.Params), // 直接使用业务参数，不再包装
        Queue:    queueName,
        Priority: step.Priority,
        Metadata: map[string]string{
            MetadataKeyTaskID: step.ID, // task_id 通过 metadata 传递
        },
    }

    info, err := e.broker.Publish(ctx, task)
    // ...（后续逻辑不变）
}
```

**注意**：移除了 `GetWorkflow` 调用，因为不再需要 `wf.ResourceID`。

### 4.4 asynqbroker/trace_envelope.go — 支持 task_id

```go
// taskEnvelope v2: 新增 _task_id，移除对 resource_id 的支持
type taskEnvelope struct {
    Version int             `json:"_v"`
    TraceID string          `json:"_tid,omitempty"`
    SpanID  string          `json:"_sid,omitempty"`
    Sampled bool            `json:"_smpl"`
    TaskID  string          `json:"_task_id,omitempty"` // v2 新增
    Payload json.RawMessage `json:"payload"`
}

// wrapWithMetadata creates a v2 envelope with trace info and task_id.
func wrapWithMetadata(ctx context.Context, payload []byte, metadata map[string]string) []byte {
    env := taskEnvelope{
        Version: 2,
        Payload: payload,
    }

    if metadata != nil {
        env.TaskID = metadata[taskqueue.MetadataKeyTaskID]
    }

    if tc, ok := trace.TraceFromContext(ctx); ok && tc != nil {
        env.TraceID = tc.TraceID
        env.SpanID = tc.SpanId
        env.Sampled = tc.Sampled
    }

    data, err := json.Marshal(env)
    if err != nil {
        return payload
    }
    return data
}

// unwrapEnvelope extracts business payload and task_id from envelope.
// Supports v1 (legacy), v2 (new) formats.
func unwrapEnvelope(data []byte) (payload []byte, taskID string, traceMeta map[string]string) {
    var env taskEnvelope
    if err := json.Unmarshal(data, &env); err != nil {
        return unwrapLegacyPayload(data)
    }

    traceMeta = buildTraceMeta(&env)

    switch env.Version {
    case 2:
        // v2: task_id in envelope, payload is pure business data
        return env.Payload, env.TaskID, traceMeta
    case 1:
        // v1: task_id inside payload (legacy format)
        return unwrapV1Payload(env.Payload, traceMeta)
    default:
        return unwrapLegacyPayload(data)
    }
}

// unwrapV1Payload handles v1 format where task_id/resource_id are in inner payload
func unwrapV1Payload(innerPayload []byte, traceMeta map[string]string) ([]byte, string, map[string]string) {
    var raw struct {
        TaskID     string `json:"task_id"`
        ResourceID string `json:"resource_id"`
        TaskParams string `json:"task_params"`
    }
    if err := json.Unmarshal(innerPayload, &raw); err != nil {
        return innerPayload, "", traceMeta
    }
    return []byte(raw.TaskParams), raw.TaskID, traceMeta
}

// unwrapLegacyPayload handles pre-envelope format
func unwrapLegacyPayload(data []byte) ([]byte, string, map[string]string) {
    var raw struct {
        TaskID     string `json:"task_id"`
        TaskParams string `json:"task_params"`
    }
    if err := json.Unmarshal(data, &raw); err != nil {
        return data, "", nil
    }
    if raw.TaskID == "" {
        return data, "", nil
    }
    return []byte(raw.TaskParams), raw.TaskID, nil
}
```

### 4.5 asynqbroker/broker.go — 使用新 wrap 函数

```go
func (b *Broker) Publish(ctx context.Context, task *taskqueue.Task) (*taskqueue.TaskInfo, error) {
    wrappedPayload := wrapWithMetadata(ctx, task.Payload, task.Metadata)
    asynqTask := asynq.NewTask(task.Type, wrappedPayload)
    // ...（其余不变）
}
```

### 4.6 asynqbroker/consumer.go — 简化解析

```go
// ---- 改动前 ----
func (c *Consumer) Handle(taskType string, handler taskqueue.HandlerFunc) {
    c.mux.HandleFunc(taskType, func(ctx context.Context, t *asynq.Task) error {
        rawBytes, metadata := unwrapEnvelope(t.Payload())
        ctx = injectTraceFromMetadata(ctx, metadata)

        var raw struct {
            TaskID     string `json:"task_id"`
            ResourceID string `json:"resource_id"`
            TaskParams string `json:"task_params"`
        }
        if err := json.Unmarshal(rawBytes, &raw); err != nil {
            return fmt.Errorf("failed to unmarshal task payload: %w", err)
        }

        payload := &taskqueue.TaskPayload{
            TaskID:     raw.TaskID,
            TaskType:   t.Type(),
            ResourceID: raw.ResourceID,
            Params:     []byte(raw.TaskParams),
        }

        result, err := handler(ctx, payload)
        // ...
    })
}

// ---- 改动后 ----
func (c *Consumer) Handle(taskType string, handler taskqueue.HandlerFunc) {
    c.mux.HandleFunc(taskType, func(ctx context.Context, t *asynq.Task) error {
        rawPayload, taskID, traceMeta := unwrapEnvelope(t.Payload())
        ctx = injectTraceFromMetadata(ctx, traceMeta)

        payload := &taskqueue.TaskPayload{
            TaskID:   taskID,
            TaskType: t.Type(),
            Params:   rawPayload, // 直接就是业务数据，不需要再解析
        }

        result, err := handler(ctx, payload)
        if err != nil {
            log.Printf("[asynqbroker] handler error: type=%s, task_id=%s, err=%v",
                taskType, taskID, err)
            return err
        }
        _ = result
        return nil
    })
}
```

### 4.7 rocketmqbroker/consumer.go — 同步简化

```go
// ---- 改动后 ----
func (c *Consumer) handleMessages(ctx context.Context, msgs ...*primitive.MessageExt) (consumer.ConsumeResult, error) {
    for _, msg := range msgs {
        taskType := msg.GetTags()

        c.mu.RLock()
        handler, exists := c.handlers[taskType]
        c.mu.RUnlock()

        if !exists {
            log.Printf("[rocketmqbroker] no handler for task type: %s", taskType)
            continue
        }

        // 尝试从 Properties 获取 task_id（新格式）
        taskID := msg.GetProperty(taskqueue.MetadataKeyTaskID)
        bodyPayload := msg.Body

        if taskID == "" {
            // 回退到旧格式：从 body 解析
            var raw struct {
                TaskID     string `json:"task_id"`
                TaskParams string `json:"task_params"`
            }
            if err := json.Unmarshal(msg.Body, &raw); err != nil {
                log.Printf("[rocketmqbroker] failed to unmarshal payload: %v", err)
                return consumer.ConsumeRetryLater, err
            }
            taskID = raw.TaskID
            bodyPayload = []byte(raw.TaskParams)
        }

        payload := &taskqueue.TaskPayload{
            TaskID:   taskID,
            TaskType: taskType,
            Params:   bodyPayload,
        }

        result, err := handler(ctx, payload)
        if err != nil {
            log.Printf("[rocketmqbroker] handler error: type=%s, task_id=%s, err=%v",
                taskType, taskID, err)
            return consumer.ConsumeRetryLater, err
        }
        _ = result
    }
    return consumer.ConsumeSuccess, nil
}
```

---

## 五、对 Workflow 的影响

### 5.1 无影响的组件

| 组件 | 说明 |
|------|------|
| `Engine.SubmitWorkflow()` | 内部调用 enqueueStep()，改动被封装 |
| `Engine.HandleCallback()` | 依赖 `taskID` 找 step，taskID 传递方式变了但值不变 |
| `Engine.RetryStep()` | 内部调用 enqueueStep() |
| `CallbackSender` | 不涉及，callback 消息格式独立 |
| `WorkflowHooks` | 不涉及 payload 解析 |
| `StepDefinition.Params` | 语义不变，只是不再被二次包装 |

### 5.2 enqueueStep() 简化带来的好处

改动前需要查询 workflow 获取 resource_id：
```go
wf, err := e.store.GetWorkflow(ctx, step.WorkflowID)  // 一次 DB 查询
// ...
"resource_id": wf.ResourceID,
```

改动后直接使用 step.Params：
```go
Payload: []byte(step.Params),  // 无需 DB 查询
```

**性能提升**：每次 enqueue 减少一次数据库查询。

---

## 六、向后兼容性

### 6.1 Breaking Change 总结

| 变更 | 影响 | 迁移方案 |
|------|------|---------|
| `TaskPayload.ResourceID` 移除 | 使用此字段的 handler | 业务方在 Params 中自行包含 |
| Envelope 格式升级 v1→v2 | Consumer 需同时支持 | 新 Consumer 兼容 v1 |

### 6.2 升级路径

```
阶段 1: 业务方迁移 handler 代码
        - 如果需要 resource_id，改为从 Params 中获取
        - 发布新版业务代码

阶段 2: 部署新 Consumer（支持 v1 + v2）
        - 先升级所有 Worker

阶段 3: 部署新 Engine（发送 v2）
        - 再升级 Orchestrator

阶段 4: (可选) 清理 v1 兼容代码
```

### 6.3 兼容性矩阵

| Producer | Consumer | Handler 代码 | 结果 |
|----------|----------|-------------|------|
| 旧 v1 | 旧 | 使用 ResourceID | ✅ 正常 |
| 旧 v1 | 新 | 使用 ResourceID | ❌ ResourceID 为空 |
| 旧 v1 | 新 | 已迁移 | ✅ 正常 |
| 新 v2 | 新 | 已迁移 | ✅ 正常 |

**关键**：Handler 代码需要先迁移，然后再升级 Consumer/Engine。

---

## 七、对用户的影响

### 7.1 必须修改的代码

任何使用 `payload.ResourceID` 的 handler：

```go
// ---- 需要修改 ----
func myHandler(ctx context.Context, payload *taskqueue.TaskPayload) (*taskqueue.TaskResult, error) {
    resourceID := payload.ResourceID  // ❌ 编译错误：字段已移除
}

// ---- 迁移后 ----
type MyParams struct {
    ResourceID string `json:"resource_id"`
    // 其他业务字段
}

func myHandler(ctx context.Context, payload *taskqueue.TaskPayload) (*taskqueue.TaskResult, error) {
    var params MyParams
    if err := json.Unmarshal(payload.Params, &params); err != nil {
        return nil, err
    }
    resourceID := params.ResourceID  // ✅ 从业务参数获取
}
```

同时，定义 step 时需要包含 resource_id：

```go
// ---- 迁移后的 Step 定义 ----
steps := []taskqueue.StepDefinition{
    {
        TaskType: "create_vrf",
        TaskName: "Create VRF",
        Params:   `{"resource_id": "vpc-001", "device_id": "switch-01"}`,  // 自行包含
    },
}
```

### 7.2 无需修改的代码

- `payload.TaskID` — 继续可用
- `payload.TaskType` — 继续可用
- `payload.Params` — 继续可用（内容更干净）
- 所有 Engine/Workflow 调用方式 — 不变

### 7.3 好处

1. **Payload 更干净** — 不再有 `task_params` 嵌套和字符串转义
2. **性能提升** — enqueueStep 减少一次 DB 查询
3. **职责清晰** — 框架只传递必要的 task_id，业务数据完全由业务方控制
4. **灵活性** — 业务方可以自由定义 Params 结构

---

## 八、测试计划

### 8.1 单元测试

| 测试用例 | 说明 |
|---------|------|
| `TestWrapWithMetadata_V2` | 验证 v2 envelope 包含 `_task_id`，payload 原样 |
| `TestUnwrapEnvelope_V2` | 验证 v2 正确提取 taskID 和纯业务 payload |
| `TestUnwrapEnvelope_V1Compat` | 验证 v1 仍能正确提取 |
| `TestUnwrapEnvelope_LegacyCompat` | 验证无 envelope 格式仍能正确提取 |

### 8.2 集成测试

| 测试用例 | 说明 |
|---------|------|
| `TestEnqueueStep_PurePayload` | 验证 Task.Payload 是原始 Params |
| `TestEnqueueStep_NoDBQuery` | 验证不再查询 workflow |
| `TestFullWorkflow_V2Format` | 完整流程测试 |
| `TestCallbackFlow_TaskIDFromEnvelope` | 验证 callback 能正确关联 step |

### 8.3 迁移测试

| 测试场景 | 说明 |
|---------|------|
| 新 Consumer 处理 v1 消息 | 兼容性 |
| Handler 从 Params 获取 resource_id | 迁移验证 |
