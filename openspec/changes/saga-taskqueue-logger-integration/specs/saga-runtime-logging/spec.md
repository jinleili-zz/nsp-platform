## ADDED Requirements

### Requirement: Saga runtime components use repository logger
`saga` 运行时组件 SHALL 通过仓库 `logger` 模块输出运行日志，而不是直接使用 `fmt.Printf` 或 `log.Printf`。

这里的运行时组件至少包括：
- `Engine`
- `Coordinator`
- `Poller`
- `Executor`

运行日志至少包括：
- 后台扫描、恢复和超时处理中的异常或告警
- 事务队列压力、事务状态推进、补偿和轮询过程中的关键运行事件
- 重试、续约、状态更新等失败路径

#### Scenario: Coordinator emits queue pressure warning
- **WHEN** `Coordinator.Submit` 因内部事务队列已满而拒绝新的 `tx_id`
- **THEN** 该告警日志 SHALL 通过仓库 `logger` 输出，而不是直接写 stdout/stderr

#### Scenario: Poller emits runtime failure log
- **WHEN** `Poller` 在扫描或处理 poll task 时遇到存储层、请求层或状态更新失败
- **THEN** 相关运行日志 SHALL 通过仓库 `logger` 输出，而不是直接使用标准库打印

### Requirement: Saga runtime logs include execution context
`saga` 运行日志 SHALL 在可用时携带执行上下文字段，至少包括模块运行标识和 trace 关联信息。

具体要求：
- 当日志与某个事务相关时，SHALL 包含 `tx_id`
- 当日志与某个步骤相关时，SHALL 包含 `step_id`，并在可用时包含 `step_name`
- 当调用路径持有带 trace 的 `context.Context` 时，SHALL 通过 context-aware logger 自动带出 `trace_id` 和 `span_id`

#### Scenario: Step execution log carries trace fields
- **WHEN** 某个带 trace 上下文的事务步骤在执行或补偿过程中产生运行日志
- **THEN** 日志 SHALL 同时包含 saga 相关字段（如 `tx_id`、`step_id`）以及从 `context.Context` 提取的 `trace_id` / `span_id`

#### Scenario: Background saga log without trace still carries identifiers
- **WHEN** 后台恢复扫描或超时扫描在没有业务 trace 的上下文中输出日志
- **THEN** 日志 SHALL 至少包含相关的 `tx_id`、`step_id` 或实例级运行标识，而不是只输出裸文本消息

### Requirement: Saga logger configuration is optional and non-breaking
`saga.Config` SHALL 支持可选的 logger 注入，同时保持现有 `NewEngine(cfg *Config) (*Engine, error)` 构造方式不变。

当调用方未显式配置 logger 时，`saga` 实现 SHALL 使用仓库默认全局 logger 作为回退。

#### Scenario: Engine uses global logger by default
- **WHEN** 调用方创建 `saga.Engine` 时未显式提供模块 logger
- **THEN** `Engine`、`Coordinator`、`Poller` 和 `Executor` SHALL 继续正常工作，并使用仓库默认全局 logger 输出运行日志

#### Scenario: Engine uses injected logger when provided
- **WHEN** 调用方在 `saga.Config` 中显式提供模块 logger
- **THEN** `saga` 运行时组件 SHALL 使用该注入 logger 输出运行日志，而不是回退到默认全局 logger
