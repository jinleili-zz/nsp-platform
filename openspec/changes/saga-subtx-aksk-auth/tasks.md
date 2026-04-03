## 1. 数据库迁移

- [ ] 1.1 在 `saga/migrations/saga.sql` 的 `saga_steps` 表定义中新增 `auth_ak VARCHAR(128) NOT NULL DEFAULT ''` 和 `auth_sk VARCHAR(128) NOT NULL DEFAULT ''` 两列，并附加对应 COMMENT
- [ ] 1.2 在同文件末尾追加在线迁移语句：`ALTER TABLE saga_steps ADD COLUMN IF NOT EXISTS auth_ak VARCHAR(128) NOT NULL DEFAULT ''; ALTER TABLE saga_steps ADD COLUMN IF NOT EXISTS auth_sk VARCHAR(128) NOT NULL DEFAULT '';`

## 2. 扩展 Step 数据模型与构建时校验

- [ ] 2.1 在 `saga/definition.go` 的 `Step` 结构体中新增 `AuthAK string` 和 `AuthSK string` 字段，并添加注释说明"两者均非空时启用 NSP-HMAC-SHA256 签名"
- [ ] 2.2 在同文件的 `var` 错误块中新增哨兵错误 `ErrStepPartialAuth = errors.New("step has partial auth config: both AuthAK and AuthSK must be set together")`
- [ ] 2.3 在 `SagaBuilder.Build()` 的步骤校验循环中，添加检查 `(step.AuthAK == "") != (step.AuthSK == "")` 的逻辑，满足时返回 `ErrStepPartialAuth`

## 3. 持久化：Store 层写入路径

- [ ] 3.1 更新 `CreateTransactionWithSteps` 中的 `stepQuery` INSERT 语句：在字段列表和参数占位符列表末尾追加 `auth_ak, auth_sk` 两个字段及其对应占位符，并在 `stmt.ExecContext` 的参数列表末尾追加 `step.AuthAK, step.AuthSK`
- [ ] 3.2 更新 `CreateSteps` 中的 `query` INSERT 语句：同上，保持与 3.1 一致

## 4. 持久化：Store 层读取路径

- [ ] 4.1 更新 `GetSteps` 中的 SELECT 查询：在 SELECT 列表末尾追加 `auth_ak, auth_sk`
- [ ] 4.2 更新 `GetStep` 中的 SELECT 查询：同上
- [ ] 4.3 更新 `scanStep` 函数：在 `rows.Scan(...)` 参数列表末尾追加 `&step.AuthAK, &step.AuthSK`
- [ ] 4.4 更新 `scanStepRow` 函数：在 `row.Scan(...)` 参数列表末尾追加 `&step.AuthAK, &step.AuthSK`

## 5. 实现 Executor 签名逻辑

- [ ] 5.1 在 `saga/executor.go` 中添加辅助函数 `signRequestIfNeeded(step *Step, req *http.Request) error`：若 `step.AuthAK != ""` 且 `step.AuthSK != ""`，则调用 `auth.NewSigner(step.AuthAK, step.AuthSK).Sign(req)`，否则直接返回 nil；在文件顶部 import 中添加 `github.com/jinleili-zz/nsp-platform/auth`（当前与 `trace` 包同 module，若 auth 后续迁移到 nsp-common 需同步更新 import 路径）
- [ ] 5.2 在 `ExecuteStep` 方法中，于所有业务 header 设置完毕、`client.Do(req)` 调用之前，插入 `signRequestIfNeeded` 调用；签名失败时先调用 `e.store.UpdateStepStatus(..., StepStatusFailed, ...)` 再直接返回 `ErrStepFatal`（不经过 `handleHTTPError`，保持 RetryCount 不变）
- [ ] 5.3 在 `ExecuteAsyncStep` 方法中同样插入 `signRequestIfNeeded` 调用，处理方式与 5.2 一致
- [ ] 5.4 在 `CompensateStep` 方法的每次重试循环中，在 `client.Do(req)` 之前调用 `signRequestIfNeeded`；签名失败时直接 `break` 退出重试循环并以 `ErrCompensationFailed` 返回
- [ ] 5.5 在 `Poll` 方法中，于 `client.Do(req)` 之前调用 `signRequestIfNeeded`；签名失败时直接返回错误

## 6. 补充测试

- [ ] 6.1 新建 `saga/executor_auth_test.go`，添加单元测试：使用 `httptest.NewServer` 验证当 Step 配置 AK/SK 时，action 请求中包含 `Authorization`、`X-NSP-Timestamp`、`X-NSP-Nonce`、`X-NSP-SignedHeaders` header
- [ ] 6.2 添加单元测试：Step 两个 auth 字段均为空时，请求中不含上述认证 header
- [ ] 6.3 添加单元测试：`signRequestIfNeeded` 返回错误时，`ExecuteStep` 返回 `ErrStepFatal` 且 `step.RetryCount` 不变
- [ ] 6.4 添加单元测试：补偿请求（`CompensateStep`）和轮询请求（`Poll`）在配置 AK/SK 时同样被签名
- [ ] 6.5 添加单元测试：`SagaBuilder.Build()` 对只填 `AuthAK` 或只填 `AuthSK` 的步骤返回 `ErrStepPartialAuth`
- [ ] 6.6 添加集成测试（或 store mock 测试）：验证含 AK/SK 的 Step 写入后再读出，`AuthAK`/`AuthSK` 值一致（round-trip）
