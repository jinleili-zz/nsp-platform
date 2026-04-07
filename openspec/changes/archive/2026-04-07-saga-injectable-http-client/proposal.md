## Why

当前 SAGA Executor 在 `NewExecutor()` 中自行创建默认 `http.Client`（仅可配置 Timeout），
业务侧无法注入自定义 TLS Transport、Root CA、ServerName 或证书热更新后的新 client。
这导致需要 HTTPS 出站的业务场景（如 `vpc_workflow_demo` 中 Top -> AZ 的内部调用链）
无法通过 SAGA 引擎完成，被迫绕过 SAGA 或 fork 代码。
平台层必须提供此扩展点，否则每个有 HTTPS 需求的业务都需要 patch SAGA 内部实现。

## What Changes

- 在 `saga.Config` 中新增可选的 `HTTPClient *http.Client` 字段，允许业务方注入自定义 HTTP client
- 在 `ExecutorConfig` 中新增对应的 `HTTPClient *http.Client` 字段作为内部传递通道
- 修改 `NewExecutor()` 逻辑：当 `HTTPClient` 非 nil 时直接使用，否则保持当前默认 client 创建行为
- 保证 SAGA 所有出站 HTTP 调用路径（action / compensation / poll）统一走注入的 client
- 现有 trace 注入和 AK/SK signing 行为不变，HTTPS 由底层 client/transport 承载

## Capabilities

### New Capabilities
- `saga-http-client-injection`: 支持业务方通过 `saga.Config.HTTPClient` 注入自定义 `*http.Client`，使 SAGA 引擎的所有出站 HTTP 请求（action、compensation、poll）使用业务提供的 client 实例，从而支持自定义 TLS 配置、Root CA、ServerName 及证书热更新等场景

### Modified Capabilities

（无现有 spec 需要修改）

## Impact

- **受影响代码**：`saga/engine.go`（Config 结构体）、`saga/executor.go`（ExecutorConfig 结构体 + NewExecutor 函数）
- **API 变更**：`saga.Config` 和 `ExecutorConfig` 各新增一个可选字段，零值行为与当前完全一致，**不是 breaking change**
- **依赖关系**：无新增外部依赖，仍使用标准库 `net/http`
- **现有测试**：不受影响，未传入 HTTPClient 时行为不变
- **文档同步更新**：新增导出 API 字段需同步更新以下文档：
  - `AGENTS.md` — SAGA 模块说明部分
  - `docs/saga.md` — SAGA 用户文档
  - `docs/modules/saga.md` — 模块级文档
  - `saga/README.md` — 包级 README
- **下游业务**：`vpc_workflow_demo` 等需要 HTTPS 的业务仓库可在构造 `saga.Config` 时传入自定义 client
