# Saga HTTP 子事务响应体 `code` 判定设计

## 背景

当前 `saga` 模块对 HTTP 子事务的成功判定仅依赖 HTTP 状态码：`2xx` 视为成功，其他状态码视为失败。

这与当前内部服务约定不一致。现有服务在“请求已被接收”时通常统一返回 `200`，实际业务成功与否需要根据响应体中的 `code` 判断：

- `code == "0"`：业务成功
- `code != "0"`：业务失败

如果 `saga` 继续只按 HTTP `2xx` 判断，前向 action、补偿 action、异步 poll 都会把“请求收到但业务失败”的响应误判为成功。

## 目标

- 为 `Action`、`Compensate`、`Poll` 三类 HTTP 子事务统一增加响应体 `code` 校验。
- 保持现有同步步骤、补偿步骤、异步轮询步骤的状态流转模型不变。
- 保持异步 `Poll` 的业务终态判断仍由 `PollSuccessPath` / `PollFailurePath` 控制。
- 当前版本只做 SDK 内部固定规则，不暴露新的对外配置入口。

## 非目标

- 不在本次设计中增加 `saga.Config` 级或 step 级可配置判定规则。
- 不修改 `Step` 的公开字段结构。
- 不引入新的 step 状态枚举。
- 不调整现有事务协调、补偿、轮询的主流程。

## 判定模型

### 统一响应封套判定

所有 HTTP 子事务请求在进入各自业务语义判断前，先执行统一的响应封套校验。只有同时满足以下条件才视为“本次 HTTP 调用成功”：

1. HTTP 状态码在 `200-299`
2. 响应体非空
3. 响应体是合法 JSON object
4. JSON 中存在字段 `code`
5. `code` 的字符串化结果等于 `"0"`

以下情况均视为失败：

- 非 `2xx`
- `2xx` 但响应体为空
- `2xx` 但响应体不是合法 JSON
- `2xx` 但 JSON 不是 object
- `2xx` 但 JSON 中缺少 `code`
- `2xx` 且 `code != "0"`

### 按路径的业务语义

#### Action

前向 action 通过统一响应封套判定后，即视为该步骤执行成功，并继续沿用现有逻辑：

- 解析后的响应体写入 `ActionResponse`
- step 状态更新为 `succeeded`

#### Compensate

补偿 action 通过统一响应封套判定后，即视为补偿成功，并继续沿用现有逻辑：

- step 状态更新为 `compensated`

#### Poll

`Poll` 需要区分“轮询请求成功”和“异步业务终态成功”。

因此，`Poll` 的处理分两层：

1. 先执行统一响应封套判定，确认本次 poll HTTP 调用成功
2. 再用现有 `PollSuccessPath` / `PollFailurePath` 判定异步业务终态

含义如下：

- 响应未通过统一响应封套判定：本次 poll 失败
- 响应通过统一响应封套判定，且命中 `PollSuccessPath`：步骤成功
- 响应通过统一响应封套判定，且命中 `PollFailurePath`：步骤失败
- 响应通过统一响应封套判定，但未命中 success/failure：步骤继续保持 `polling`

这保证了 `{"code":"0","status":"running"}` 这类响应不会被误判为步骤成功，只会表示“轮询请求成功，但业务仍在处理中”。

## 错误语义与重试策略

本次设计不新增新的错误类型或 step 状态，统一复用现有失败路径。

### Action

统一响应封套判定失败时，按现有 step 执行失败逻辑处理：

- 仍走 `handleHTTPError`
- 如果未达到 `MaxRetry`，保持可重试语义
- 达到重试上限后，step 进入 `failed`
- 事务进入现有补偿流程

### Compensate

补偿响应未通过统一响应封套判定时，按现有补偿失败处理：

- 沿用现有指数退避和重试次数
- 重试耗尽后返回 `ErrCompensationFailed`

### Poll

poll 响应未通过统一响应封套判定时，按现有 poll 失败路径处理，不增加旁路逻辑。

当前设计不把 `code != "0"` 单独定义为“不可重试的业务失败”，原因是现阶段缺少稳定、通用的跨服务语义约束。继续复用现有重试模型更稳妥。

## 代码改动边界

### 统一响应判定入口

不要在 `ExecuteStep`、`ExecuteAsyncStep`、`CompensateStep`、`Poll` 中复制粘贴判定逻辑。

应新增一个 `saga` 内部复用的统一响应判定辅助函数，负责：

- 校验 HTTP 状态码
- 读取响应体
- 解析 JSON object
- 提取 `code`
- 判断 `code == "0"`
- 返回解析后的响应体和标准化错误

### 调用点

- `ExecuteStep`：调用统一判定函数，通过后写入 `ActionResponse` 并标记成功
- `ExecuteAsyncStep`：调用统一判定函数，通过后写入 `ActionResponse` 并进入 `polling`
- `CompensateStep`：每次补偿请求收到响应后都调用统一判定函数，通过后标记补偿成功
- `Poll`：调用统一判定函数，通过后再进入 `MatchPollResult`

## 测试范围

至少覆盖以下场景：

1. `Action` 在 `2xx + code == "0"` 时成功
2. `Action` 在 `2xx + code != "0"` 时失败，并沿用现有重试/失败语义
3. `Action` 在 `2xx + 空 body` 时失败
4. `Action` 在 `2xx + 非 JSON` 时失败
5. `Action` 在 `2xx + 缺少 code` 时失败
6. `Compensate` 在 `2xx + code != "0"` 时按现有补偿失败重试
7. `Poll` 在 `2xx + code == "0" + 业务态未完成` 时继续 `polling`
8. `Poll` 在 `2xx + code == "0" + 命中 success` 时成功
9. `Poll` 在 `2xx + code == "0" + 命中 failure` 时失败
10. `Poll` 在 `2xx + code != "0"` 时按现有 poll 失败处理

## 文档同步要求

实现时至少同步更新以下文档：

- `AGENTS.md`
- `docs/saga.md`
- `docs/modules/saga.md`
- `saga/README.md`

需要把“HTTP 子事务成功判定”从“只看 `2xx`”更新为“`2xx` 且响应体 `code == "0"`；异步 poll 还需继续匹配 `PollSuccessPath` / `PollFailurePath`”。

## 实施建议

本次改动适合拆成一个小的、聚焦的实现：

1. 先补统一响应判定辅助函数和单元测试
2. 再接入 `Action` / `ExecuteAsyncStep` / `Compensate` / `Poll`
3. 最后同步更新文档

这样可以减少行为回归面，也便于在测试里直接覆盖所有失败分支。
