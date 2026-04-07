## ADDED Requirements

### Requirement: SAGA Config 支持注入自定义 HTTP Client
`saga.Config` 结构体 SHALL 提供一个可选的 `HTTPClient *http.Client` 字段，
允许业务方在创建引擎时注入自定义的 HTTP client 实例。
当 `HTTPClient` 为 nil 时，引擎 MUST 使用 `HTTPTimeout` 创建默认 client，保持向后兼容。

#### Scenario: 注入自定义 HTTPClient
- **WHEN** 业务方构造 `saga.Config` 时传入非 nil 的 `HTTPClient`
- **THEN** 引擎创建的 Executor MUST 使用该 client 发送所有出站 HTTP 请求

#### Scenario: 未注入 HTTPClient 使用默认行为
- **WHEN** 业务方构造 `saga.Config` 时 `HTTPClient` 为 nil
- **THEN** 引擎 MUST 使用 `HTTPTimeout` 配置创建默认 `http.Client`，行为与改动前完全一致

#### Scenario: HTTPClient 和 HTTPTimeout 同时传入
- **WHEN** 业务方同时传入非 nil 的 `HTTPClient` 和非零的 `HTTPTimeout`
- **THEN** 引擎 MUST 使用 `HTTPClient`，忽略 `HTTPTimeout` 配置

### Requirement: ExecutorConfig 支持注入自定义 HTTP Client
`ExecutorConfig` 结构体 SHALL 提供一个可选的 `HTTPClient *http.Client` 字段。
`NewExecutor()` 在 `HTTPClient` 非 nil 时 MUST 直接使用该 client，
否则 MUST 使用 `HTTPTimeout` 创建默认 client。

#### Scenario: 通过 ExecutorConfig 注入 HTTPClient
- **WHEN** 调用 `NewExecutor()` 时 `ExecutorConfig.HTTPClient` 非 nil
- **THEN** Executor MUST 使用该 client，不再内部创建新的 `http.Client`

#### Scenario: ExecutorConfig 未传入 HTTPClient
- **WHEN** 调用 `NewExecutor()` 时 `ExecutorConfig.HTTPClient` 为 nil
- **THEN** Executor MUST 以 `HTTPTimeout` 创建默认 `http.Client`

### Requirement: 注入的 HTTPClient 覆盖所有出站路径
注入的 `*http.Client` MUST 被用于 SAGA 引擎的所有出站 HTTP 调用，包括：
- 同步步骤的 action 请求（`ExecuteStep`）
- 异步步骤的 action 请求（`ExecuteAsyncStep`）
- 补偿请求（`CompensateStep`）
- 轮询请求（`Poll`）

#### Scenario: 同步步骤 action 使用注入的 client
- **WHEN** Executor 执行同步步骤的 action 请求
- **THEN** MUST 通过注入的 `*http.Client` 发送请求

#### Scenario: 异步步骤 action 使用注入的 client
- **WHEN** Executor 执行异步步骤的 action 请求
- **THEN** MUST 通过注入的 `*http.Client` 发送请求

#### Scenario: 补偿请求使用注入的 client
- **WHEN** Executor 执行补偿请求
- **THEN** MUST 通过注入的 `*http.Client` 发送请求

#### Scenario: 轮询请求使用注入的 client
- **WHEN** Executor 执行轮询请求
- **THEN** MUST 通过注入的 `*http.Client` 发送请求

### Requirement: Trace 和 AK/SK 签名与注入的 HTTPClient 互不干扰
SAGA 现有的 trace 注入（`trace.Inject`）和 AK/SK 签名（`signRequestIfNeeded`）
在 `*http.Request` 层面操作，MUST 不受注入的 `*http.Client` 影响。
HTTPS 传输由底层 client/transport 承载，签名和 trace 在请求头层面叠加。

#### Scenario: 使用自定义 HTTPS client 时 trace 头正常注入
- **WHEN** 注入了配置 TLS 的自定义 client 且请求 context 中有 TraceContext
- **THEN** 出站请求 MUST 同时包含 TLS 加密传输和 X-B3-TraceId 等 trace 请求头

#### Scenario: 使用自定义 HTTPS client 时 AK/SK 签名正常工作
- **WHEN** 注入了配置 TLS 的自定义 client 且 step 配置了 AuthAK
- **THEN** 出站请求 MUST 同时包含 TLS 加密传输和 NSP-HMAC-SHA256 Authorization 签名头

### Requirement: 向后兼容性保证
本次变更 MUST NOT 是 breaking change。所有现有调用方在不修改代码的情况下，
行为 MUST 与变更前完全一致。

#### Scenario: 现有代码无需修改
- **WHEN** 现有业务方使用 `saga.Config` 未传入 `HTTPClient`（即字段零值 nil）
- **THEN** 引擎行为 MUST 与本次变更前完全一致，所有现有测试 MUST 继续通过
