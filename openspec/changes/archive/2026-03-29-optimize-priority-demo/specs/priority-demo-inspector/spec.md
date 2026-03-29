## ADDED Requirements

### Requirement: Producer 使用 Inspector 查询队列统计
Producer SHALL 在发送完所有任务后，使用 `asynqbroker.NewInspector` 查询 `nsp:taskqueue:high`、`nsp:taskqueue:middle`、`nsp:taskqueue:low` 三个队列的统计信息并打印。

#### Scenario: 打印队列统计
- **WHEN** producer 发送完所有任务后调用 inspector 查询队列
- **THEN** 打印每个队列的 Pending、Active、Completed、Failed 数量

#### Scenario: Inspector 使用完毕后关闭
- **WHEN** producer 退出时
- **THEN** 调用 `inspector.Close()` 释放资源
