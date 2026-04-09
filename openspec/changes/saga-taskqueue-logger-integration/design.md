## Context

仓库已经有统一的 `logger` 模块，支持结构化字段、`context` 中的 `trace_id` / `span_id` 自动提取，以及全局 logger 和注入式 logger 两种使用方式。但 `saga` 当前仍在后台执行路径中大量直接使用 `fmt.Printf` 和 `log.Printf`，`taskqueue/asynqbroker` 也在 consumer 包装层直接使用标准库日志。

这带来几个直接问题：
- 运行日志不能稳定复用仓库已有的输出配置、级别控制和日志分类
- 异步链路中的 `trace_id` / `span_id` 虽然已经能进 `context`，但直接 `Printf` 时无法自动带出
- 模块测试很难验证日志行为，调用方也无法为单个模块注入测试 logger
- asynq 框架日志和本仓库的业务运行日志分属不同通道，排障体验割裂

这是一个典型的跨模块基础设施改造，涉及 `saga`、`taskqueue/asynqbroker`、`logger` 三层协作，适合先做 design 再落代码。

## Goals / Non-Goals

**Goals:**
- 统一 `saga` 和 `taskqueue/asynqbroker` 的运行时日志输出到仓库 `logger`
- 在有 `context` 的路径上使用 context-aware 日志接口，自动关联 trace 字段
- 提供可选 logger 注入能力，减少对全局 logger 的硬耦合，同时保持现有构造入口兼容
- 让 asynq 框架日志和 consumer/broker 包装层日志默认进入同一日志体系
- 保持改造范围聚焦于“运行时日志输出”，不引入新的业务语义或新日志依赖

**Non-Goals:**
- 不重构 `taskqueue.Broker` / `taskqueue.Consumer` 核心接口
- 不为所有模块一次性引入复杂的日志分类、采样或动态路由策略
- 不把纯数据结构、模板渲染、只读查询层等低层工具函数全部强制改成到处打日志
- 不改变现有 trace 传播协议、本地消息结构或 SAGA 状态机语义

## Decisions

### Decision 1: 统一使用仓库 `logger` 作为运行时日志出口

**选择**：`saga` 与 `taskqueue/asynqbroker` 的模块运行日志统一通过仓库 `logger` 输出，不再直接调用 `fmt.Printf` / `log.Printf`

**替代方案**：
- 继续保留标准库输出，只在外层服务里做 stdout 收集
- 每个模块各自定义日志接口再适配

**理由**：
- 仓库已经有稳定的 `logger` 能力，继续保留标准库输出只会放大不一致
- 直接复用 `logger` 可以立刻获得字段化输出、trace 关联和统一配置
- 相比自定义新接口，直接依赖现有 `logger.Logger` 成本更低、认知更一致

### Decision 2: 优先在导出配置层增加“可选 logger 注入”，而不是修改核心接口

**选择**：
- 为 `saga.Config` 新增可选 logger 配置
- 为 `taskqueue/asynqbroker.ConsumerConfig` 新增面向仓库 `logger` 的可选配置
- 保持 `taskqueue.Broker` / `taskqueue.Consumer` 核心接口不变

**替代方案**：
- 完全依赖全局 logger，不提供注入能力
- 修改 `Broker` / `Consumer` 接口，把 logger 作为构造或运行参数强制引入

**理由**：
- 全局 logger 可以作为默认值，但缺乏测试隔离和模块级覆写能力
- 直接改核心接口会把一次日志改造升级为更大范围的 API 变更，不必要
- 在配置层做可选字段是向后兼容的，适合当前仓库节奏

### Decision 3: 具备 `context` 的运行路径一律使用 context-aware 日志接口

**选择**：在 `saga` 的事务驱动路径和 `taskqueue/asynqbroker` 的消息处理路径上优先使用 `logger.*Context(...)`

**替代方案**：
- 统一使用不带 context 的日志接口，再手工拼 trace 字段
- 只在最外层 HTTP/middleware 里记录 trace，不让基础模块感知

**理由**：
- 这两个模块已经在运行路径中持有 `context.Context`
- trace 与日志关联是这次改造的核心收益之一，不该退化成手工字段复制
- 统一使用 context-aware 接口更利于减少漏字段和后续审计

### Decision 4: asynq 框架日志默认桥接到仓库 `logger`，但保留显式 `asynq.Logger` 覆盖

**选择**：
- 保留 `ConsumerConfig.Logger asynq.Logger` 的现有覆盖语义
- 当调用方未显式提供 `asynq.Logger` 时，由实现层提供一个桥接到仓库 `logger` 的默认 adapter
- consumer 包装层自己的错误日志也走仓库 `logger`

**替代方案**：
- 强制删除 `ConsumerConfig.Logger`，统一改为仓库 `logger`
- 保持 asynq 默认 logger，不处理框架日志统一问题

**理由**：
- 删除现有字段属于不必要的破坏性调整
- 只改包装层日志而不处理 asynq 内部日志，会继续留下两套输出通道
- 默认桥接 + 显式覆盖，是兼容性和一致性的平衡点

### Decision 5: 先覆盖“明确属于运行时基础设施”的日志点，不扩大到所有工具路径

**选择**：本次重点覆盖：
- SAGA 后台协程与状态机驱动日志
- poll/recovery/timeout/compensation/queue 压力等运行事件
- taskqueue consumer/broker 包装层和 asynq 运行时日志

**替代方案**：
- 一次性扫全仓所有 `fmt.Printf` / `log.Printf`
- 只改最明显的几处错误日志

**理由**：
- 这次提案的边界是 `saga` 和 `taskqueue` 运行时日志统一
- 一次性全仓收口会让范围失控，影响交付节奏
- 只改零散错误日志又无法形成稳定规则，后续还会反复回退

## Risks / Trade-offs

- **[风险] 改成结构化日志后，字段过多或日志级别选择不当会放大日志噪声**
  → 在实现时明确区分 `Info/Warn/Error/Debug`，只对关键状态变化和异常路径打日志

- **[风险] 某些后台路径没有业务 `context`，trace 字段无法完整带出**
  → 对无 trace 的后台日志至少补齐 `tx_id`、`step_id`、`queue`、`task_id` 等模块级标识，不把 trace 作为唯一关联手段

- **[风险] 同时支持仓库 `logger` 和 `asynq.Logger` 会让配置面略复杂**
  → 保持现有 `asynq.Logger` 字段不变，只新增一个面向仓库 logger 的可选字段，并明确优先级规则

- **[取舍] 通过配置层注入 logger 仍然会让模块直接依赖仓库 logger 类型**
  → 当前仓库已经把 `logger` 作为基础设施模块，这是可接受的耦合，收益大于抽象成本

## Migration Plan

1. 为 `saga` 和 `taskqueue/asynqbroker` 明确 logger 配置入口与默认值策略
2. 替换运行时路径中的标准库日志调用，统一为仓库 `logger`
3. 为 asynq 提供默认 logger adapter，并验证显式 `asynq.Logger` 覆盖仍然生效
4. 补充测试，验证 logger 注入、trace 关联和关键错误路径日志行为
5. 更新模块文档和接入说明，说明新的 logger 配置和默认行为

## Open Questions

- `saga` 运行日志默认应该走全局 logger，还是优先走 `logger.Platform()` / `logger.Business()` 这类分类 logger
- `taskqueue/asynqbroker` 是否只给 `ConsumerConfig` 增加仓库 logger 配置，还是连 `Broker` / `Inspector` 的具体实现也补同样的入口
- 是否需要在第一阶段同步清理 `examples/` 或文档中的标准库日志示例，还是只覆盖库本身运行时路径
