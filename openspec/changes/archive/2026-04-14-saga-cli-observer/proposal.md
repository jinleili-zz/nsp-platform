## Why

当前 SAGA 模块已经把事务状态、步骤状态、轮询任务和错误信息持久化到 PostgreSQL，
但排障和日常巡检仍然只能靠业务方手写 SQL 或零散日志检索。`Engine.Query()` 只适合程序内做简单状态查询，
不适合一线排障场景下快速回答“卡在哪一步、是否在重试、是否还在轮询、最后一次错误是什么”。

第一期需要一个基于 CLI/TUI 的只读观测工具，直接复用现有 `saga_*` 表提供统一观察入口，
先解决“看得到、查得快”的问题，再为后续事件流和更完整的可视化审计打基础。

## What Changes

- 新增一个独立的 SAGA 只读观测工具，提供 CLI 子命令和 TUI 交互界面
- 工具直接读取 PostgreSQL 中现有的 `saga_transactions`、`saga_steps`、`saga_poll_tasks` 表，不改动事务执行主流程
- 提供最小可用的观测能力：`list`、`show <tx_id>`、`watch <tx_id>`、`failed`
- 在详情视图中展示事务摘要、按步骤排序的执行状态、重试次数、轮询次数、下一次轮询时间、最后错误和响应摘要
- 在观察模式中提供自动刷新能力，方便现场跟踪异步步骤和补偿过程
- 明确该工具为只读工具：不提交事务、不重试步骤、不人工触发补偿、不修改数据库状态
- 同步补充 SAGA 文档，说明工具定位、使用方式和第一期边界

## Capabilities

### New Capabilities
- `saga-cli-observer`: 提供一个基于终端的只读观测工具，用于查询和展示 SAGA 事务及步骤执行状态，支持列表、详情和自动刷新的观察模式

### Modified Capabilities

（无现有 spec 需要修改）

## Impact

- **受影响代码**：
  - 新增观测命令入口，例如 `cmd/sagactl`
  - 新增只读查询层，例如 `saga/observer` 或同等职责的内部包
  - 可能补充少量 `saga` 读模型结构体和格式化辅助代码
- **数据库影响**：第一期不新增表、不修改现有 migration，完全基于现有 `saga_*` 表查询
- **运行时影响**：不改动 `Coordinator`、`Executor`、`Poller` 的执行语义，不影响线上事务处理
- **API 影响**：不改变现有 `saga.Engine` 对外 API；新增一个独立 CLI/TUI 工具入口
- **依赖关系**：
  - CLI 参数解析优先使用标准库
  - TUI 预计新增一个终端 UI 依赖，用于列表/详情/刷新交互
- **文档同步**：
  - `docs/saga.md`
  - `docs/modules/saga.md`
  - `saga/README.md`
  - 需要时补充仓库根 `README.md` 的工具入口说明
