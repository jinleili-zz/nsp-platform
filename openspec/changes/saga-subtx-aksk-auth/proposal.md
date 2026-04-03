## Why

Saga 模块在调用子事务远端 URL（action、compensate、poll）时，目前只注入 trace 和幂等 header，没有身份认证机制。对于需要鉴权的下游服务（如内部微服务、第三方平台），调用方无法通过现有配置传递 AK/SK 凭据，只能在业务层绕过或硬编码，破坏了模块的封装性和安全性。

## What Changes

- 在 `Step` 结构体上新增 `AuthAK` / `AuthSK` 字段：两者同时非空时启用签名，任一为空且另一非空时 `Build()` 报验证错误（fail-fast），两者均为空表示不鉴权。
- `SagaBuilder.Build()` 新增半配置检查，拒绝只设置 AK 或只设置 SK 的步骤定义。
- `saga_steps` 表新增 `auth_ak` / `auth_sk` 两列（`VARCHAR(128) NOT NULL DEFAULT ''`），并提供对应的 ALTER TABLE 迁移脚本。
- Store 层（`CreateSteps`、`CreateTransactionWithSteps`、`GetSteps`、`GetStep`、`scanStep`、`scanStepRow`）同步更新 INSERT / SELECT / Scan 以持久化并还原 AK/SK，确保 coordinator 和 poller 在从 DB 读取步骤后仍能获取到凭据。
- `Executor` 在发送 action、compensate、poll 等 HTTP 请求时，若 Step 配置了 AK/SK，则使用 `auth.Signer` 对请求签名后再发送。
- 签名逻辑复用现有 `auth` 包（`auth.NewSigner` / `signer.Sign`），不重复实现。
- 两个 auth 字段均为空的 Step 行为与当前完全一致，**向后兼容，无破坏性变更**。

## Capabilities

### New Capabilities

- `saga-step-aksk-auth`: Saga 子事务步骤（action/compensate/poll）支持配置 AK/SK 认证，Executor 在发出 HTTP 请求前自动签名。

### Modified Capabilities

<!-- 无现有 spec 级别的行为变更 -->

## Impact

- **修改文件**：
  - `saga/definition.go`（扩展 `Step` 结构体 + `Build()` 半配置检查）
  - `saga/store.go`（INSERT / SELECT / scanStep / scanStepRow 全部同步）
  - `saga/executor.go`（注入签名逻辑）
  - `saga/migrations/saga.sql`（新增 `auth_ak` / `auth_sk` 列及 ALTER TABLE 脚本）
- **新增依赖**：`saga` 包引入 `auth` 包（同 module，无外部依赖新增）
- **API 兼容性**：纯增量扩展，现有 Step 定义零改动即可兼容；DB 迁移使用 DEFAULT '' 确保存量行安全
- **测试**：需为含 AK/SK 的步骤添加单元测试；`Build()` 半配置路径需要验证错误输出
