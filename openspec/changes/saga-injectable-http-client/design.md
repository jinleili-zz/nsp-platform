## Context

当前 SAGA 引擎的 `Executor` 在 `NewExecutor()` 中硬编码创建一个默认的 `http.Client`，
仅暴露 `HTTPTimeout` 一个配置项。所有出站 HTTP 请求（action / compensation / poll）
均通过这个内部 client 发出。

业务方无法：
- 配置自定义 TLS Transport（Root CA、ServerName、客户端证书等）
- 在证书/CA 文件更新后替换底层 client
- 对 SAGA 出站请求使用与业务其他 HTTP 调用一致的 transport 策略

现有架构中，trace 注入（`trace.Inject`）和 AK/SK 签名（`signRequestIfNeeded`）
在 `*http.Request` 层面操作，与底层 `http.Client` / `http.Transport` 无关，
因此替换 client 不会影响这两个能力。

相关文件：
- `saga/engine.go`: `Config` → `ExecutorConfig` 传递 `HTTPTimeout`
- `saga/executor.go`: `NewExecutor()` 创建 `http.Client`，所有 HTTP 调用走 `e.client.Do(req)`
- `saga/poller.go`: 委托 `executor.Poll()` 发起 HTTP 请求，不直接持有 client

## Goals / Non-Goals

**Goals:**
- 允许业务方通过 `saga.Config.HTTPClient` 注入自定义 `*http.Client` 实例
- 注入的 client 覆盖 SAGA 所有出站 HTTP 路径（action、compensation、poll）
- 不传入时保持当前默认行为，完全向后兼容
- 变更范围最小化：仅涉及 Config 结构体和 NewExecutor 初始化逻辑

**Non-Goals:**
- 不提供通用 TLS 证书热更新框架（由业务侧自行实现）
- 不引入 mTLS 支持
- 不改动 AK/SK 签名协议或 CredentialStore
- 不管理证书文件路径、CA 文件路径、URL scheme 选择
- 不对 trace / auth 以外的 HTTP 模块做统一改造
- 不引入 `http.RoundTripper` 接口抽象（`*http.Client` 已足够，且与现有代码类型一致）

## Decisions

### Decision 1: 在 `*http.Client` 层面注入，而非 `http.RoundTripper`

**选择**：`HTTPClient *http.Client`

**替代方案**：`Transport http.RoundTripper`

**理由**：
- 现有 Executor 内部使用 `e.client.Do(req)`，直接替换 client 变更最小
- `*http.Client` 包含 Timeout、CheckRedirect、Jar 等配置，业务方可完整控制行为
- 如果只暴露 Transport，业务方仍需关心 Timeout 等 client 级配置与 SAGA 默认值的交互
- `*http.Client` 是标准库类型，不引入额外抽象

### Decision 2: 注入点放在 `saga.Config`，内部透传到 `ExecutorConfig`

**选择**：在 `saga.Config` 新增 `HTTPClient` 字段，`NewEngine()` 内部将其赋值给 `ExecutorConfig.HTTPClient`

**理由**：
- 业务方只与 `saga.Config` 交互，不需要了解 `ExecutorConfig` 的存在
- 保持 `ExecutorConfig` 也有此字段，方便单元测试直接构造 Executor
- 与现有 `HTTPTimeout` 字段的传递模式一致

### Decision 3: HTTPClient 和 HTTPTimeout 的交互规则

**规则**：
- 当 `HTTPClient != nil` 时，直接使用业务提供的 client，忽略 `HTTPTimeout` 配置
  （业务 client 的 Timeout 由业务自行设置）
- 当 `HTTPClient == nil` 时，使用 `HTTPTimeout` 创建默认 client（当前行为不变）

**理由**：
- 避免平台覆盖业务 client 的 Timeout 设置，尊重业务方的完整控制权
- 如果同时传入两者，以 HTTPClient 为准，减少歧义

### Decision 4: 不引入 ClientFactory / ClientProvider 动态接口

**选择**：直接注入 `*http.Client` 实例，不提供 `func() *http.Client` 等工厂接口

**理由**：
- 证书热更新场景下，业务方可通过自定义 `http.Transport`（配合 `tls.Config.GetCertificate`
  或定期替换 Engine 实例）实现，不需要平台层提供动态 client 刷新机制
- 保持 API 简单，一个字段解决问题
- 如果未来确实需要动态刷新，可以用后续 proposal 增加，不阻塞当前需求

## Risks / Trade-offs

- **[风险] 业务 client 配置不当导致 SAGA 请求失败** → 这是业务侧职责，平台层只负责"使用"，
  不负责"校验" client 配置的合理性。SAGA 现有的错误处理和重试机制照常生效。

- **[风险] HTTPClient 和 HTTPTimeout 同时传入时的语义混淆** → 通过文档明确说明：
  HTTPClient 非 nil 时 HTTPTimeout 被忽略。可在 NewEngine() 中加日志提示。

- **[取舍] 不支持 per-step 级别的 client 注入** → 当前所有 step 共享同一个 client。
  如果业务需要不同 step 走不同的 TLS 配置，需要在 Transport 层面用 URL/host 路由区分，
  或提交后续 proposal 支持 per-step client。当前场景（Top -> AZ 统一 HTTPS）不需要此能力。

- **[取舍] 不提供 Transport 层面的 wrap/middleware 机制** → 如果业务需要在 transport 层
  做日志、metrics 等，可以自行 wrap Transport 后构造 client 传入。平台不提供额外 hook。
