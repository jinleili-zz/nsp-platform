## Context

当前 SAGA 运行态的主要观测数据已经存在于 PostgreSQL：
- `saga_transactions` 保存事务级状态、当前步骤、超时、最后错误和分布式锁信息
- `saga_steps` 保存每个步骤的状态、重试次数、轮询次数、开始/结束时间、最后错误和响应体
- `saga_poll_tasks` 保存异步步骤的下一次轮询时间与轮询任务锁信息

这意味着第一期不需要改动协调器、执行器或持久化模型，就可以构建一个只读观测工具。
约束也很明确：
- 当前没有事件流表，只能展示“当前快照”，不能展示完整状态迁移历史
- `SagaDefinition.Name` 没有落库，第一期详情页无法稳定显示事务业务名称
- `trace_id` 当前只在 `payload` 中以 `_trace_id` 形式存在，需要在只读查询层做兼容性提取
- 工具必须适用于本地排障和线上 Kubernetes 环境中的只读连接场景

## Goals / Non-Goals

**Goals:**
- 提供一个独立的 CLI/TUI 工具，支持 `list`、`show`、`watch`、`failed` 四类观测操作
- 只通过只读 SQL 查询现有 `saga_*` 表，输出稳定的事务与步骤视图
- 在 TUI 中提供事务摘要、步骤列表、选中步骤详情和自动刷新能力
- 让使用者无需手写 SQL 即可定位卡住的步骤、重试状态、轮询状态和最终失败原因
- 保持实现边界清晰，不改变现有 SAGA 执行行为和数据库写入路径

**Non-Goals:**
- 不提供 Web UI 或 HTTP 观测服务
- 不实现事务重试、手工补偿、终止事务等写操作
- 不补充事件表、审计时间线或完整状态迁移历史
- 不修改现有 SAGA runtime 的状态机、重试策略或轮询策略
- 不在第一期引入复杂鉴权、多租户隔离或 RBAC

## Decisions

### Decision 1: 以独立命令形式交付，而不是挂到 `saga.Engine`

**选择**：新增独立命令，例如 `cmd/sagactl`

**替代方案**：
- 在 `saga.Engine` 中扩展更多查询 API
- 直接做一个 Web 管理页

**理由**：
- 观测工具是运维/开发者工具，不属于事务执行 SDK 的核心职责
- 独立命令更适合本地直连数据库和跳板机排障场景
- 第一期开发 CLI/TUI 成本最低，不需要引入服务端部署和权限模型

### Decision 2: 直接查询数据库快照，而不是复用 `Engine.Query()`

**选择**：观测工具使用专门的只读查询层，直接读取 `saga_transactions`、`saga_steps`、`saga_poll_tasks`

**替代方案**：只调用 `Engine.Query()`

**理由**：
- `Engine.Query()` 当前返回的字段为：
  - 事务级：`ID`、`Status`、`CurrentStep`、`LastError`、`CreatedAt`、`FinishedAt`
  - 步骤级：`Steps[Index, Name, Status, PollCount, LastError]`
- 这些字段足够支持轻量状态查询，但仍不足以支撑 observer 的详情和排障视图
- 直查数据库还可以拿到 `retry_count`、`started_at`、`finished_at`、`next_poll_at`、`locked_by`、`locked_until`、`action_response`，以及从 `payload` 中兼容提取 `_trace_id` 等额外观测字段
- 第一阶段无需改动现有 `Engine` API，避免把排障视图耦合进业务 SDK

### Decision 3: 将查询逻辑收敛到单独的只读仓储层

**选择**：新增一个职责清晰的只读查询包，例如 `saga/observer`

**替代方案**：
- 在 CLI 命令里直接内联 SQL
- 复用 `PostgresStore` 并继续扩展其接口

**理由**：
- `PostgresStore` 当前聚焦事务执行路径，直接扩展会把读模型和写模型混在一起
- 独立只读仓储更容易测试，也便于后续复用到 Web UI 或 HTTP API
- CLI/TUI 层只处理展示，不直接拼装复杂 SQL

### Decision 4: CLI 与 TUI 分层，命令解析尽量保持轻量

**选择**：
- `list`、`show`、`failed` 使用普通 CLI 输出
- `watch` 使用 TUI 展示单事务观察界面
- CLI 参数解析优先使用标准库；TUI 引入一个专用终端 UI 依赖

**替代方案**：
- 全部只做纯文本 CLI
- 引入完整 CLI 框架和完整 TUI 框架

**理由**：
- 列表和详情命令更适合脚本化与管道处理
- 观察模式需要自动刷新、选中步骤和分区布局，TUI 更合适
- 参数解析不需要复杂子命令生态，优先保持依赖面小

### Decision 5: 第一阶段只展示“当前快照”，不伪造时间线

**选择**：基于现有表展示事务和步骤当前状态，并根据 `started_at/finished_at/current_step/poll_count/retry_count` 推导摘要

**替代方案**：
- 通过日志反查拼时间线
- 为了 TUI 完整性先加事件表

**理由**：
- 当前数据库没有事件流，强行拼装时间线会产生误导
- 日志格式尚未结构化统一，第一期不应依赖日志侧解析
- “当前快照 + 自动刷新”已经能解决多数现场排障问题

### Decision 6: 强制只读模式，避免误伤运行中的事务

**选择**：工具仅实现 `SELECT` 查询，不调用任何写接口，不获取执行锁，不做 `FOR UPDATE`

**替代方案**：
- 允许后续附加重试、补偿等运维动作
- 通过已有 `Store` 锁机制提供诊断增强

**理由**：
- 第一阶段的核心目标是观测，而不是运维干预
- 只读语义更安全，也便于申请生产只读数据库账号
- 避免和协调器/poller 的分布式锁争用

## Risks / Trade-offs

- **[风险] 当前没有事件历史，观察结果只能代表快照，不代表完整执行轨迹**
  → 在文档和界面中明确标注“当前状态视图”，不提供伪时间线；后续通过新 proposal 引入事件表解决

- **[风险] 事务名称未落库，列表页可读性不如业务侧预期**
  → 第一阶段以 `tx_id`、`trace_id`、`status`、`last_error` 为主；是否补充事务名入库留到后续提案

- **[风险] `action_response` 或 `payload` 过大导致 TUI 可读性差**
  → 详情视图默认展示摘要和截断预览，完整 JSON 通过单独展开或 CLI 原始输出查看

- **[风险] 线上数据库访问权限较严格，工具落地受限**
  → 设计上只要求只读 DSN；支持通过环境变量或参数传入连接串，适配只读账号

- **[取舍] 引入 TUI 依赖会扩大模块依赖面**
  → 保持 TUI 依赖只在命令入口和展示层使用，不渗透到 `saga` 核心执行包

## Migration Plan

1. 新增观测命令和只读查询层，不修改现有 SAGA migration 和执行逻辑
2. 在开发环境和测试库中验证 CLI/TUI 输出与直接 SQL 查询一致
3. 补充文档，明确工具的只读语义、连接方式和命令用法
4. 生产环境以只读数据库账号使用；若发现查询压力过大，再补充索引或过滤策略的独立提案

## Open Questions

- 第一阶段的命令入口名称最终使用 `sagactl` 还是其他仓库约定名称
- TUI 依赖最终选型是 `tview`、`bubbletea`，还是用 ANSI 刷新做一个轻量 watch 模式
- 是否需要在第一阶段额外暴露 `trace_id` 到普通 CLI 列表视图，还是仅在 `show/watch` 详情视图展示
