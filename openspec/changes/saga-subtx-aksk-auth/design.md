## Context

Saga 模块通过 `Executor` 发送三类 HTTP 请求：forward action、compensation action、poll。目前所有请求均不携带身份认证信息。`auth` 包已实现完整的 NSP-HMAC-SHA256 签名方案（`auth.Signer`），以及凭证存储接口（`auth.CredentialStore`）和内存实现（`auth.MemoryStore`），可直接复用。

关键的执行路径：`Engine.Submit` 将 `SagaDefinition` 的 Steps 写入 `saga_steps` 表后，原始的内存对象不再被使用。此后 coordinator（恢复逻辑）和 poller（异步轮询）均通过 `store.GetSteps` / `store.GetStep` 从 DB 读取步骤来驱动执行。因此 `AuthAK` **必须被持久化到 DB** 并在读取时还原。而 SK 无需持久化——执行时通过 `auth.CredentialStore.GetByAK(ak)` 从内存凭证中心运行时查询即可。

`Executor` 持有 `*http.Client`，在发送请求前构造 `*http.Request`，插入签名的最佳时机是 `Do(req)` 调用之前。

## Goals / Non-Goals

**Goals:**
- `Step` 支持可选 `AuthAK` 配置字段；非空时启用签名，为空时不鉴权
- AK 随步骤持久化到 `saga_steps` 表，SK 不落盘，通过 `auth.CredentialStore` 运行时查询
- `Executor` 在发送 action / compensate / poll 请求前，若 Step 配置了 AuthAK，通过 CredentialStore 查出 SK，自动调用 `auth.Signer.Sign` 签名
- AuthAK 为空的 Step 行为与现有完全一致（零破坏）
- 签名失败（包括 AK 查不到凭证）为 fatal 错误（不重试）
- Poller 中 Poll 签名失败作为终态错误处理，不进行瞬态重试

**Non-Goals:**
- 不支持 Bearer Token / OAuth 等其他认证方式（可未来扩展）
- 不实现 AK/SK 轮换机制（但本方案天然支持运行时凭证更新）
- 不修改 auth 包本身

## Decisions

### D1：AuthAK 字段放在 `Step` 上，而非 `ExecutorConfig`

**决策**：在 `Step` 结构体新增 `AuthAK` 字段（string 类型，可选），粒度为单步骤。

**理由**：不同步骤可能调用不同下游服务，各自持有不同凭据。全局 `ExecutorConfig` 级别的配置无法满足多租户或多服务场景，且与现有的步骤自描述设计一致（URL、method 都在步骤上配置）。同一事务内，部分步骤需要鉴权、部分不需要，通过 Step 级 AuthAK 字段自然支持。

**备选方案**：在 `ExecutorConfig` 加全局 AK → 无法支持步骤间不同凭据，灵活性不足，排除。

---

### D2：只存 AK，SK 通过 `auth.CredentialStore` 运行时查询

**决策**：`saga_steps` 表只新增 `auth_ak` 一列。SK 不入库，执行时 Executor 通过注入的 `auth.CredentialStore.GetByAK(ak)` 查出完整凭证（含 SK），再构造 Signer 签名。

**理由**：
- SK 不落盘，彻底消除 DB 明文泄露风险（包括备份、慢查询日志等场景）
- 复用现有 `auth.CredentialStore` 接口和 `MemoryStore` 实现，不引入新的凭证管理机制
- 天然支持凭证轮换：运行中通过 `MemoryStore.Add()` 更新 SK，后续步骤自动使用新 SK
- DB 改动量减半：只加 1 列（vs 原方案 2 列），Store 层每处改动少 1 个字段
- 消除"半配置"问题：只有 AuthAK 一个字段，不存在"只填了 AK 没填 SK"的歧义

**备选方案**：AK/SK 同时存入 DB → SK 明文入库，需要后续引入应用层加密（AES-GCM + envelope encryption），复杂度高，且无法利用现有 CredentialStore 架构，排除。

**约束**：凭证必须在 Engine 启动前加载到 CredentialStore。对 MemoryStore 来说，即服务启动时先加载凭证，再启动 Engine——这在实际使用中是自然的初始化顺序。

---

### D3：Executor 注入 `auth.CredentialStore`，而非直接构造 Signer

**决策**：`Executor` 新增 `credStore auth.CredentialStore` 字段（可选），通过 `NewExecutor` 参数注入。签名时调用 `credStore.GetByAK(ak)` 获取凭证，再 `auth.NewSigner(cred.AccessKey, cred.SecretKey).Sign(req)`。

**理由**：`Executor` 只知道 AK（来自 DB），需要一个查询 SK 的途径。`CredentialStore` 是 auth 包已有的接口，注入它比在 Step 上存 SK 更干净。credStore 为 nil 时，所有步骤都不签名，完全兼容现有行为。

**备选方案**：在 Step 结构体上同时存 AuthAK + AuthSK → 回到 SK 入库方案，排除（见 D2）。

---

### D4：签名时机：在 `client.Do(req)` 前统一签名

**决策**：在 `ExecuteStep`、`ExecuteAsyncStep`、`CompensateStep`、`Poll` 四个方法中，构造完 `req` 并设置完所有业务 header 后、`client.Do(req)` 前，插入签名调用。

**理由**：`auth.Signer.Sign` 会读取并重置 body，必须在 body 已设置之后调用；同时签名要覆盖所有业务 header（Content-Type 等），必须在 header 设置完毕后调用。

---

### D5：签名失败的处理

**决策**：签名失败（凭证查不到、`signer.Sign` 返回错误）时，直接返回 fatal 错误（不重试），因为签名失败属于配置级错误，重试无意义。

**理由**：AK 不存在于 CredentialStore、凭证被禁用、nonce 生成失败、body 超限等都是确定性错误，重试只会浪费资源。

---

### D6：Poller 中 Poll 签名失败作为终态错误

**决策**：在 `poller.processPollTask` 中，当 `Poll` 返回签名错误时，走 `handlePollFailure` 路径（标记 step failed → 删除 poll task → 通知 coordinator 触发补偿），而非 `releasePollTask`（释放锁等待下次重试）。

**理由**：当前 `processPollTask` 对所有 `Poll` 错误一律走 `releasePollTask`（瞬态重试），但签名失败是确定性的，重试永远不会成功。若不区分处理，事务会永远卡在 `polling` 状态，永远不会触发补偿。通过 `ErrSigningFailed` 哨兵错误或 `IsSigningError(err)` 辅助函数区分签名错误和瞬态 HTTP 错误。

---

### D7：`Engine.Submit` 可选 fail-fast 校验

**决策**：在 `Engine.Submit()` 中，若 CredentialStore 可用，对每个 AuthAK 非空的 Step 调用 `credStore.GetByAK(ak)` 校验凭证是否存在且启用。校验失败时返回错误，不创建事务。

**理由**：把 AK 无效的错误提前到提交阶段，比等到异步执行时才发现更易调试。`Build()` 无法做此校验（不持有 CredentialStore），`Submit()` 是最早的可校验时机。

## Risks / Trade-offs

- **CredentialStore 可用性依赖**：执行时 CredentialStore 必须包含对应 AK 的凭证。若凭证在 Saga 执行期间被删除或禁用，后续步骤签名失败，触发补偿。这实际上是合理的安全行为——凭证被撤销就应该停止使用。
- **body 被 Sign 读取并重置**：`auth.Signer.Sign` 内部读取并重置 body（通过 `NopCloser`），若 body 超过 10MB 会报错。→ 缓解：Saga payload 通常远小于 10MB；`Sign` 已有 `ErrBodyTooLarge` 保护。
- **Store 方法改动范围**：需同步修改 6 处 INSERT/SELECT/Scan 路径（每处只加 1 个字段）。→ 缓解：task 中逐一明确列出，测试验证 round-trip（写入再读出）后 AuthAK 一致。

## Migration Plan

1. 运行 `saga/migrations/saga.sql` 中新增的 ALTER TABLE 语句，为线上 `saga_steps` 表添加一列（`TEXT NOT NULL DEFAULT ''` 保证存量行兼容）。
2. 部署新代码，`Engine.Config` 中配置 `CredentialStore`（如 `auth.MemoryStore`），在启动时加载凭证。
3. 存量步骤 `auth_ak = ''`，Executor 跳过签名，行为与变更前完全一致。
4. 新增需要鉴权的步骤时，填入 `AuthAK` 即可。
5. 回滚：回滚代码，新列对旧代码无副作用（SELECT 不包含新列时忽略）；如需清理列，单独执行 DROP COLUMN（生产环境需评估）。

## Open Questions

- AK/SK 轮换和密钥生命周期管理超出本次范围，可作为后续独立 change。但本方案通过 CredentialStore 运行时查询 SK，天然支持在不停机的情况下更新凭证。
