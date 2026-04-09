## ADDED Requirements

### Requirement: Asynq runtime logs use repository logger by default
`taskqueue/asynqbroker` 的运行日志 SHALL 默认通过仓库 `logger` 模块输出，包括 consumer 包装层日志、broker/inspector 具体实现日志，以及 asynq 框架日志的默认桥接输出。

实现 SHALL 不再在运行路径中直接使用 `log.Printf` 记录 handler 错误或类似运行时事件。

#### Scenario: Consumer wrapper logs handler error through repository logger
- **WHEN** asynq consumer 调用业务 `handler` 返回错误
- **THEN** wrapper 记录的错误日志 SHALL 通过仓库 `logger` 输出，并包含 `task_type` 与 `task_id` 等任务标识字段

#### Scenario: Asynq framework logs bridge to repository logger by default
- **WHEN** 调用方未显式提供 `ConsumerConfig.Logger asynq.Logger`
- **THEN** asynq server 的默认运行日志 SHALL 被桥接到仓库 `logger`，而不是落回第三方默认日志输出

#### Scenario: Broker and inspector logs follow the same repository logger strategy
- **WHEN** `taskqueue/asynqbroker.Broker` 或 `taskqueue/asynqbroker.Inspector` 在运行路径中输出日志
- **THEN** 这些日志 SHALL 通过仓库 `logger` 输出，并与 consumer 包装层日志遵循相同的默认分类和注入策略

### Requirement: Asynq consumer logs preserve restored trace context
当 asynq consumer 从 trace envelope 恢复 `TraceContext` 后，后续由 consumer 包装层输出的运行日志 SHALL 使用 context-aware logger，以便自动携带 `trace_id` 和 `span_id`。

#### Scenario: Error log includes restored trace fields
- **WHEN** consumer 收到带 trace envelope 的消息，恢复出 `TraceContext`，且业务 `handler` 返回错误
- **THEN** 该错误日志 SHALL 通过 context-aware repository logger 输出，并自动包含恢复后的 `trace_id` / `span_id`

#### Scenario: Legacy message logs gracefully without trace metadata
- **WHEN** consumer 收到不带 trace envelope 的旧消息且运行路径需要输出日志
- **THEN** 日志 SHALL 仍然通过 repository logger 输出，并在没有 trace 字段时保持兼容而不失败

### Requirement: Asynqbroker logger configuration remains backward compatible
`taskqueue/asynqbroker` 的具体实现 SHALL 提供一致的仓库 logger 配置能力，同时保持现有抽象接口与默认构造入口的向后兼容。

优先级规则 SHALL 为：
- 若调用方显式提供 `ConsumerConfig.Logger`，实现 SHALL 继续使用该 `asynq.Logger` 作为框架日志输出
- 若未提供 `ConsumerConfig.Logger`，实现 SHALL 使用仓库 logger 适配器作为默认框架日志输出
- consumer 包装层自己的运行日志 SHALL 使用仓库 logger 配置，而不是直接复用标准库 logger
- `Broker` / `Inspector` 的运行日志 SHALL 支持显式注入仓库 logger；若调用方未显式提供，则默认使用仓库的既定 logger 策略
- 现有 `NewBroker(opt)` / `NewInspector(opt)` 构造入口 SHALL 继续可用；若调用方需要显式注入 logger，应通过新增的配置化构造入口完成，而不是破坏现有调用方式

#### Scenario: Explicit asynq logger override is preserved
- **WHEN** 调用方显式提供 `ConsumerConfig.Logger`
- **THEN** `taskqueue/asynqbroker` SHALL 保留该覆盖行为，不得强制替换为仓库 logger adapter

#### Scenario: Repository runtime logger can be injected separately
- **WHEN** 调用方为 `taskqueue/asynqbroker.ConsumerConfig` 显式提供仓库运行时 logger 配置
- **THEN** consumer 包装层运行日志 SHALL 使用该注入 logger，并与框架日志覆盖规则按既定优先级协同工作

#### Scenario: Broker and inspector keep default constructors while allowing injected logger
- **WHEN** 调用方继续使用现有 `NewBroker(opt)` 或 `NewInspector(opt)` 默认构造入口
- **THEN** 这些构造方式 SHALL 继续工作，并使用默认仓库 logger 策略
- **AND WHEN** 调用方需要为 `Broker` 或 `Inspector` 显式注入仓库 logger
- **THEN** 实现 SHALL 通过新增的配置化构造入口支持该能力，而不是要求调用方迁移现有默认构造调用
