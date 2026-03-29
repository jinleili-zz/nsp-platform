# TaskQueue 重构设计方案

## 1. 背景与目标

当前 `taskqueue` 模块把两类职责混在了一起：

1. 通用消息队列职责
   - 发布消息
   - 消费消息
   - 透传元数据
   - 跨 broker 适配

2. Workflow 编排协议职责
   - `task_id`
   - `resource_id`
   - `task_params`
   - 固定结果返回约定

这种混合设计导致两个直接问题：

- `Broker` 层看起来抽象了，但 `Consumer.Handle(...)` 仍然强依赖 `TaskPayload`
- 固定结果返回约定侵入了通用层，导致结果消息不能由调用方自由定义

本次重构目标是把 `taskqueue` 明确拆成两层：

- 通用消息层：broker/consumer 只处理“消息”本身
- workflow 协议层：负责步骤任务、回调结果、编排相关字段

同时满足两个新增要求：

- 支持 trace-id 透明透传
- producer 发送任务时，能把“结果回复约定”一起传给 consumer

## 2. 当前设计存在的问题

### 2.1 核心抽象边界不清

当前公共 handler 签名是：

```go
type HandlerFunc func(ctx context.Context, payload *TaskPayload) (*TaskResult, error)
```

这意味着所有 broker consumer 都必须知道 `TaskPayload` 的业务结构。

结果是：

- `asynqbroker.Consumer` 要手动反序列化 `task_id/resource_id/task_params`
- `rocketmqbroker.Consumer` 也要做同样的事情
- broker 层并不真正通用，只是“传输介质可替换”

### 2.2 workflow 协议侵入了通用层

当前 `TaskPayload` 中的这些字段：

- `TaskID`
- `ResourceID`
- `TaskType`

都不是通用 MQ 概念，而是 workflow 编排语义。

### 2.3 结果返回没有被设计成统一消息协议

当前 worker 结果回传依赖固定约定：

- 固定消息类型
- 固定回传队列
- 固定 helper

这本质上只是“再发送一条结果消息”。

问题不在于需要一个特殊回调机制，而在于当前实现把“结果返回给原 producer 进程”的方式写死了，调用方无法自由定义结果消息的类型、结构和处理方式。

### 2.4 trace 透传在 asynq 中依赖私有编码

当前 asynq 通过 envelope 包装 payload，把 trace 信息塞进 payload 外壳中。

这解决了 asynq 无 header 的问题，但文档层面需要明确：这里的 envelope 只是 broker 对“逻辑 metadata”的编码方式，不代表 metadata 必须是 broker 原生 header。

## 3. 重构原则

### 3.1 分层清晰

通用消息层只关心：

- 消息类型
- 消息体
- 元数据
- 路由
- 回复约定

workflow 层才关心：

- 步骤任务结构
- 编排推进

### 3.2 broker 层不理解 workflow 字段

broker 只传递 opaque payload + metadata，不反序列化 `task_id/resource_id/...`。

### 3.3 trace 与 reply 都应该成为消息层的一等能力

原因：

- trace 是跨消息传输边界的基础设施能力，不应藏在某个业务 payload 里
- reply-to 是消息投递语义的一部分，不应放进用户业务 payload
- 回传结果本质上只是“按 reply 约定再发送一条普通消息”
- 这两者都不应该做成 asynq 私有功能，否则无法跨 broker 统一

### 3.4 workflow 层通过 codec/adapter 构建在消息层之上

workflow 层只依赖通用消息层接口，通过 codec 做编码/解码。

## 4. 重构后的分层模型

### 4.1 第一层：通用消息层

建议新增如下核心类型：

```go
type Message struct {
    Type     string
    Body     []byte
    Queue    string
    Priority Priority
    Metadata map[string]string
    Reply    *ReplySpec
}

type ReplySpec struct {
    Queue         string
    Type          string
    CorrelationID string
}

type Delivery struct {
    BrokerMessageID string
    Type            string
    Body            []byte
    Queue           string
    Metadata        map[string]string
    Reply           *ReplySpec
}

type MessageResult struct {
    Metadata map[string]string
}

type MessageHandler func(ctx context.Context, msg *Delivery) (*MessageResult, error)
```

说明：

- `Message` 是 producer 发出的通用消息
- `Delivery` 是 consumer 收到的通用消息
- `ReplySpec` 是“如何回复”的通用约定
- `Metadata` 承载 trace 和其他非业务元信息
- `Metadata` 是逻辑消息字段，不要求底层 broker 必须原生支持 header/properties

### 4.2 第二层：workflow 协议层

workflow 层建议定义专属协议结构：

```go
type WorkflowTaskPayload struct {
    TaskID     string          `json:"task_id"`
    ResourceID string          `json:"resource_id"`
    Params     json.RawMessage `json:"task_params"`
}

```

并提供 codec：

```go
func EncodeWorkflowTask(step *StepTask, wf *Workflow) (*Message, error)
func DecodeWorkflowTask(msg *Delivery) (*WorkflowTaskPayload, error)
```

这样 workflow 层完全不需要 asynq/RocketMQ 细节。

## 5. 接口重构建议

## 5.1 Broker 接口

建议调整为：

```go
type Broker interface {
    Publish(ctx context.Context, msg *Message) (*PublishResult, error)
    Close() error
}
```

说明：

- 新接口直接以 `Message` 为核心输入
- 不再围绕旧的 `Task` 抽象做兼容设计

## 5.2 Consumer 接口

建议改为：

```go
type Consumer interface {
    HandleMessage(messageType string, handler MessageHandler)
    Start(ctx context.Context) error
    Stop() error
}
```

不要把 `HandleRaw` 放进这个接口。

原因：

- `HandleRaw` 暴露的是实现细节
- 一旦进入公共接口，抽象就被 vendor 类型污染
- 通用消息层存在后，`HandleRaw` 的必要性会显著下降

## 5.3 workflow 层使用方式

workflow 不再保留旧的 `Handle(taskType, handler)` 风格。

新的使用方式是：

1. 使用 `HandleMessage(...)` 注册通用 handler
2. 在 handler 内部对 `Delivery.Body` 做 workflow 解码
3. 使用 `Delivery.Reply / Delivery.Metadata` 处理结果返回和 trace

也就是说：

- `HandleMessage(...)` 是唯一标准消费入口
- workflow 只提供 codec 和可选 helper
- 不再提供旧 handler 签名的兼容层

## 6. trace-id 透传设计

## 6.1 设计原则

trace 信息不进入业务 payload，统一放入 `Metadata`。

这里的 `Metadata` 指的是通用消息层的逻辑字段，不等于“底层 broker 一定原生支持 metadata/header”。

不同 broker 的映射方式可以不同：

- asynq：通过 envelope 编码进 payload 外层
- RocketMQ：通过 message properties 映射
- Kafka / RabbitMQ：通过各自的 header/properties 映射

上层只依赖 `Message.Metadata / Delivery.Metadata` 这个统一抽象。

建议复用现有 trace 包已经定义好的 metadata 形式：

- `trace_id`
- `span_id`
- `sampled`

## 6.2 发送端约定

发送消息时：

```go
msg.Metadata = MergeMetadata(msg.Metadata, trace.MetadataFromContext(ctx))
```

规则：

- 如果调用方已显式设置 trace metadata，优先保留调用方值
- 否则从 `ctx` 自动注入

## 6.3 消费端约定

消费消息时：

- 从消息 metadata 恢复 `TraceContext`
- 写入 `context.Context`
- 同步写入 logger context

这样所有业务 handler 都基于统一的 `Delivery` 模型工作，并可直接从 `ctx` 取到 trace。

## 6.4 broker 映射方式

### asynq

asynq 没有标准 header，所以建议继续使用 envelope，但升级为通用消息 envelope：

```go
type envelope struct {
    Version  int               `json:"_v"`
    Metadata map[string]string `json:"_meta,omitempty"`
    Reply    *ReplySpec        `json:"_reply,omitempty"`
    Payload  json.RawMessage   `json:"payload"`
}
```

优点：

- trace、reply、其他 metadata 全部统一进 envelope
- asynq 不需要理解 workflow payload
- broker 只处理通用消息 envelope

说明：

- `Metadata` 仍然存在于通用消息模型中
- 只是 asynq 对这份逻辑 metadata 的物理落地方式是 envelope
- 因此“trace 统一走 Metadata”和“asynq 不支持原生 metadata”并不冲突

### RocketMQ

RocketMQ 原生支持 message properties，建议：

- `Body` 直接放 payload
- `Metadata` 写入 properties
- `ReplySpec` 映射为保留 properties

保留字段建议：

- `nsp.reply.queue`
- `nsp.reply.type`
- `nsp.reply.correlation_id`

trace 字段直接写 properties：

- `trace_id`
- `span_id`
- `sampled`

## 7. reply-to 与结果消息设计建议

## 7.1 不建议放到用户 payload 里

不推荐把 reply 信息塞进业务 payload，例如：

```json
{
  "order_id": "xxx",
  "callback_queue": "task_callbacks"
}
```

原因：

- 业务 payload 应只承载业务参数
- reply-to 是消息投递协议，不是业务字段
- 一旦混进去，所有 handler 都得理解这些“平台字段”
- 不利于跨业务复用

## 7.2 也不建议做成 asynq 私有能力

不推荐只在 asynq broker 里偷偷支持 reply-to。

原因：

- 这样会重新回到 broker 私有语义
- RocketMQ/Kafka 等实现无法共享相同编程模型
- 上层业务无法稳定依赖

## 7.3 推荐方案：reply-to 作为通用消息层一等字段

建议放在 `Message.Reply` / `Delivery.Reply` 中。

这是本次设计最关键的结论之一。

这里需要明确：

- reply 不是特殊回调流程
- reply 只是“消费者处理完成后，按照约定再发一条普通消息”
- 框架不再预置固定 callback 协议
- 结果消息的类型、结构、处理方式由调用方定义

推荐结构：

```go
type ReplySpec struct {
    Queue         string
    Type          string
    CorrelationID string
}
```

字段含义：

- `Queue`：回复应该发到哪个队列/主题
- `Type`：回复消息类型，由调用方自行定义
- `CorrelationID`：用于把回复和原始请求关联起来，workflow 场景可直接填 `step.ID`

## 7.4 结果消息如何使用

producer 发任务时可以附带：

```go
msg.Reply = &ReplySpec{
    Queue:         "result_queue",
    Type:          "task_result",
    CorrelationID: "step-123",
}
```

worker 侧收到任务后：

- 从 `Delivery.Reply` 中拿到回复约定
- 自己决定是否返回结果
- 自己决定返回什么结构的结果消息
- 使用通用 `ReplySender` 或直接重新 `Publish` 一条普通消息

从消息层视角看，这里没有“特殊回调流程”：

1. producer 发出任务消息，并附带 `ReplySpec`
2. consumer 执行完后，按 `ReplySpec` 再发出一条普通结果消息
3. 原 producer 进程像消费普通消息一样消费这条结果消息

## 7.5 workflow 场景建议

如果 workflow engine 需要根据结果推进状态机，可以由 workflow 调用方自行约定：

- 结果消息类型，例如 `workflow_step_result`
- 结果消息结构
- 结果消息消费者

也就是说：

- 通用层不再提供固定 `task_callback`
- workflow 层也不再预置固定 callback 协议
- 由具体调用方决定如何组织结果返回链路
- workflow engine 只保留状态推进能力，不内建固定结果消息协议

## 8. workflow 层重构方案

## 8.1 发送任务

当前 `Engine.enqueueStep(...)` 直接构造 JSON payload。

建议重构为：

1. workflow codec 生成 `WorkflowTaskPayload`
2. workflow codec 序列化为 `Message.Body`
3. 设置 `Message.Type / Queue / Priority / Reply`
4. 由 broker 发布

示意：

```go
payload := WorkflowTaskPayload{
    TaskID:     step.ID,
    ResourceID: wf.ResourceID,
    Params:     json.RawMessage(step.Params),
}

body, _ := json.Marshal(payload)
msg := &Message{
    Type:     step.TaskType,
    Body:     body,
    Queue:    queueName,
    Priority: step.Priority,
    Reply: &ReplySpec{
        Queue:         "workflow_result_queue",
        Type:          "workflow_step_result",
        CorrelationID: step.ID,
    },
}
```

## 8.2 worker 处理任务

worker 不再依赖 broker 把原始消息硬解成 `TaskPayload`。

建议流程：

1. broker consumer 交付 `Delivery`
2. workflow adapter 解码 `Delivery.Body`
3. handler 直接使用 `Delivery`
4. 需要 workflow 语义时，显式调用 `DecodeWorkflowTask(delivery)`
5. 需要返回结果时，直接使用 `Delivery.Reply`

推荐 handler 形态：

```go
func(ctx context.Context, delivery *Delivery) (*MessageResult, error)
```

如果业务需要 workflow payload，则在 handler 内部显式解码：

```go
payload, err := taskqueue.DecodeWorkflowTask(delivery)
```

这样好处是：

- 所有消息消费统一到一个入口
- `ReplySpec`、`Metadata`、`BrokerMessageID` 都是显式可见的
- 不需要把 `ReplySpec` 再额外塞进 `context.Context`
- 不再为旧 handler 签名支付兼容成本

## 8.3 结果消息发送器

建议新增通用回复发送器：

```go
type ReplySender struct {
    broker Broker
}

func (s *ReplySender) Reply(ctx context.Context, reply *ReplySpec, body []byte, metadata map[string]string) error
```

设计意图：

- 通用层只提供发送结果消息的能力
- 不再提供固定 callback helper
- 调用方自己决定是否定义上层 helper，以及结果消息体结构

## 8.4 workflow engine 的结果推进职责

workflow engine 的职责边界明确为：

- 不内建固定结果消息协议
- 不内建固定结果消息类型
- 不内建固定结果消息体结构
- 但保留 workflow 状态推进能力

推荐调用模式：

1. 调用方使用 `HandleMessage(...)` 注册自己的结果消息 handler
2. 调用方自行解析结果消息体
3. 调用方将标准化后的结果显式传给 engine API
4. engine 负责更新 step/workflow 状态并决定是否推进下一步

也就是说：

- 消息协议由调用方掌控
- 状态机推进由 engine 掌控

建议 engine 至少暴露一类明确的推进接口，例如：

```go
func (e *Engine) ApplyStepResult(ctx context.Context, result *StepResult) error
```

其中 `StepResult` 是 engine 内部面向状态机的标准输入，而不是消息协议本身。

这样可以保证：

- engine 仍然有独立价值
- 不把状态机逻辑重新散落到业务方
- 同时避免 engine 再次绑死某种 callback/result message 协议

## 9. 实施策略

本次建议直接落地新设计，不继续为旧抽象做兼容。

原则：

- `HandleMessage(...)` 作为唯一标准消费入口
- `Message / Delivery / ReplySpec` 作为唯一核心消息模型
- workflow 通过 codec 使用通用消息层
- 不再保留旧的 `Handle(...)` / `TaskPayload` / `HandlerFunc` 设计

影响：

- 现有基于旧 handler 签名的业务代码需要同步调整
- 但可以避免继续背负错误抽象
- 实施完成后，整个消息层边界会更稳定、更清晰
- workflow 相关业务需要在自己的结果消息 handler 中显式调用 engine 推进 API

## 10. 实施顺序建议

建议按以下顺序实施：

1. 新增通用消息模型与 metadata/reply 规范
2. 改造 asynq broker：trace envelope 升级为通用 envelope
3. 改造 RocketMQ broker：properties 映射 metadata/reply
4. 新增 `HandleMessage(...)`
5. 让业务消费侧统一迁移到 `HandleMessage(...)`
6. 把 `Engine.enqueueStep(...)` 改为通过 workflow codec 发消息
7. 为 engine 提供显式状态推进 API，例如 `ApplyStepResult(...)`
8. 删除固定 callback/helper 设计，统一改为普通结果消息模式
9. 删除结果返回对 `HandleRaw` 的依赖
10. 补充测试：trace、reply、普通结果消息、新 API、engine 状态推进

## 11. 需要确认的设计决策

实施前建议先确认以下 4 点：

1. 通用消息层是否采用 `Message/Delivery/ReplySpec` 这套模型
2. reply-to 是否确定为消息层一等字段，而不是用户 payload 或 asynq 私有特性
3. workflow 场景是否接受统一改为 `HandleMessage(...) + DecodeWorkflowTask(...)`
4. 是否确认 workflow engine 不内建固定结果消息协议，但保留状态推进能力，由调用方显式调用 engine API

## 12. 推荐结论

本次重构我建议采用以下最终方向：

- `Broker` 只负责通用消息投递
- `Consumer` 只负责通用消息消费
- trace 统一走 `Metadata`
- reply-to 统一走 `ReplySpec`
- workflow 层通过 codec/adaptor 构建在通用消息层之上
- 不再保留固定 callback 设计，由调用方自行定义结果消息协议
- workflow engine 不内建固定结果消息协议，但保留状态推进能力
- 不再保留旧 handler 抽象，统一迁移到 `HandleMessage(...)`
- `HandleRaw` 如需存在，也只作为 broker 私有逃生口，不进入公共设计

这是一个比较干净、也最利于后续扩展 Kafka/RabbitMQ/NATS 的方向。

## 12.1 Inspector 定位

`Inspector` 不属于通用消息收发抽象的一部分，它属于 broker 的可选运维能力。

建议定位如下：

- `Broker` / `Consumer`：负责消息发布与消费
- `Inspector`：负责队列观测、任务查询、运维控制

也就是说：

- 不把 `Inspector` 并入 `Broker` 或 `Consumer` 核心接口
- 继续保留 `Inspector` 作为独立可选接口
- 具体 broker 按能力选择实现

建议接口关系保持为：

```go
type Broker interface {
    Publish(ctx context.Context, msg *Message) (*PublishResult, error)
    Close() error
}

type Consumer interface {
    HandleMessage(messageType string, handler MessageHandler)
    Start(ctx context.Context) error
    Stop() error
}

type Inspector interface {
    Queues(ctx context.Context) ([]string, error)
    GetQueueStats(ctx context.Context, queue string) (*QueueStats, error)
    ListWorkers(ctx context.Context) ([]*WorkerInfo, error)
    Close() error
}
```

设计原则：

- `Inspector` 只依赖 broker 自身的数据模型和运维能力
- `Inspector` 不理解 workflow payload
- `Inspector` 不依赖 `WorkflowTaskPayload`
- `Inspector` 不参与 `ReplySpec`、结果消息协议等上层设计

对于 `asynqbroker`：

- 继续完整实现 `Inspector`
- 因为 asynq 本身就具备较强的队列管理和任务观测能力

对于其他 broker：

- 可以按能力部分实现或不实现
- 不强迫所有 broker 提供同等运维能力

这样可以保证：

- 核心消息抽象保持干净
- 运维能力仍然保留
- 不把 broker-specific 管理能力错误地提升为通用消息契约

## 12.2 Broker 反向依赖 Workflow 的问题

当前设计里，一个核心问题是：

- `asynqbroker` 的 consumer 为了实现 `Handle(...)`
- 直接依赖了 `TaskPayload`
- 而 `TaskPayload` 又定义在 `task.go` 中
- `task.go` 同时还包含 workflow 相关模型

结果就是：

- 想使用 `asynqbroker` 的开发者
- 会被迫依赖 `task.go`
- 等价于 broker 层向上引用了 workflow 语义

这是本次重构必须消除的问题。

新设计下，原则应该是：

- `asynqbroker` 只依赖通用消息层
- 不依赖 workflow 协议层
- 不依赖 `task.go` 中的 workflow 类型

也就是说，新的依赖方向应该是：

```text
taskqueue core(message)  <-  asynqbroker / rocketmqbroker
taskqueue workflow       <-  engine / codec / workflow-specific helpers
```

而不应该是：

```text
asynqbroker -> task.go(含 workflow)
```

更具体地说：

- `asynqbroker` 只应 import 通用消息层中的
  - `Message`
  - `Delivery`
  - `ReplySpec`
  - `MessageHandler`
  - `PublishResult`
- `asynqbroker` 不应 import
  - `WorkflowTaskPayload`
  - `Workflow` / `StepTask`
  - `HandlerFunc`
  - `TaskPayload`

如果某个开发者只是想：

- 使用 asynq 作为 broker
- 发布/消费普通消息

那他不应该被迫理解或依赖 workflow 层。

结论：

- 新设计下，不应再出现“使用 asynq broker 还要依赖 task.go / workflow”的情况
- 如果实施后仍然存在这种依赖，说明分层没有拆干净

## 13. 最终接口草案

本节给出准备落地到代码中的接口草案，用于确认实施边界。

## 13.1 通用消息模型

```go
package taskqueue

type Message struct {
    Type     string
    Body     []byte
    Queue    string
    Priority Priority
    Metadata map[string]string
    Reply    *ReplySpec
}

type ReplySpec struct {
    Queue         string
    Type          string
    CorrelationID string
}

type Delivery struct {
    BrokerMessageID string
    Type            string
    Body            []byte
    Queue           string
    Metadata        map[string]string
    Reply           *ReplySpec
}

type PublishResult struct {
    BrokerTaskID string
    Queue        string
}

type MessageResult struct {
    Metadata map[string]string
}
```

说明：

- `TaskInfo` 后续建议收敛为 `PublishResult`
- `Delivery` 是统一的消费视图，屏蔽 broker 原始对象

## 13.2 通用 Broker / Consumer 接口

```go
package taskqueue

type Broker interface {
    Publish(ctx context.Context, msg *Message) (*PublishResult, error)
    Close() error
}

type MessageHandler func(ctx context.Context, msg *Delivery) (*MessageResult, error)

type Consumer interface {
    HandleMessage(messageType string, handler MessageHandler)
    Start(ctx context.Context) error
    Stop() error
}
```

设计意图：

- `Broker` 只负责发送通用消息
- `Consumer` 只负责把通用消息交给 handler
- 核心接口不暴露 `*asynq.Task`、`*primitive.MessageExt` 等 vendor 类型

## 13.3 可选 context helper 草案

通用消费入口已经直接把 `Delivery` 传给 handler，因此不再需要把 `ReplySpec` 为兼容目的塞进 `context.Context`。

如果某些框架内部链路需要，最多只保留完整 `Delivery` 的 context helper：

```go
package taskqueue

func ContextWithDelivery(ctx context.Context, msg *Delivery) context.Context
func DeliveryFromContext(ctx context.Context) (*Delivery, bool)
```

说明：

- 大多数业务 handler 直接使用 `delivery *Delivery` 即可
- `ContextWithDelivery` 仅用于少量内部链路透传

## 13.4 通用回复发送器草案

```go
package taskqueue

type ReplySender struct {
    broker Broker
}

func NewReplySender(broker Broker) *ReplySender

func (s *ReplySender) Reply(ctx context.Context, reply *ReplySpec, msgType string, body []byte, metadata map[string]string) error
```

约定：

- `reply.Queue` 决定结果消息发往哪里
- `msgType` 优先使用显式参数；若为空，则回退到 `reply.Type`
- 发送时自动合并 trace metadata

这样 `ReplySender` 可以服务于：

- workflow 结果消息
- 非 workflow 的 request/reply 模式
- 其他未来的通用应答场景

## 13.5 Workflow 协议模型草案

```go
package taskqueue

type WorkflowTaskPayload struct {
    TaskID     string          `json:"task_id"`
    ResourceID string          `json:"resource_id"`
    Params     json.RawMessage `json:"task_params"`
}
```

说明：

- workflow 层只预置步骤任务 payload
- 结果消息 payload 不再由框架固定定义
- 如果某个 workflow 调用方需要结果消息结构，应由该调用方自行定义

## 13.6 Workflow codec 草案

```go
package taskqueue

func EncodeWorkflowTask(step *StepTask, wf *Workflow, queue string, reply *ReplySpec) (*Message, error)
func DecodeWorkflowTask(msg *Delivery) (*WorkflowTaskPayload, error)
```

设计意图：

- workflow 编排逻辑不再直接手写 JSON
- 统一收口到 codec，便于后续演进字段
- broker 层完全不感知 workflow payload 结构

## 13.7 Workflow 使用草案

workflow 不再定义独立的 handler 签名。

推荐写法：

```go
c.HandleMessage("workflow_step", func(ctx context.Context, delivery *taskqueue.Delivery) (*taskqueue.MessageResult, error) {
    payload, err := taskqueue.DecodeWorkflowTask(delivery)
    if err != nil {
        return nil, err
    }

    _ = payload
    _ = delivery.Reply
    return nil, nil
})
```

设计意图：

- workflow 继续使用统一消费模型
- workflow payload 解码保持显式
- 不再维护第二套 handler API

## 13.8 结果消息处理约定草案

框架不再提供固定结果消息注册函数，也不固定结果消息协议。

调用方如果需要结果消息处理，应自行：

1. 约定结果消息类型
2. 定义结果消息体结构
3. 使用 `HandleMessage(...)` 注册对应 handler
4. 在 handler 中将标准化结果显式传给 engine API，或推进自己的业务逻辑

如果使用 workflow engine，推荐模式为：

```go
consumer.HandleMessage("workflow_step_result", func(ctx context.Context, delivery *taskqueue.Delivery) (*taskqueue.MessageResult, error) {
    result := &taskqueue.StepResult{
        // 调用方自行从 delivery.Body 解析并组装
    }
    if err := engine.ApplyStepResult(ctx, result); err != nil {
        return nil, err
    }
    return nil, nil
})
```

关键点：

- `StepResult` 是 engine 的状态推进输入
- 它不是固定消息协议
- 调用方仍然完全掌控消息类型和消息体

## 13.9 旧抽象处理原则

旧抽象不再作为设计目标的一部分。

原则上：

- 不新增基于旧模型的兼容 API
- 不为旧签名继续扩展能力
- 如确需过渡，可在实施时做最小范围桥接，但不写入正式设计

## 13.10 asynq / RocketMQ 实现落点草案

### asynqbroker

建议新增或改造成：

```go
func (b *Broker) Publish(ctx context.Context, msg *taskqueue.Message) (*taskqueue.PublishResult, error)
func (c *Consumer) HandleMessage(messageType string, handler taskqueue.MessageHandler)
```

说明：

- `HandleMessage` 是唯一标准消费入口
- `HandleRaw` 如保留，也只作为 asynq 私有高级接口

### rocketmqbroker

建议新增或改造成：

```go
func (b *Broker) Publish(ctx context.Context, msg *taskqueue.Message) (*taskqueue.PublishResult, error)
func (c *Consumer) HandleMessage(messageType string, handler taskqueue.MessageHandler)
```

说明：

- properties 承担 `Metadata + ReplySpec` 的落地

## 13.11 Engine 落点草案

Engine 侧建议改造成：

```go
func (e *Engine) enqueueStep(ctx context.Context, step *StepTask) error
func (e *Engine) NewReplySender() *ReplySender
func (e *Engine) ApplyStepResult(ctx context.Context, result *StepResult) error
```

其中：

- `enqueueStep` 内部通过 workflow codec 构造 `Message`
- `NewReplySender` 返回通用发送器
- `ApplyStepResult` 负责状态机推进
- 不再提供固定 `NewCallbackSender`

## 13.12 需要最终拍板的接口点

在开始实施前，需要对以下接口点做最终确认：

1. `PublishResult` 是否替代 `TaskInfo`
2. 是否仅保留 `ContextWithDelivery/DeliveryFromContext`，不再提供 `ReplySpec` 的 context helper
3. 是否接受不再为 `Task / TaskInfo / TaskPayload / HandlerFunc / Handle(...)` 提供正式兼容设计
4. workflow engine 是否确认采用“调用方定义结果消息协议，engine 提供 `ApplyStepResult(...)` 之类的状态推进 API”这一模式

## 14. 测试与验证方案

本次重构不能只验证“代码能编译”，还需要验证：

1. 通用消息抽象是否真正成立
2. broker 是否不再反向依赖 workflow
3. trace / reply 是否按设计工作
4. workflow engine 是否仍具备状态推进价值
5. asynq inspector 是否保持可用

## 14.1 测试分层

### 第一层：单元测试

目标：

- 验证纯内存逻辑
- 不依赖外部 broker
- 不依赖数据库或只依赖 mock store

覆盖重点：

- `Message / Delivery / ReplySpec` 模型行为
- trace metadata merge / restore
- workflow task codec
- engine 的状态推进 API

### 第二层：broker 集成测试

目标：

- 验证具体 broker 的消息收发落地正确
- 验证 metadata / reply 的 broker 映射正确
- 验证 broker 不需要 workflow 类型也能独立工作

覆盖重点：

- `asynqbroker`
- `rocketmqbroker`（如测试环境可用）

### 第三层：workflow 集成测试

目标：

- 验证 workflow 仍然能形成完整编排闭环
- 但结果消息协议由调用方定义
- engine 只负责状态推进

覆盖重点：

- 提交 workflow
- 消费 step message
- 自定义结果消息
- 调用 `engine.ApplyStepResult(...)`
- engine 推进 workflow

### 第四层：架构约束验证

目标：

- 防止“表面重构，实际耦合仍在”
- 验证依赖方向和接口边界

覆盖重点：

- broker 不再依赖 workflow
- 不再暴露旧 handler 抽象
- 不再残留固定 callback 协议

## 14.2 单元测试用例

### A. 通用消息模型

1. `Reply == nil` 的消息可正常构造与发布
2. `Reply != nil` 的消息可正常构造与发布
3. `Message -> Delivery` 字段映射正确
4. `PublishResult` 返回字段正确

### B. trace metadata

1. `ctx` 中存在 trace 时，发布消息自动注入 metadata
2. `ctx` 中不存在 trace 时，不注入 metadata
3. 消费端从 metadata 恢复 trace 成功
4. `sampled=0` 场景恢复正确
5. 非法 metadata 不应污染消费上下文

### C. workflow codec

1. `EncodeWorkflowTask(...)` 生成合法消息体
2. `DecodeWorkflowTask(...)` 成功解析合法 payload
3. 非法 JSON 解析失败
4. 缺少必要字段时返回错误

### D. engine 状态推进

1. `ApplyStepResult(success)` 后 step 状态成功
2. `ApplyStepResult(success)` 后若存在下一步，则推进下一步
3. 最后一步成功后 workflow 进入成功态
4. `ApplyStepResult(fail)` 后 workflow 进入失败态
5. 未知 step id 返回错误
6. 已终态 workflow 再收到结果时拒绝或幂等处理
7. 重复结果消息不会导致重复推进

## 14.3 broker 集成测试用例

### A. asynqbroker

1. `Publish(Message)` 后，`HandleMessage(...)` 能收到 `Delivery`
2. `Delivery.Type / Body / Queue` 正确
3. `Delivery.Metadata` 通过 envelope 正确恢复
4. `Delivery.Reply` 通过 envelope 正确恢复
5. `Reply == nil` 时消息仍可正常消费
6. trace 可从 producer 透传到 consumer handler
7. 不使用任何 workflow 类型，也能完成发消息与收消息

### B. rocketmqbroker

1. `Publish(Message)` 后，consumer 能收到 `Delivery`
2. `Metadata` 通过 properties 正确恢复
3. `ReplySpec` 通过 properties 正确恢复
4. `Reply == nil` 时消息仍可正常消费

如果测试环境不具备 RocketMQ，可将其列为环境依赖集成测试，不阻塞第一阶段本地验证。

## 14.4 workflow 集成测试用例

### A. workflow 基本编排

1. 提交 workflow 后，第一步成功入队
2. step consumer 收到 `Delivery`
3. consumer 显式 `DecodeWorkflowTask(delivery)`
4. consumer 自定义结果消息类型和结果消息体
5. 结果 handler 解析结果后调用 `engine.ApplyStepResult(...)`
6. engine 正确推进下一步
7. 最后一条结果处理完成后，workflow 成功结束

### B. workflow 失败路径

1. 某一步返回失败结果
2. 结果 handler 调用 `ApplyStepResult(fail)`
3. engine 将 workflow 标记为失败
4. 不再继续推进后续步骤

### C. workflow 自定义结果协议

1. 使用非 `task_callback` 的结果消息类型，例如 `workflow_step_result`
2. 使用调用方自定义结果消息结构
3. engine 仍可通过 `ApplyStepResult(...)` 正确推进

这条用例非常关键，它用来证明：

- engine 不依赖固定结果消息协议
- 调用方定义协议这一目标已经真正落地

### D. workflow 恢复与幂等

1. workflow 正在运行时，进程重启后仍可恢复
2. 重复投递同一结果消息不会导致多次推进
3. 已成功 workflow 再收到迟到结果，不会破坏终态

## 14.5 Inspector 验证

虽然本次重构核心不在 inspector，但必须验证其不受回归影响。

对 `asynqbroker` 至少验证：

1. `Inspector.Queues()` 正常
2. `GetQueueStats()` 正常
3. `ListWorkers()` 正常
4. 消息模型从 `Task` 切换到 `Message` 后，inspector 不受影响

结论标准：

- inspector 继续作为 broker 可选能力存在
- inspector 不依赖 workflow 协议层

## 14.6 架构约束检查项

这是本次重构必须做的非功能验证。

### A. 依赖方向检查

必须满足：

- `asynqbroker` 不 import workflow 类型
- `rocketmqbroker` 不 import workflow 类型
- broker 层只依赖通用消息模型

建议检查方式：

- `rg`
- `go list -deps`
- 编译独立示例

### B. API 边界检查

必须满足：

- 公共 `Consumer` 只暴露 `HandleMessage(...)`
- 正式设计中不再出现 `Handle(...)`
- 正式设计中不再出现 `TaskPayload / HandlerFunc`
- 正式设计中不再出现固定 `task_callback` 协议依赖

### C. 使用者视角检查

必须满足：

- 开发者只想使用 `asynqbroker` 发/收普通消息时，不需要理解 workflow
- 开发者可以只使用 `Message / Delivery / ReplySpec`
- workflow 只是构建在其上的上层能力

## 14.7 建议测试文件与范围

建议新增或调整以下测试：

- `taskqueue/message_test.go`
  - 通用消息模型
  - reply
  - delivery

- `taskqueue/workflow_codec_test.go`
  - `EncodeWorkflowTask`
  - `DecodeWorkflowTask`

- `taskqueue/engine_apply_result_test.go`
  - `ApplyStepResult(...)`
  - workflow 状态推进

- `taskqueue/asynqbroker/message_integration_test.go`
  - 通用消息收发
  - metadata / reply / trace

- `taskqueue/asynqbroker/inspector_test.go`
  - inspector 回归验证

- `taskqueue/rocketmqbroker/message_integration_test.go`
  - RocketMQ 环境集成验证

## 14.8 验收标准

### 架构标准

1. `asynqbroker` 可以独立作为通用消息 broker 使用
2. broker 实现不再反向依赖 workflow
3. workflow engine 不再依赖固定结果消息协议
4. `Inspector` 仍是 broker 的可选能力，而不是核心消息接口的一部分

### 功能标准

1. 消息可正常发布和消费
2. trace 可正常透传
3. reply 为可选能力，不是强制字段
4. 调用方可以自定义结果消息协议
5. engine 可通过显式 API 正确推进 workflow 状态机

### 回归标准

1. asynq inspector 能力不退化
2. 不再残留固定 `task_callback` 协议依赖
3. 不再残留旧 `Handle(...)` 抽象作为正式入口
4. 状态推进无重复推进、漏推进、终态破坏问题

### 质量标准

1. `go test ./...` 通过
2. `go test -race ./...` 通过
3. 关键失败路径有测试覆盖
4. 至少一条端到端测试证明：
   - 调用方自定义结果消息类型
   - 调用方自定义结果消息结构
   - handler 中显式调用 `engine.ApplyStepResult(...)`
   - workflow 最终正确推进

## 15. 多实例无状态支持设计

本次重构后的目标架构，应该支持：

- 多实例部署
- 实例无状态
- 任意实例消费 step message
- 任意实例消费 result message
- 任意实例调用 engine 状态推进 API

但这件事是否真正成立，不取决于消息协议本身，而取决于：

- workflow 状态是否完全以数据库为准
- 状态推进是否原子化
- 结果处理是否幂等
- 下一步推进是否防重

## 15.1 结论

结论分两层：

### 架构结论

新的“通用消息层 + workflow 状态推进 API”设计，天然适合多实例无状态。

原因：

- 消息收发由 broker 承担
- 结果消息协议由调用方决定
- engine 只负责基于持久化状态推进 workflow
- 不依赖本地内存状态

### 工程结论

如果要真正支持多实例无状态，必须新增严格的并发控制和恢复机制。

否则会出现：

- 重复应用结果
- 重复推进下一步
- workflow 计数错误
- DB 状态与 MQ 投递状态不一致

## 15.2 多实例场景下必须满足的约束

### 1. 数据库是唯一事实源

必须满足：

- workflow 当前状态只看 DB
- step 当前状态只看 DB
- 不依赖进程内 map / 本地缓存 / 本地锁

任何实例重启或扩缩容后，都应能从数据库恢复执行上下文。

### 2. `ApplyStepResult(...)` 必须幂等

结果消息在真实环境中天然可能重复：

- broker retry
- 网络超时导致重发
- 消费端重复投递

因此必须保证：

- 同一个 step result 被重复处理时，不会推进两次
- 不会重复增加 `completed_steps/failed_steps`
- 不会重复发送下一步任务

### 3. 状态推进必须原子化

以下动作不能散落在多个无保护操作中：

- 标记当前 step 成功/失败
- 更新 workflow 聚合状态
- 判断是否有下一步
- claim 下一步

至少这些状态变更必须在同一个数据库事务中完成。

### 4. 下一步推进必须有唯一 claim 机制

多实例下，最危险的情况之一是：

- 两个实例几乎同时认为“该推进下一步了”
- 于是都发送下一步消息

必须防止这种情况。

### 5. DB 与 MQ 的一致性必须有恢复策略

即使数据库事务做对了，仍然可能出现：

- DB 已提交，但 MQ 发送失败
- MQ 已发送，但 DB 未来状态更新失败

至少需要：

- outbox
- 或补偿扫描 / 恢复机制

否则多实例下会更难排查和恢复。

## 15.3 当前旧实现的主要风险点

当前 `taskqueue` 的旧 workflow 实现并不适合直接多实例运行，主要风险包括：

### 1. 下一步获取不是 claim

当前 [GetNextPendingStep](/root/workspace/nsp/nsp_platform/taskqueue/pg_store.go#L262) 只是普通查询：

- 没有 `FOR UPDATE`
- 没有 claim 字段
- 没有唯一推进保护

两个实例可能同时取到同一个 pending step。

### 2. 结果应用与推进不是一个事务闭环

当前 [HandleCallback](/root/workspace/nsp/nsp_platform/taskqueue/engine.go#L195) 的模式是：

1. 更新 step result
2. 然后再决定 success/failure 分支
3. 再做 workflow 聚合更新
4. 再推进下一步

这些动作分散在多个调用中，多实例下容易产生竞争窗口。

### 3. 聚合计数是裸增量

当前：

- [IncrementCompletedSteps](/root/workspace/nsp/nsp_platform/taskqueue/pg_store.go#L152)
- [IncrementFailedSteps](/root/workspace/nsp/nsp_platform/taskqueue/pg_store.go#L159)

都是无条件加一。

一旦重复应用结果，会导致聚合状态不准确。

### 4. 入队与状态更新不是强一致

当前 `enqueueStep(...)` 中：

- 先 publish
- 再写 broker_task_id
- 再更新 step 状态

这不是一个可靠的“持久化推进 + 投递”模型。

## 15.4 推荐实现机制

### A. `ApplyStepResult(...)` 事务化

推荐将 `ApplyStepResult(...)` 设计为数据库事务驱动的状态推进入口。

事务内建议完成：

1. `SELECT step FOR UPDATE`
2. 校验当前 step 状态是否允许接收结果
3. 若 step 已终态，则做幂等返回
4. 更新 step 状态/result/error
5. 更新 workflow 聚合状态
6. 判断 workflow 是否终态
7. 若需推进下一步，则 claim 下一步
8. 提交事务

事务外再做消息投递，或使用 outbox 统一投递。

### B. 条件更新 / CAS

推荐所有关键状态流转都带条件：

```sql
UPDATE tq_steps
SET status = 'completed', ...
WHERE id = $1 AND status IN ('queued', 'running')
```

通过 `RowsAffected()` 判断是否真正拿到推进权。

这样可以天然抵抗重复结果消息。

### C. 下一步 claim 机制

建议不要再用简单的 `GetNextPendingStep()` 普通查询模式。

可以考虑两种方案：

#### 方案 1：状态 claim

直接在事务中把下一步从 `pending` 改为 `dispatching` / `queued`：

```sql
UPDATE tq_steps
SET status = 'dispatching', updated_at = NOW()
WHERE id = (
    SELECT id
    FROM tq_steps
    WHERE workflow_id = $1 AND status = 'pending'
    ORDER BY step_order ASC
    LIMIT 1
    FOR UPDATE SKIP LOCKED
)
RETURNING ...
```

#### 方案 2：增加 claim 字段

例如增加：

- `claimed_by`
- `claimed_at`
- `dispatch_token`

但对当前场景来说，状态 claim 往往已经够用。

### D. Outbox 或恢复扫描

最稳妥方案是 outbox：

- 在同一事务里写入待发送消息记录
- 由 dispatcher 异步发送到 MQ

如果不做 outbox，至少需要恢复扫描：

- 扫描 DB 中已 claim 但未真正投递的 step
- 重新补发消息

否则会出现：

- workflow 看起来已推进
- 但下一步消息根本没发出去

### E. 结果消息幂等键

建议 `StepResult` 中至少包含：

- `StepID`
- `Status`
- `Result/Error`

如果调用方结果协议中本身有唯一结果 ID，也可附加。

但最关键的幂等键仍然是：

- `StepID + 当前可接受状态`

## 15.5 建议的 engine API 语义

为了支持多实例无状态，`ApplyStepResult(...)` 建议具备以下语义：

```go
func (e *Engine) ApplyStepResult(ctx context.Context, result *StepResult) error
```

要求：

1. 幂等
2. 可重复调用
3. 基于 DB 事务推进
4. 不依赖调用方进程本地状态
5. 在并发场景下只有一个实例真正推进下一步

可选返回值设计：

```go
type ApplyResult struct {
    WorkflowID     string
    StepID         string
    StepStatus     string
    WorkflowStatus string
    EnqueuedNext   bool
    Idempotent     bool
}
```

这样便于日志和调用方观测。

## 15.6 建议的表结构增强方向

如果要把多实例做扎实，建议考虑增强 `tq_steps`：

- `dispatch_token`
- `dispatch_status` 或更细粒度状态，如 `dispatching`
- `result_applied_at`

也可以考虑增强 `tq_workflows`：

- `version`

这些字段不是必须一步到位，但如果完全不加，很多并发安全只能靠脆弱约定支撑。

## 15.7 多实例测试用例

必须新增专门的多实例测试。

### A. 重复结果消息

1. 同一个 step result 被提交两次
2. 只允许一次真正推进
3. 聚合计数不重复增加

### B. 并发推进下一步

1. 两个 goroutine/实例几乎同时处理同一 step 的结果
2. 只允许一个实例成功 claim 下一步
3. 下一步消息只发送一次

### C. 已终态重复写入

1. workflow 已成功
2. 再收到某个旧结果消息
3. workflow 终态不被破坏

### D. DB 提交后 MQ 发送失败

1. 模拟 DB 成功推进
2. MQ 发送失败
3. 能通过 outbox 或恢复扫描补发

### E. 实例重启恢复

1. step 已 claim 但进程崩溃
2. 新实例启动
3. 能恢复并继续推进

## 15.8 多实例验收标准

只有满足以下标准，才能认为“支持多实例无状态”：

### 架构标准

1. 任意实例都可独立处理结果消息
2. 不依赖本地内存状态
3. engine 状态推进完全以 DB 为准

### 并发标准

1. 重复结果不会重复推进
2. 同一步不会被多个实例重复 claim
3. 下一步不会被重复投递

### 恢复标准

1. 实例崩溃后可恢复
2. MQ 发送失败后有补偿路径
3. workflow 最终状态一致

### 质量标准

1. 至少有并发单测/集成测试覆盖重复推进场景
2. 至少有恢复测试覆盖崩溃/补发场景
3. race 检测通过
