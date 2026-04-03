## Context

Saga 模块通过 `Executor` 发送三类 HTTP 请求：forward action、compensation action、poll。目前所有请求均不携带身份认证信息。`auth` 包已实现完整的 NSP-HMAC-SHA256 签名方案（`auth.Signer`），可直接复用。

关键的执行路径：`Engine.Submit` 将 `SagaDefinition` 的 Steps 写入 `saga_steps` 表后，原始的内存对象不再被使用。此后 coordinator（恢复逻辑）和 poller（异步轮询）均通过 `store.GetSteps` / `store.GetStep` 从 DB 读取步骤来驱动执行。因此 `AuthAK`/`AuthSK` **必须被持久化到 DB** 并在读取时还原，否则工作进程拿到的 Step 永远是空凭据。

`Executor` 持有 `*http.Client`，在发送请求前构造 `*http.Request`，插入签名的最佳时机是 `Do(req)` 调用之前。

## Goals / Non-Goals

**Goals:**
- `Step` 支持可选 AK/SK 配置字段；半配置（只填一个）在 `Build()` 阶段 fail-fast
- AK/SK 随步骤持久化到 `saga_steps` 表，store 层完整负责写入和读取
- `Executor` 在发送 action / compensate / poll 请求前，若 Step 配置了 AK/SK，自动调用 `auth.Signer.Sign` 签名
- 两个字段均为空的 Step 行为与现有完全一致（零破坏）
- 签名失败为 fatal 错误（不重试）

**Non-Goals:**
- 不支持 Bearer Token / OAuth 等其他认证方式（可未来扩展）
- 不实现 AK/SK 存储或轮换机制
- 不修改 auth 包本身

## Decisions

### D1：AK/SK 字段放在 `Step` 上，而非 `ExecutorConfig`

**决策**：在 `Step` 结构体新增 `AuthAK` / `AuthSK` 字段（string 类型，可选），粒度为单步骤。

**理由**：不同步骤可能调用不同下游服务，各自持有不同凭据。全局 `ExecutorConfig` 级别的配置无法满足多租户或多服务场景，且与现有的步骤自描述设计一致（URL、method 都在步骤上配置）。

**备选方案**：在 `ExecutorConfig` 加全局 AK/SK → 无法支持步骤间不同凭据，灵活性不足，排除。

---

### D2：直接在 `Executor` 内部构造 `auth.Signer`，不注入接口

**决策**：在需要签名时，按需 `auth.NewSigner(step.AuthAK, step.AuthSK)` 创建 Signer 并调用 `Sign(req)`，不引入签名接口抽象。

**理由**：目前只有一种签名方案（NSP-HMAC-SHA256），过早抽象会增加复杂度。`auth.Signer` 已经对 `*http.Request` 操作，测试中可用 `httptest` 验证 header，无需 mock 签名器。

**备选方案**：定义 `RequestSigner interface { Sign(*http.Request) error }` 并注入 → 仅当需要多种签名方案时有价值，当前过度设计，排除。

---

### D3：签名时机：在 `client.Do(req)` 前统一签名

**决策**：在 `ExecuteStep`、`ExecuteAsyncStep`、`CompensateStep`、`Poll` 四个方法中，构造完 `req` 并设置完所有业务 header 后、`client.Do(req)` 前，插入签名调用。

**理由**：`auth.Signer.Sign` 会读取并重置 body，必须在 body 已设置之后调用；同时签名要覆盖所有业务 header（Content-Type 等），必须在 header 设置完毕后调用。

---

### D4：签名失败的处理

**决策**：签名失败（`signer.Sign` 返回错误）时，直接返回 `ErrStepFatal`（不重试），因为签名失败属于配置级错误，重试无意义。

**理由**：nonce 生成失败或 body 读取超限（> 10MB）是确定性错误，重试只会浪费资源。

---

### D5：AK/SK 持久化到 `saga_steps` 表

**决策**：在 `saga_steps` 表新增 `auth_ak VARCHAR(128) NOT NULL DEFAULT ''` 和 `auth_sk VARCHAR(128) NOT NULL DEFAULT ''` 两列。所有 Store 方法（`CreateSteps`、`CreateTransactionWithSteps` 的 INSERT，`GetSteps`/`GetStep` 的 SELECT，以及 `scanStep`/`scanStepRow` 的 Scan）均需同步更新。

**理由**：Saga 执行路径在 `Submit` 后完全由 DB 驱动——coordinator 和 poller 通过 `GetSteps`/`GetStep` 加载步骤。若不持久化，工作进程读到的 Step 永远是空凭据，签名逻辑永远不会触发，功能实际失效。`DEFAULT ''` 确保存量行（`auth_ak = ''`、`auth_sk = ''`）被 Executor 识别为"不鉴权"，零迁移成本。

---

### D6：半配置（只填 AK 或只填 SK）在 `Build()` fail-fast

**决策**：在 `SagaBuilder.Build()` 的步骤校验循环中，检测 `(AuthAK == '') != (AuthSK == '')` 的情况，返回新的哨兵错误 `ErrStepPartialAuth`。

**理由**：把"只填了一个字段"当作"不鉴权"静默处理，会把凭据注入失误转变为静默的鉴权禁用，下游调用方收到 401 却难以定位根因。`Build()` 是用户唯一的配置入口，在此 reject 是成本最低的防线，且不影响运行时路径。

**备选方案**：在 `Executor.signRequestIfNeeded` 里检测并返回 fatal → 错误发现延迟到执行期，且无法在提交前捕获，调试成本更高，排除。

## Risks / Trade-offs

- **SK 明文存储在 DB**：`auth_sk` 列以明文存储。→ 缓解：调用方可通过 KMS/Vault 在运行时注入；DB 层可在应用层加密后存储；本模块不负责凭据生命周期管理。
- **body 被 Sign 读取并重置**：`auth.Signer.Sign` 内部读取并重置 body（通过 `NopCloser`），若 body 超过 10MB 会报错。→ 缓解：Saga payload 通常远小于 10MB；`Sign` 已有 `ErrBodyTooLarge` 保护。
- **Store 方法改动范围**：需同步修改 4 个 INSERT/SELECT/Scan 路径，遗漏任何一处都会导致读回空凭据。→ 缓解：task 中逐一明确列出，测试验证 round-trip（写入再读出）后凭据一致。

## Migration Plan

1. 运行 `saga/migrations/saga.sql` 中新增的 ALTER TABLE 语句，为线上 `saga_steps` 表添加两列（DEFAULT '' 保证存量行兼容）。
2. 部署新代码：存量步骤 `auth_ak = ''`/`auth_sk = ''`，Executor 跳过签名，行为与变更前完全一致。
3. 新增需要鉴权的步骤时，填入 `AuthAK`/`AuthSK` 即可。
4. 回滚：回滚代码，新列对旧代码无副作用（SELECT 不包含新列时忽略）；如需清理列，单独执行 DROP COLUMN（生产环境需评估）。

## Open Questions

- **SK 明文存储的安全加固（Phase 2）**：当前 `auth_sk` 列以明文存储，属于有意的 MVP 简化。后续应在 Store 层为 `auth_sk` 列增加应用层加密（AES-GCM + envelope encryption），或改为只存加密后的 ciphertext + key reference。此外，数据库备份和慢查询日志中可能泄露 SK 明文，需配合 DB 层审计策略一并考虑。此项作为后续独立 change 跟踪。
- AK/SK 轮换和密钥生命周期管理超出本次范围，可作为后续独立 change。
