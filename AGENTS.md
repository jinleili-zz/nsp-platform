# 仓库协作说明

## Git 提交规范

所有提交到 GitHub 的变更，必须通过 Pull Request（PR）方式合并，禁止直接推送到 `main` 分支。

操作流程：

1. 从 `main` 切出新分支，命名为 `<type>/<short-description>`，例如 `feat/add-auth`、`fix/broker-crash`
2. 在新分支上完成开发并提交
3. 推送分支：`git push -u origin <branch-name>`
4. 使用 `gh pr create` 创建 PR，目标分支为 `main`
5. 不得使用 `git push origin main` 直接推送

## 当前代码基线

本仓库当前 Go 模块为：

`github.com/jinleili-zz/nsp-platform`

历史文档中出现过 `nsp-common/pkg/...`、`github.com/paic/nsp-common/pkg/...` 等路径，这些描述已不再代表当前代码结构。当前实际公共基础能力均直接位于仓库根目录包下：

- `auth`
- `trace`
- `saga`
- `logger`
- `taskqueue`

如需修改文档，必须以当前代码实现和当前导出 API 为准，不要继续沿用旧的 `nsp-common/pkg/...` 路径描述。

## 目录概览

当前与公共基础能力直接相关的目录如下：

```text
auth/
  aksk.go
  middleware.go
  nonce.go
  store.go
  auth_test.go

trace/
  context.go
  generator.go
  propagator.go
  middleware.go
  client.go
  trace_test.go

saga/
  definition.go
  engine.go
  coordinator.go
  executor.go
  poller.go
  store.go
  template.go
  jsonpath.go
  migrations/saga.sql
  saga_test.go
  saga_integration_test.go
  executor_auth_test.go

examples/
  server/
  testclient/
```

## Auth 模块

包路径：

`github.com/jinleili-zz/nsp-platform/auth`

当前实现是一个完整的 AK/SK 认证模块，包含：

- 凭证模型与内存凭证存储
- Nonce 防重放接口与内存实现
- HMAC-SHA256 请求签名与验签
- Gin 鉴权中间件

### 当前对外能力

- `Credential`
- `CredentialStore`
- `MemoryStore`
- `NonceStore`
- `MemoryNonceStore`
- `Signer` / `NewSigner`
- `Verifier` / `NewVerifier`
- `VerifierConfig`
- `AKSKAuthMiddleware`
- `CredentialFromGin`
- `ContextWithCredential`
- `CredentialFromContext`
- `ErrorToHTTPStatus`
- `HTTPStatusFromError`
- `NewSkipperByPath`
- `NewSkipperByPathPrefix`

### 当前实现细节

- 签名算法为 `HMAC-SHA256`
- 默认签名头为 `content-type;x-nsp-nonce;x-nsp-timestamp`
- 默认时间戳容忍窗口为 `5 * time.Minute`
- 默认 Nonce TTL 为 `15 * time.Minute`
- 请求体签名存在最大读取限制：`MaxRequestBodySize = 10MB`
- `MemoryNonceStore` 会启动后台清理 goroutine，并提供 `Stop()` 用于关闭
- `MemoryNonceStore` 当前实现会拒绝任何已出现过的 nonce；内部以 `2x TTL` 保存 nonce，再由后台定时清理
- Gin 中间件默认返回 JSON 错误响应，并通过 `ErrorToHTTPStatus` 进行状态码映射

### 当前请求头约定

- `Authorization`
- `X-NSP-Timestamp`
- `X-NSP-Nonce`
- `X-NSP-SignedHeaders`

`Authorization` 格式为：

`NSP-HMAC-SHA256 AK=<ak>, Signature=<signature>`

## Trace 模块

包路径：

`github.com/jinleili-zz/nsp-platform/trace`

当前实现是一个轻量级分布式链路追踪模块，不依赖 OpenTelemetry，也不依赖第三方追踪 SDK。

### 当前对外能力

- `TraceContext`
- `ContextWithTrace`
- `TraceFromContext`
- `MustTraceFromContext`
- `NewTraceID`
- `NewSpanId`
- `GetInstanceId`
- `Extract`
- `Inject`
- `InjectResponse`
- `MetadataFromContext`
- `MetadataFromTraceContext`
- `TraceFromMetadata`
- `TraceMiddleware`
- `TraceFromGin`
- `TracedClient`
- `NewTracedClient`

### 当前传播模型

使用 B3 Multi Header 命名，但不透传 `X-B3-ParentSpanId`：

- `X-B3-TraceId`
- `X-B3-SpanId`
- `X-B3-Sampled`
- `X-Request-Id`

当前行为如下：

- 入站请求优先读取 `X-B3-TraceId`
- 当 `X-B3-TraceId` 缺失时，尝试读取 `X-Request-Id`
- 两者都不可用或格式非法时，生成新的 TraceID
- 当前服务始终生成新的 SpanID
- 上游 `X-B3-SpanId` 会写入当前 `ParentSpanId`
- 响应头会写回 `X-B3-TraceId` 和 `X-Request-Id`

### 当前中间件与客户端能力

- 服务端中间件当前只提供 Gin 版本：`TraceMiddleware(instanceId string) gin.HandlerFunc`
- 当前仓库没有独立的 `net/http` 服务端 trace middleware
- 出站 HTTP 注入通过 `TracedClient` 和 `Inject` 完成，底层基于标准库 `net/http`
- `TraceMiddleware` 会同时把 `trace_id` 和 `span_id` 写入 `logger` 模块使用的 context 中
- `MetadataFromContext` / `TraceFromMetadata` 用于消息队列、异步任务等非 HTTP 场景的 trace 透传

## Saga 模块

包路径：

`github.com/jinleili-zz/nsp-platform/saga`

当前实现是嵌入式 SAGA 分布式事务 SDK，以后台 goroutine 方式运行在业务进程内，依赖 PostgreSQL 持久化。

### 当前代码结构

- `definition.go`：事务定义、步骤定义、Builder
- `engine.go`：引擎入口，对外暴露 `NewEngine`、`Start`、`Stop`、`Submit`、`Query`
- `coordinator.go`：状态机驱动与事务协调
- `executor.go`：HTTP 执行器，支持同步步骤、异步步骤、补偿、可选 AK/SK 签名
- `poller.go`：异步步骤轮询
- `store.go`：PostgreSQL 持久化与分布式锁
- `template.go`：模板渲染
- `jsonpath.go`：简单 JSONPath 提取
- `migrations/saga.sql`：建表与增量字段迁移脚本
- `observer/reader.go`：只读观测查询层，供 CLI/TUI 或后续观测面复用
- `cmd/sagactl/main.go`：SAGA 只读终端观测命令

### 当前对外能力

- `StepType`
- `TxStatus`
- `StepStatus`
- `Step`
- `Transaction`
- `PollTask`
- `SagaDefinition`
- `SagaBuilder`
- `NewSaga`
- `Config`
- `DefaultConfig`
- `Engine`
- `NewEngine`
- `(*Engine).Start`
- `(*Engine).Stop`
- `(*Engine).Submit`
- `(*Engine).Query`
- `(*Engine).Store`
- `(*Engine).DB`
- `TransactionStatus`
- `StepStatusView`
- `Store`
- `PostgresStore`
- `NewPostgresStore`
- `observer.DefaultLimit`
- `observer.ListFilter`
- `observer.ListResult`
- `observer.TransactionSummary`
- `observer.TransactionDetail`
- `observer.StepDetail`
- `observer.PollTaskDetail`
- `observer.Reader`
- `observer.NewReader`
- `RenderTemplate`
- `ExtractByPath`
- `IsStepHTTPSuccess`

### 当前实现细节

- `NewEngine` 当前签名为 `NewEngine(cfg *Config) (*Engine, error)`
- `Config` 包含 `CredentialStore auth.CredentialStore`，用于给步骤出站请求执行可选 AK/SK 签名
- `Config` 还包含可选 `Logger logger.Logger`；未显式注入时，`saga` 运行时日志默认走 `logger.Platform()`，并在后台协调/轮询路径上优先从事务 payload 的 `_trace_id`、`_span_id` 重建 trace 上下文
- `Step` 当前包含 `AuthAK string` 字段；当非空时，执行器会通过 `CredentialStore` 查凭证并对请求进行 `NSP-HMAC-SHA256` 签名
- `Engine.Submit` 在配置了 `CredentialStore` 时，会对步骤中的 `AuthAK` 做 best-effort fail-fast 校验
- `IsStepHTTPSuccess(statusCode int, body []byte)` 是统一的步骤 HTTP 成功判断函数；仅当 HTTP `2xx` 且响应体 JSON 顶层 `code == "0"`（兼容字符串 `"0"` 和数字 `0`）时判定成功
- `SagaBuilder` 除 `AddStep` 外，还支持 `WithTimeout` 和 `WithPayload`
- `SagaDefinition` 当前包含 `Payload map[string]any`
- `Engine.Submit` 会将调用方 context 中的 trace 信息写入事务 payload：`_trace_id`、`_span_id`
- Store 接口当前不仅包含基础 CRUD，还包含：
  - `CreateTransactionWithSteps`
  - `ClaimTransaction`
  - `ReleaseTransaction`
  - `UpdateTransactionStatusCAS`
  - 带实例锁与租约参数的恢复/超时扫描接口
- 当前多实例安全实现依赖：
  - `saga_transactions.locked_by`
  - `saga_transactions.locked_until`
  - `saga_poll_tasks.locked_by`
  - `saga_poll_tasks.locked_until`
- 数据库脚本位于 `saga/migrations/saga.sql`
- `observer.Reader` 只执行只读查询，不会获取执行锁，也不会写入任何 `saga_*` 表
- `cmd/sagactl` 当前提供 `list`、`failed`、`show`、`watch` 四个子命令
- `cmd/sagactl` 支持通过 `--dsn` 或环境变量 `SAGA_OBSERVER_DSN` 指定只读 PostgreSQL 连接串
- `cmd/sagactl watch` 当前采用 ANSI 清屏自动刷新，而不是完整 TUI 框架

### 当前数据库要点

当前 `saga/migrations/saga.sql` 除基础三张表外，还包含以下已落地扩展：

- `saga_transactions` 增加 `locked_by`、`locked_until`
- `saga_steps` 增加 `auth_ak`
- 所有建表和索引语句均使用 `IF NOT EXISTS`
- 迁移末尾包含 `ALTER TABLE ... ADD COLUMN IF NOT EXISTS ...` 兼容已有库

## 示例与中间件顺序

当前示例服务位于：

- `examples/server`
- `examples/testclient`

`examples/server/main.go` 中当前中间件顺序为：

1. `middleware.GinRecovery()`
2. `trace.TraceMiddleware(instanceId)`
3. `middleware.GinLogger()`
4. `auth.AKSKAuthMiddleware(...)`

如需调整文档中的接入示例，请以此顺序和当前导入路径为准。

## 测试约定

当前测试入口：

- `go test ./auth`
- `go test ./trace`
- `go test ./saga`

说明：

- `auth`、`trace` 主要是单元测试
- `saga` 依赖真实 PostgreSQL，测试前需要设置 `TEST_DSN`
- 凡是测试或示例依赖 PostgreSQL、Redis 等外部服务时，相关用户名、密码、主机、端口、DSN、URL 一律从环境变量读取，不得在测试代码、脚本或命令示例中写死
- PostgreSQL 连接信息统一通过类似 `TEST_DSN` / `SAGA_OBSERVER_DSN` 这类环境变量传入；Redis 连接信息统一通过类似 `REDIS_ADDR` 的环境变量传入
- 数据库初始化脚本使用 `saga/migrations/saga.sql`

示例：

```bash
TEST_DSN="$TEST_DSN" go test ./saga
```

如果运行环境对默认 Go build cache 目录有写限制，可显式指定临时缓存目录，例如：

```bash
GOCACHE=/tmp/go-build TEST_DSN="$TEST_DSN" go test ./saga
```

## 文档维护要求

后续修改以下内容时，必须同步更新对应文档，不得只改代码不改说明：

- 导出类型、函数、方法签名
- 包路径
- Header 规范、状态流转、错误语义
- 数据库表结构与迁移脚本
- 示例代码中的导入路径和中间件顺序

最低要求：

- `AGENTS.md`
- `docs/auth.md` / `docs/modules/auth.md`
- `docs/trace.md` / `docs/modules/trace.md`
- `docs/saga.md` / `docs/modules/saga.md`
- `saga/README.md`

如果文档内容与代码冲突，一律以当前代码实现为准，并立即修正文档。
