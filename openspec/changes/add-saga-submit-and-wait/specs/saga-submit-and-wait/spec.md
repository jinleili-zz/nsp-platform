## ADDED Requirements

### Requirement: Engine 支持阻塞式提交并等待事务终态
`Engine` SHALL 提供 `SubmitAndWait(ctx, def)` 能力。该方法 MUST 先按现有 `Submit` 语义创建并提交事务，再等待事务进入终态后返回。事务终态仅包括 `succeeded` 和 `failed`。

#### Scenario: 事务提交阶段失败
- **WHEN** `Submit` 本身因定义校验、数据库写入或凭证校验失败而返回错误
- **THEN** `SubmitAndWait` MUST 返回空 `txID` 和 `nil` `TransactionStatus`
- **THEN** 返回的错误 MUST 为 `Submit` 的原始错误，而不是 `ErrTransactionFailed`、`ErrTransactionNotFound` 或 `ErrTransactionDisappeared`

#### Scenario: 同步步骤事务成功后返回终态
- **WHEN** 调用方使用 `SubmitAndWait` 提交仅包含同步步骤且最终成功的事务
- **THEN** 方法 MUST 在事务状态变为 `succeeded` 后返回
- **THEN** 返回值 MUST 包含非空 `txID`
- **THEN** 返回的 `TransactionStatus` MUST 标识事务成功且包含各步骤的最终状态概览

#### Scenario: 异步步骤事务成功后返回终态
- **WHEN** 调用方使用 `SubmitAndWait` 提交包含异步步骤并通过轮询最终成功的事务
- **THEN** 方法 MUST 等待异步步骤完成和事务状态变为 `succeeded` 后再返回
- **THEN** 返回的 `TransactionStatus` MUST 反映异步步骤的最终成功状态

### Requirement: SubmitAndWait 必须区分事务失败、事务消失、调用方等待结束和基础设施错误
`SubmitAndWait` MUST 将事务终态失败、事务在等待期间消失、调用方 `ctx` 取消或超时、以及等待期间的基础设施错误区分开。事务最终失败时，方法 MUST 返回可识别的事务失败错误；事务消失时，方法 MUST 返回可识别的事务消失错误；调用方等待提前结束时，方法 MUST 返回调用方上下文错误，并保留最近一次可见的事务状态信息。

#### Scenario: 事务执行失败并完成补偿
- **WHEN** 已提交事务中的某个步骤失败并使事务进入补偿流程
- **THEN** `SubmitAndWait` MUST 继续等待直到事务进入最终 `failed`
- **THEN** `SubmitAndWait` MUST NOT 在事务仅处于 `compensating` 时提前返回
- **THEN** 返回的错误 MUST 能让调用方区分这是事务最终失败，而不是事务消失、提交失败、基础设施错误或 `ctx` 取消

#### Scenario: 调用方上下文超时先于事务终态
- **WHEN** 调用方传入的 `ctx` 在事务进入终态前超时或被取消
- **THEN** `SubmitAndWait` MUST 立刻结束等待并返回 `ctx` 对应的错误
- **THEN** 已提交的 saga 事务 MUST 保持原有执行语义，不得因为等待方返回而被强制终止
- **THEN** 若方法已观测到事务状态，返回值 SHOULD 包含最近一次成功查询到的事务状态概览

#### Scenario: 已提交事务在等待期间消失
- **WHEN** `Submit` 已成功返回 `txID`，但 `SubmitAndWait` 后续 `Query` 返回 `ErrTransactionNotFound`
- **THEN** 方法 MUST 返回可与基础设施错误区分的事务消失错误
- **THEN** 方法 MUST NOT 以空状态或成功结果静默返回

### Requirement: SubmitAndWait 必须定义等待期间的 Query 异常语义
`SubmitAndWait` MUST 对等待期间的 `Query` 异常给出确定行为。对于普通 `Query` 错误，方法 MUST 采用有限重试或退避策略；若错误持续存在到超过实现定义的阈值或 `ctx` 结束，则 MUST 返回基础设施错误。对于 `Query` 返回 `ErrTransactionNotFound` 的情况，方法 MUST 将其视为事务消失异常而不是成功。

#### Scenario: 等待期间出现暂时性 Query 错误
- **WHEN** `SubmitAndWait` 在等待终态期间遇到暂时性的 `Query` 错误，且后续查询恢复正常
- **THEN** 方法 MUST 继续等待，而不是立即把该事务判定为失败
- **THEN** 若事务随后达到终态，方法 MUST 按终态语义返回

### Requirement: Query 必须显式区分事务不存在
`Query` MUST 在事务不存在时返回可识别的 `ErrTransactionNotFound`，而不是以 `nil` 状态和 `nil` 错误表达“未找到”。

#### Scenario: 查询一个不存在的事务
- **WHEN** 调用方对不存在的 `txID` 调用 `Query`
- **THEN** 方法 MUST 返回 `nil` `TransactionStatus`
- **THEN** 返回的错误 MUST 可被 `errors.Is(err, saga.ErrTransactionNotFound)` 识别

#### Scenario: 等待期间 Query 持续失败
- **WHEN** `SubmitAndWait` 在等待终态期间持续遇到 `Query` 错误，直到超过实现定义的重试阈值且 `ctx` 仍有效
- **THEN** 方法 MUST 返回该基础设施错误
- **THEN** 若方法此前已观测到事务状态，返回值 SHOULD 包含最近一次成功查询到的事务状态概览

### Requirement: 调用方等待超时与 saga 事务超时必须解耦
`SubmitAndWait` 的等待生命周期 MUST 由调用方 `ctx` 控制；`SagaDefinition.TimeoutSec` / `SagaBuilder.WithTimeout` MUST 继续只控制 saga 事务自身的业务超时与补偿触发。两者 MUST NOT 互相替代。

#### Scenario: saga 事务超时后继续等待补偿结果
- **WHEN** 事务定义配置了 `WithTimeout` 且事务超时后进入补偿流程
- **THEN** 在调用方 `ctx` 仍有效时，`SubmitAndWait` MUST 继续等待直到事务进入最终 `failed` 状态
- **THEN** 返回结果 MUST 反映补偿后的终态，而不是在进入 `compensating` 时提前返回

#### Scenario: 调用方等待窗口短于事务业务超时
- **WHEN** 调用方 `ctx` 的超时时间短于事务定义中的业务超时时间
- **THEN** `SubmitAndWait` MUST 在 `ctx` 超时后返回调用方上下文错误
- **THEN** 事务后续仍 MAY 由后台 engine 继续执行并在稍后通过 `Query` 观察到终态

### Requirement: SubmitAndWait 的文档必须反映活跃执行者和 pending 边界
`SubmitAndWait` 的文档和示例 MUST 说明等待依赖至少一个连接同一事务存储并正在运行的 engine 实例来推进事务，而不是强制要求当前调用实例已启动后台执行。文档还 MUST 说明当前实现中存在“事务已持久化但未被执行者接手”而长期停留在 `pending` 的边界。

#### Scenario: 由其他实例推进事务时等待成功
- **WHEN** 调用 `SubmitAndWait` 的当前实例未实际推进事务，但另一个连接同一存储的运行中 engine 实例完成了该事务
- **THEN** `SubmitAndWait` MUST 仍可通过持久化状态观测到终态并按终态语义返回

#### Scenario: 没有活跃执行者时调用方结束等待
- **WHEN** 系统中没有任何活跃 engine 实例推进已提交事务，且调用方 `ctx` 先结束
- **THEN** `SubmitAndWait` MUST 返回调用方上下文错误
- **THEN** 文档 MUST 明确说明该行为，而不是暗示方法一定会自行完成事务执行

#### Scenario: 提交后事务长期停留 pending
- **WHEN** 事务已通过 `Submit` 持久化，但因当前实现中的派发/恢复边界未被执行者及时接手而长时间保持 `pending`
- **THEN** `SubmitAndWait` MUST NOT 将该状态误判为成功
- **THEN** `SubmitAndWait` MUST 仅在事务进入终态、事务消失、发生基础设施错误或调用方 `ctx` 结束时返回
