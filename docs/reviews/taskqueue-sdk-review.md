# TaskQueue SDK Review

这份 review 文档已归档。

它最初针对的是旧版 `taskqueue` 实现，那个版本仍包含：

- workflow engine
- `TaskPayload` / `TaskResult`
- `HandleRaw`
- `rocketmqbroker`

这些内容都已经不再代表当前代码。

## 当前结论

现在的 `taskqueue` 已经简化为纯 broker 层：

- 公共模型：`Task` / `ReplySpec`
- 消费签名：`func(ctx context.Context, task *taskqueue.Task) error`
- 当前仓库实现：`asynqbroker`
- 保留能力：`Inspector` 四层接口

## 请改看这些文档

- `docs/taskqueue.md`
- `docs/modules/taskqueue.md`
- `docs/taskqueue_inspector.md`
- `docs/taskqueue_refactor_design.md`
- `taskqueue/GUIDE.md`
- `taskqueue/COMPATIBILITY_REPORT.md`

如果后续还需要保留 review 视角，应基于当前 broker-only 设计重新编写，而不是继续沿用这份旧记录。
