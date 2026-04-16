## Context

当前 `Engine.Submit` 只负责持久化事务并把 `txID` 交给 coordinator，调用方如果需要最终执行结果，必须自行循环调用 `Engine.Query`。仓库里的 `saga_test.go` 和 `saga_integration_test.go` 已经大量重复这种等待模式，说明阻塞式提交是明确存在的接入需求。

本次设计需要保持现有 SAGA 执行模型不变：事务仍由后台 coordinator/poller 异步推进，可能跨实例完成；`SagaBuilder.WithTimeout` 仍代表事务业务超时，而不是调用方等待超时。新接口必须基于持久化状态判断终态，不能依赖单进程内存事件，否则会破坏多实例恢复和抢锁执行场景。

另一个必须正视的现状是：`Engine.Submit` 在事务已入库后调用 `coordinator.Submit(txID)`，但目前没有处理队列满返回 `false` 的逻辑，而 `recoveryScan` 只在启动时运行一次。结果是，某些事务可能长时间停留在 `pending`，并不是因为正在慢执行，而是因为当前没有执行者真正接手。这里需要精确定义边界：
- 若系统中存在活跃的 engine 实例，且事务配置了 `WithTimeout`，`timeoutScanner` 可以周期性发现超时事务并将其转入 `compensating`，因此不一定会无限停留在 `pending`。
- 若系统中没有任何活跃执行者，则没有 coordinator worker 和 `timeoutScanner` 可推进事务；此时即使配置了 `WithTimeout`，事务也不会自行脱离 `pending`。
- 若存在活跃执行者，但事务未配置超时且提交后未被执行者接手，则该事务可能长期停留在 `pending`，直到后续恢复机会出现。

## Goals / Non-Goals

**Goals:**
- 提供一个简单的 `Engine.SubmitAndWait(ctx, def)` API，供调用方在需要最终结果时同步等待。
- 明确区分五类结果：提交失败、事务终态失败、事务在等待期间消失、调用方上下文提前结束、等待期间的基础设施错误。
- 保持 `Submit`、coordinator、poller 的现有职责和执行路径不变，不引入新的执行通道。
- 通过测试覆盖同步步骤、异步轮询步骤、补偿、事务超时、查询失败、事务消失、无活跃执行者和队列派发边界等场景。
- 更新对外文档，说明 `Submit` 与 `SubmitAndWait` 的适用场景以及 `ctx` / `WithTimeout` / 运行中 engine 实例的区别。

**Non-Goals:**
- 不新增基于 channel 或 callback 的等待机制。
- 不改变 saga 事务的状态机、补偿顺序或多实例锁语义。
- 不在本次变更中扩展 `Engine.Query` 为完整 step detail 查询接口。
- 不在本次变更中彻底修复 coordinator 队列满后缺少周期性 pending 恢复扫描的问题；该问题会在设计和文档中明确记录为现有边界。
- 不新增 engine 级用户可配置等待参数，除非实现过程中证明固定默认值不可接受。

## Decisions

### 1. 采用 `Submit + Query` 轮询实现阻塞等待

`SubmitAndWait` 将先调用现有 `Submit` 完成事务创建，再用定时轮询 `Query` 的方式等待事务进入终态（`succeeded` / `failed`）。

选择这个方案的原因：
- `Query` 读取的是数据库持久化状态，天然适配多实例 coordinator、恢复扫描和异步 poller。
- 不需要侵入 coordinator/poller 内部，不会引入新的进程内通知耦合。
- 能直接复用现有测试和状态语义，改动面最小。

备选方案：
- 复用 poller/coordinator 的通知 channel。放弃原因是该机制不是为跨实例等待设计的，且只覆盖部分路径，不适合作为公共 API 的正确性基础。
- 在 store 层新增阻塞通知。放弃原因是会引入更高复杂度，而当前需求用轮询已足够满足。

### 2. 保留 `ctx` 作为调用方等待生命周期控制

`SubmitAndWait` 必须接收 `ctx`，并用它控制：提交数据库操作、等待循环、上游请求取消以及服务 shutdown 传播。`SagaDefinition.TimeoutSec` / `WithTimeout` 继续只控制 saga 事务自身的业务超时与补偿触发。

选择这个方案的原因：
- 调用方通常比事务本身需要更短的等待窗口，例如 HTTP 请求只等 10 秒，但 saga 可以继续后台完成。
- 事务超时后还可能经历补偿，`SubmitAndWait` 只有在调用方 `ctx` 仍有效时才应继续等到最终态。

备选方案：
- 仅依赖 `WithTimeout`。放弃原因是它不能表达调用方主动取消，也不能覆盖提交阶段和查询阶段的超时需求。
- 为 `SubmitAndWait` 增加独立超时参数。放弃原因是与 `ctx` 职责重叠，会使 API 语义变差。

### 3. 返回 `txID`、最终/最近状态，以及可区分的错误

建议接口签名为：

`func (e *Engine) SubmitAndWait(ctx context.Context, def *SagaDefinition) (string, *TransactionStatus, error)`

并导出至少两个哨兵错误：
- `ErrTransactionFailed`：事务已进入最终 `failed`。
- `ErrTransactionDisappeared`：`Submit` 已成功返回后，后续等待查询发现事务不存在。

返回约定：
- `Submit` 失败：返回空 `txID`、空状态和原始错误。
- 事务最终成功：返回 `txID`、终态状态和 `nil`。
- 事务最终失败：返回 `txID`、终态状态和 `ErrTransactionFailed`（可包装）。
- 事务消失：返回 `txID`、最近一次成功查询到的状态（若有）和 `ErrTransactionDisappeared`（可包装）。
- `ctx` 提前结束：返回 `txID`、最近一次成功查询到的状态和 `ctx.Err()`。
- 等待期间遇到无法恢复的查询/基础设施错误：返回 `txID`、最近一次成功查询到的状态（若有）和该错误。

选择这个方案的原因：
- 调用方既能用 `error` 快速分支，也能读取返回的 `TransactionStatus` 获取步骤级概览。
- 终态失败、事务消失、调用方等待结束、以及基础设施异常是不同问题，必须在 API 层明确区分。

备选方案：
- 只返回 `error`。放弃原因是调用方会丢失步骤状态信息，仍需额外调用 `Query`。
- 在事务失败时返回 `nil` 状态。放弃原因是会丢失失败现场信息，不利于排错。

### 4. 定义等待循环中的 `Query` 错误和事务消失语义

等待循环不能对 `Query` 的非终态错误保持未定义状态。实现将区分两类异常：
- `Query` 返回普通错误：视为读路径基础设施异常。实现 SHOULD 使用有限次数的指数退避重试；推荐默认值为连续失败达到 3 次后返回错误，退避从 500ms 开始并封顶到 2s。
- `Query` 返回 `nil, nil`：在 `Submit` 已成功返回后，这代表事务在等待期间异常消失。实现 MUST 将其视为 `ErrTransactionDisappeared` 并结束等待，不得静默返回空状态成功。

选择这个方案的原因：
- 避免把短暂数据库抖动直接误判为 saga 失败。
- 避免把“事务消失”这种不一致状态包装成正常成功路径。
- 通过推荐默认值收敛实现差异，减少实现者随意选择重试策略的空间。
- 让错误路径的首次重试不比正常轮询更激进，避免故障期间额外放大数据库压力。

备选方案：
- 所有 `Query` 错误立即返回。放弃原因是对短暂连接抖动过于敏感。
- 所有 `Query` 错误无限重试直到 `ctx` 结束。放弃原因是永久性故障会被掩盖，调用方无法及时得知等待链路已经失效。

### 5. 使用内部可覆盖的 500ms 基准轮询间隔

`SubmitAndWait` 的正常等待循环将使用一个包内未导出的基准轮询间隔，推荐默认值为 500ms，并允许测试通过受控方式调小。正常轮询与错误退避分开处理：
- 正常查询成功时，按 500ms 基准间隔继续等待。
- `Query` 出错时，使用 Decision 4 中定义的错误退避策略。

选择这个方案的原因：
- `Engine.Query` 当前至少包含一次事务查询和一次步骤查询；在并发等待场景下，轮询频率过高会放大数据库压力。
- 以 500ms 为基准时，每个等待者大约每秒触发 2 次 `Query`，即约 4 次底层 SQL 读取，明显比 200ms 更保守。
- 测试仍可缩短等待间隔以减少集成测试时长和脆弱性。

备选方案：
- 暴露 `Config.SubmitWaitInterval`。放弃原因是当前需求尚不足以证明需要配置化。
- 默认 200ms 固定间隔。放弃原因是对 PostgreSQL 查询压力评估不够保守。

### 6. 文档说明依赖“活跃执行者”，并记录当前 pending 边界

`SubmitAndWait` 的正确前提不是“当前 `Engine` 实例一定已调用 `Start()`”，而是“系统中至少存在一个连接同一存储并正在运行的 engine 实例，可以推进该事务”。文档必须说明：
- 若当前实例或其他实例能推进事务，`SubmitAndWait` 可以正常等待到终态。
- 若没有任何活跃执行者，方法将持续等待，直到事务被其他实例接手或调用方 `ctx` 结束；此时即使事务配置了 `WithTimeout`，也不会自动被推进。
- 在当前实现下，如果事务已入库但 coordinator 本地入队失败，且后续没有新的恢复机会，该事务也可能长时间停留在 `pending`；对于存在活跃执行者且配置了 `WithTimeout` 的事务，还可能由 `timeoutScanner` 在超时后接手推进到补偿路径。
- `SubmitAndWait` 不会把 `pending` 状态误判为成功，只会继续等待直到终态、事务消失、基础设施错误或 `ctx` 结束。

选择这个方案的原因：
- 这与当前 PostgreSQL 持久化、多实例抢锁执行模型一致。
- 避免错误地把合法的跨实例等待场景判为“engine 未启动”。
- 正视当前 coordinator 队列溢出/恢复模型的限制，避免调用方误以为 `pending` 必然表示“正在慢执行”。

备选方案：
- 在 `SubmitAndWait` 入口强制检查当前实例是否已 `Start()` 并返回 `ErrEngineNotStarted`。放弃原因是这会错误拒绝由其他实例执行的合法部署形态。
- 在本次变更中直接修改 `Submit` 让队列满时返回错误。放弃原因是事务此时已经持久化，直接返回失败会制造“已提交但看似失败”的歧义；更完整的修复应是未来补充周期性 pending 恢复机制。

## Risks / Trade-offs

- [等待期间增加查询压力] → 以 500ms 作为默认基准间隔；每个等待者约每秒触发 2 次 `Query`，当前 `Query` 约对应 4 次底层 SQL 读取；保留包内可覆盖间隔以优化测试；后续若有性能证据再考虑配置化或事件化优化。
- [调用方误把 `ctx` 当作 saga 事务超时] → 在文档、注释和测试名称中明确 `ctx` 与 `WithTimeout` 的职责边界。
- [最终失败或事务消失语义不清] → 导出 `ErrTransactionFailed` 和 `ErrTransactionDisappeared`，并通过返回状态同时表达，避免把业务失败或数据不一致混同为基础设施错误。
- [查询链路临时异常导致等待不稳定] → 定义 3 次连续失败阈值和指数退避策略，并为持续失败保留显式返回路径。
- [没有活跃执行者或本地入队失败时调用方误以为方法卡死] → 在 godoc 和文档中明确依赖活跃执行者推进事务，并覆盖 `pending` 长时间不推进时由 `ctx` 结束返回的测试。

## Migration Plan

这是一个增量 API 变更，不涉及数据库 schema 或迁移脚本修改。现有 `Submit` 调用方无需改动；需要同步等待结果的调用方可选择切换到 `SubmitAndWait`。

文档和示例将补充两类调用方式的适用场景，并明确 `SubmitAndWait` 依赖至少一个运行中的 engine 实例推进事务，不要求必须由当前实例执行。同时会说明当前实现中仍存在“事务已持久化但未被执行者接手”的 pending 边界，作为后续恢复机制优化的输入。

## Open Questions

- 当前没有阻塞性问题。未来若需要彻底消除队列溢出后长期 `pending` 的窗口，应单独评估周期性 pending/recovery 扫描机制。
