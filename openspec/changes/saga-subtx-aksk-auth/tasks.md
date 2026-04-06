## 1. 数据库迁移

- [ ] 1.1 在 `saga/migrations/saga.sql` 的 `saga_steps` 表定义中新增 `auth_ak TEXT NOT NULL DEFAULT ''` 列，并附加对应 COMMENT
- [ ] 1.2 在同文件末尾追加在线迁移语句：`ALTER TABLE saga_steps ADD COLUMN IF NOT EXISTS auth_ak TEXT NOT NULL DEFAULT '';`

## 2. 扩展 Step 数据模型

- [ ] 2.1 在 `saga/definition.go` 的 `Step` 结构体中新增 `AuthAK string` 字段，并添加注释说明"非空时启用 NSP-HMAC-SHA256 签名，SK 通过 auth.CredentialStore 运行时查询"

## 3. 持久化：Store 层写入路径

- [ ] 3.1 更新 `CreateTransactionWithSteps` 中的 `stepQuery` INSERT 语句：在字段列表和参数占位符列表末尾追加 `auth_ak` 字段及其对应占位符，并在 `stmt.ExecContext` 的参数列表末尾追加 `step.AuthAK`
- [ ] 3.2 更新 `CreateSteps` 中的 `query` INSERT 语句：同上，保持与 3.1 一致

## 4. 持久化：Store 层读取路径

- [ ] 4.1 更新 `GetSteps` 中的 SELECT 查询：在 SELECT 列表末尾追加 `auth_ak`
- [ ] 4.2 更新 `GetStep` 中的 SELECT 查询：同上
- [ ] 4.3 更新 `scanStep` 函数：在 `rows.Scan(...)` 参数列表末尾追加 `&step.AuthAK`
- [ ] 4.4 更新 `scanStepRow` 函数：在 `row.Scan(...)` 参数列表末尾追加 `&step.AuthAK`

## 5. 注入 CredentialStore 依赖

- [ ] 5.1 在 `saga/executor.go` 的 `Executor` 结构体中新增 `credStore auth.CredentialStore` 字段；更新 `NewExecutor` 函数签名，新增可选参数 `credStore auth.CredentialStore`（nil 表示不支持签名）
- [ ] 5.2 在 `saga/engine.go` 的 `Config` 结构体中新增 `CredentialStore auth.CredentialStore` 字段；`NewEngine` 中将其透传到 `NewExecutor`

## 6. 实现 Executor 签名逻辑

- [ ] 6.1 在 `saga/executor.go` 中添加辅助方法 `(e *Executor) signRequestIfNeeded(ctx context.Context, step *Step, req *http.Request) error`：若 `step.AuthAK == ""` 或 `e.credStore == nil`，直接返回 nil；否则调用 `e.credStore.GetByAK(ctx, step.AuthAK)` 查出凭证，凭证不存在或 `Enabled==false` 时返回 fatal 错误；凭证有效时调用 `auth.NewSigner(cred.AccessKey, cred.SecretKey).Sign(req)`
- [ ] 6.2 在 `ExecuteStep` 方法中，于所有业务 header 设置完毕、`client.Do(req)` 调用之前，插入 `signRequestIfNeeded` 调用；签名失败时先调用 `e.store.UpdateStepStatus(..., StepStatusFailed, ...)` 再直接返回 `ErrStepFatal`（不经过 `handleHTTPError`，保持 RetryCount 不变）
- [ ] 6.3 在 `ExecuteAsyncStep` 方法中同样插入 `signRequestIfNeeded` 调用，处理方式与 6.2 一致
- [ ] 6.4 在 `CompensateStep` 方法的每次重试循环中，在 `client.Do(req)` 之前调用 `signRequestIfNeeded`；签名失败时直接 `break` 退出重试循环并以 `ErrCompensationFailed` 返回
- [ ] 6.5 在 `Poll` 方法中，于 `client.Do(req)` 之前调用 `signRequestIfNeeded`；签名失败时包装为哨兵错误 `ErrSigningFailed` 返回
- [ ] 6.6 在 `saga/executor.go` 中新增哨兵错误 `ErrSigningFailed` 和导出函数 `IsSigningError(err error) bool`

## 7. Poller 签名错误终态处理

- [ ] 7.1 在 `saga/poller.go` 的 `processPollTask` 方法中，修改 `Poll` 错误处理路径：当 `IsSigningError(err)` 为 true 时，走 `handlePollFailure` 路径（标记步骤为 failed、删除 poll task、通知 coordinator 触发补偿），而非当前的 `releasePollTask`（释放锁等待下次重试）；非签名错误保持现有的瞬态重试行为不变

## 8. Submit fail-fast 校验

- [ ] 8.1 在 `Engine.Submit()` 中，遍历 steps，若 `step.AuthAK != ""` 且 `e.config.CredentialStore != nil`，调用 `credStore.GetByAK(ctx, step.AuthAK)` 校验凭证存在且 `Enabled==true`，校验失败时返回错误，不创建事务

## 9. 补充测试

- [ ] 9.1 新建 `saga/executor_auth_test.go`，添加单元测试：使用 `httptest.NewServer` 验证当 Step 配置 AuthAK 且 CredentialStore 中有对应凭证时，action 请求中包含 `Authorization`、`X-NSP-Timestamp`、`X-NSP-Nonce`、`X-NSP-SignedHeaders` header
- [ ] 9.2 添加单元测试：Step AuthAK 为空时，请求中不含上述认证 header
- [ ] 9.3 添加单元测试：AuthAK 非空但 CredentialStore 中无对应凭证时，`signRequestIfNeeded` 返回 fatal 错误
- [ ] 9.4 添加单元测试：`signRequestIfNeeded` 返回错误时，`ExecuteStep` 返回 `ErrStepFatal` 且 `step.RetryCount` 不变
- [ ] 9.5 添加单元测试：补偿请求（`CompensateStep`）和轮询请求（`Poll`）在配置 AuthAK 时同样被签名
- [ ] 9.6 添加集成测试（或 store mock 测试）：验证含 AuthAK 的 Step 写入后再读出，`AuthAK` 值一致（round-trip）
- [ ] 9.7 添加单元测试：`poller.processPollTask` 在 `Poll` 返回签名错误时，步骤被标记为 failed 且 poll task 被删除（而非释放锁重试）
- [ ] 9.8 添加单元测试：`Engine.Submit()` 对 AuthAK 不存在于 CredentialStore 中的步骤返回错误
