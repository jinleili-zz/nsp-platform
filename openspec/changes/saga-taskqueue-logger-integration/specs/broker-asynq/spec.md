## ADDED Requirements

### Requirement: Asynq runtime logs use repository logger by default
`taskqueue/asynqbroker` 的运行日志 SHALL 默认通过仓库 `logger` 模块输出，包括 consumer 包装层日志以及 asynq 框架日志的默认桥接输出。

实现 SHALL 不再在运行路径中直接使用 `log.Printf` 记录 handler 错误或类似运行时事件。

#### Scenario: Consumer wrapper logs handler error through repository logger
- **WHEN** asynq consumer 调用业务 `handler` 返回错误
- **THEN** wrapper 记录的错误日志 SHALL 通过仓库 `logger` 输出，并包含 `task_type` 与 `task_id` 等任务标识字段

#### Scenario: Asynq framework logs bridge to repository logger by default
- **WHEN** 调用方未显式提供 `ConsumerConfig.Logger asynq.Logger`
- **THEN** asynq server 的默认运行日志 SHALL 被桥接到仓库 `logger`，而不是落回第三方默认日志输出

### Requirement: Asynq consumer logs preserve restored trace context
当 asynq consumer 从 trace envelope 恢复 `TraceContext` 后，后续由 consumer 包装层输出的运行日志 SHALL 使用 context-aware logger，以便自动携带 `trace_id` 和 `span_id`。

#### Scenario: Error log includes restored trace fields
- **WHEN** consumer 收到带 trace envelope 的消息，恢复出 `TraceContext`，且业务 `handler` 返回错误
- **THEN** 该错误日志 SHALL 通过 context-aware repository logger 输出，并自动包含恢复后的 `trace_id` / `span_id`

#### Scenario: Legacy message logs gracefully without trace metadata
- **WHEN** consumer 收到不带 trace envelope 的旧消息且运行路径需要输出日志
- **THEN** 日志 SHALL 仍然通过 repository logger 输出，并在没有 trace 字段时保持兼容而不失败

### Requirement: Consumer logger configuration remains backward compatible
`taskqueue/asynqbroker.ConsumerConfig` SHALL 在保留现有 `Logger asynq.Logger` 覆盖能力的同时，支持一个面向仓库 `logger` 的可选运行时 logger 配置。

优先级规则 SHALL 为：
- 若调用方显式提供 `ConsumerConfig.Logger`，实现 SHALL 继续使用该 `asynq.Logger` 作为框架日志输出
- 若未提供 `ConsumerConfig.Logger`，实现 SHALL 使用仓库 logger 适配器作为默认框架日志输出
- consumer 包装层自己的运行日志 SHALL 使用仓库 logger 配置，而不是直接复用标准库 logger

#### Scenario: Explicit asynq logger override is preserved
- **WHEN** 调用方显式提供 `ConsumerConfig.Logger`
- **THEN** `taskqueue/asynqbroker` SHALL 保留该覆盖行为，不得强制替换为仓库 logger adapter

#### Scenario: Repository runtime logger can be injected separately
- **WHEN** 调用方为 `taskqueue/asynqbroker.ConsumerConfig` 显式提供仓库运行时 logger 配置
- **THEN** consumer 包装层运行日志 SHALL 使用该注入 logger，并与框架日志覆盖规则按既定优先级协同工作
