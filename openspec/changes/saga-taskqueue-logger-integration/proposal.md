## Why

当前 `saga` 和 `taskqueue/asynqbroker` 的运行时日志仍然混用 `fmt.Printf`、`log.Printf` 和第三方默认 logger，导致日志格式、输出位置、级别控制和 trace 关联都不一致。仓库已经具备统一的 `logger` 模块，现在需要把这两个基础模块的后台运行日志收敛到同一套日志体系，避免继续扩大观测碎片化。

## What Changes

- 将 `saga` 模块中 `Engine`、`Coordinator`、`Poller`、`Executor` 的运行时日志统一改为通过仓库 `logger` 模块输出
- 将 `taskqueue/asynqbroker` 中 consumer 包装层和 broker 包内的运行时日志统一改为通过仓库 `logger` 模块输出
- 为 `saga` 和 `taskqueue/asynqbroker` 暴露可选的 logger 注入配置，并保持现有核心接口和构造入口向后兼容
- 在具备 `context.Context` 的路径上优先使用 context-aware 日志接口，保证 `trace_id`、`span_id` 能自动进入日志字段
- 为 asynq 运行时日志补一层 logger 适配，默认走仓库 `logger`，同时保留调用方显式传入 `asynq.Logger` 的能力
- 审计并移除上述模块中面向运行时路径的 `fmt.Printf` / `log.Printf` 直接输出
- 同步补充 `saga`、`taskqueue` 和相关模块文档，说明新的日志行为与配置方式

## Capabilities

### New Capabilities
- `saga-runtime-logging`: 定义 SAGA 运行时组件统一使用仓库 `logger` 输出结构化日志、携带事务/步骤上下文并支持可选 logger 注入

### Modified Capabilities
- `broker-asynq`: 扩展 asynq broker/consumer 的运行时日志要求，统一接入仓库 `logger` 并保留显式 `asynq.Logger` 覆盖能力

## Impact

- **受影响代码**：
  - `saga/engine.go`
  - `saga/coordinator.go`
  - `saga/poller.go`
  - `saga/executor.go`
  - `taskqueue/asynqbroker/consumer.go`
  - `taskqueue/asynqbroker/broker.go`
  - 可能新增 asynq 到仓库 `logger` 的适配辅助代码
- **API 影响**：
  - 不修改 `taskqueue.Broker` / `taskqueue.Consumer` 核心接口
  - 预计仅新增可选配置字段，例如 `saga.Config` 和 `taskqueue/asynqbroker.ConsumerConfig` 的 logger 配置
- **依赖关系**：
  - 复用现有仓库 `logger` 模块
  - 不计划新增外部日志依赖
- **运行时影响**：
  - 统一日志格式、级别和 trace 关联
  - 可能带来更明确的字段化日志和更高的默认日志密度，需要控制噪声
- **文档同步**：
  - `AGENTS.md`
  - `docs/saga.md`
  - `docs/modules/saga.md`
  - `saga/README.md`
  - `taskqueue/GUIDE.md`
