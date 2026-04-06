## Why

Saga 模块在调用子事务远端 URL（action、compensate、poll）时，目前只注入 trace 和幂等 header，没有身份认证机制。对于需要鉴权的下游服务（如内部微服务、第三方平台），调用方无法通过现有配置传递 AK/SK 凭据，只能在业务层绕过或硬编码，破坏了模块的封装性和安全性。

## What Changes

- 在 `Step` 结构体上新增 `AuthAK` 字段：非空时启用签名，为空表示不鉴权。SK 不在 Step 中存储，而是在执行时通过 `auth.CredentialStore.GetByAK(ak)` 从凭证中心运行时查询。
- `saga_steps` 表新增 `auth_ak` 一列（`TEXT NOT NULL DEFAULT ''`），并提供对应的 ALTER TABLE 迁移脚本。SK 不落盘，彻底消除明文泄露风险。
- Store 层（`CreateSteps`、`CreateTransactionWithSteps`、`GetSteps`、`GetStep`、`scanStep`、`scanStepRow`）同步更新 INSERT / SELECT / Scan 以持久化并还原 AK。
- `Executor` 新增 `auth.CredentialStore` 依赖（可选，nil 时不支持签名）。在发送 action、compensate、poll 等 HTTP 请求时，若 Step 配置了 AuthAK，则通过 CredentialStore 查出 SK，使用 `auth.Signer` 对请求签名后再发送。
- 签名逻辑复用现有 `auth` 包（`auth.NewSigner` / `signer.Sign`），不重复实现。
- `Engine.Config` 新增可选的 `CredentialStore` 字段，透传到 Executor。
- `Engine.Submit()` 新增可选的 fail-fast 校验：若 Step 配置了 AuthAK 且 CredentialStore 可用，提交时即检查 AK 是否存在。
- Poller 中 Poll 签名失败作为终态错误处理（标记 step failed + 删除 poll task + 通知 coordinator），不进行瞬态重试。
- AuthAK 为空的 Step 行为与当前完全一致，**向后兼容，无破坏性变更**。

## Capabilities

### New Capabilities

- `saga-step-aksk-auth`: Saga 子事务步骤（action/compensate/poll）支持配置 AK 认证，Executor 在发出 HTTP 请求前通过 CredentialStore 查出 SK 并自动签名。

### Modified Capabilities

<!-- 无现有 spec 级别的行为变更 -->

## Impact

- **修改文件**：
  - `saga/definition.go`（扩展 `Step` 结构体新增 `AuthAK` 字段）
  - `saga/store.go`（INSERT / SELECT / scanStep / scanStepRow 追加 `auth_ak`）
  - `saga/executor.go`（注入 `auth.CredentialStore` + 签名逻辑）
  - `saga/engine.go`（Config 新增 `CredentialStore` 字段，透传到 Executor）
  - `saga/poller.go`（Poll 签名错误走终态失败路径）
  - `saga/migrations/saga.sql`（新增 `auth_ak` 列及 ALTER TABLE 脚本）
- **新增依赖**：`saga` 包引入 `auth` 包（同 module，无外部依赖新增）
- **API 兼容性**：纯增量扩展，现有 Step 定义零改动即可兼容；DB 迁移使用 DEFAULT '' 确保存量行安全
- **测试**：需为含 AuthAK 的步骤添加单元测试；Submit fail-fast 校验需验证错误输出
