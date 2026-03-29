## Context

`taskqueue-priority-demo` 是 NSP 平台中演示多优先级任务队列的示例。原实现使用了独立的 `internal/config` 和 `internal/handler` 包，直接依赖 `asynq`，并使用了与 `taskqueue-broker-demo`（重构后的参考实现）不一致的队列命名（`taskqueue:business:task:send:priority:*`）和代码模式。重构目标是将其对齐到最新的 broker-demo 实现风格，统一内存 store、trace 集成、回调机制和队列命名规范。

## Goals / Non-Goals

**Goals:**
- 与 `taskqueue-broker-demo` 的代码模式保持一致（store 包、TaskManager、CallbackSender、trace 初始化）
- 队列命名统一使用 `nsp:taskqueue:high` / `nsp:taskqueue:middle` / `nsp:taskqueue:low` / `nsp:taskqueue:callback`（多词用 `:` 分隔）
- 保留原 demo 的核心功能：三优先级队列投递、权重消费、多种任务类型处理
- 重构完成后可成功运行并通过测试

**Non-Goals:**
- 不引入持久化数据库（保持内存 store）
- 不新增 Inspector 队列统计（原 demo 该功能可移除或保留为可选）
- 不修改 `taskqueue` 或 `asynqbroker` 底层库

## Decisions

### 复用 store 包模式
参照 broker-demo，新建 `store/store.go`，包含：
- 队列名称常量（`nsp:taskqueue:high/middle/low/callback`）
- `MustRedisAddr()` 工具函数
- 内存 `TaskStore` 实现（含 CRUD、状态更新、重试计数）

**原因**：避免重复定义，统一命名，与 broker-demo 一致。

### 移除 internal/ 目录
原 `internal/config` 和 `internal/handler` 包职责被 `store/store.go` 和 consumer `main.go` 内联 handler 取代。保留 `internal/` 目录结构没有意义，直接删除。

**原因**：减少不必要的包层级，与 broker-demo 扁平结构对齐。

### producer 引入 TaskManager + 回调消费者
producer 不仅发送任务，还监听 `nsp:taskqueue:callback` 接收回调，通过 TaskManager 管理任务生命周期（重试、状态更新）。

**原因**：演示完整的请求-响应模式，与 broker-demo 功能对齐。

### consumer 使用内联 handler + CallbackSender
consumer main 内直接注册所有 handler（无独立 handler 包），每个 handler 执行完后通过 CallbackSender 发送结果到 callback 队列。

**原因**：减少包层级，handler 逻辑简单，无需独立包。

### 队列命名规范
- 高优先级：`nsp:taskqueue:high`
- 中优先级：`nsp:taskqueue:middle`
- 低优先级：`nsp:taskqueue:low`
- 订单类回调队列：`nsp:taskqueue:callback:order`
- 通知类回调队列：`nsp:taskqueue:callback:notify`

**原因**：多词用 `:` 分隔，符合 Redis key 命名规范；双回调队列演示不同业务域的结果路由，避免不同类型回调混入同一队列造成消费者耦合。

### 双回调队列路由机制

producer 在构造 `taskqueue.Task` 时，根据任务业务域在 `Reply.Queue` 中填入对应回调队列：
- 订单类任务（`send:payment:notification`、`deduct:inventory`、`generate:report`、`export:data`）→ `Reply.Queue = nsp:taskqueue:callback:order`
- 通知类任务（`send:email`、`send:notification`、`process:image`）→ `Reply.Queue = nsp:taskqueue:callback:notify`

consumer `CallbackSender` 不感知路由逻辑，直接读取 `task.Reply.Queue` 发回结果。

producer 侧启动两个独立回调消费者，分别监听两条回调队列，各自更新 TaskStore 中对应任务的状态。

**原因**：路由信息随消息传递，consumer 无需任何配置变更即可支持新增回调队列；双消费者隔离不同业务域回调处理，互不影响。

## Risks / Trade-offs

- [风险] 删除 `internal/` 会破坏任何依赖它的其他代码 → 搜索确认，demo 内部引用，无外部依赖
- [风险] 队列命名变更导致存量 Redis 数据不兼容 → demo 场景下无持久数据，重启 Redis 即可
- [取舍] 移除 Inspector 队列统计功能 → 该功能不在核心演示目标内，可通过 asynq dashboard 替代观察
- [取舍] 两条回调队列由 producer 内两个 goroutine 分别消费，增加少量复杂度 → 恰好演示多消费者并发模式，是 demo 价值所在
