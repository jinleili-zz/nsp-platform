## Why

当前 taskqueue 模块缺少一个完整的示例程序来演示生产者-消费者模式下的双向通信（请求-回复）。开发者需要参考一个实际的例子来理解如何：使用多个队列、指定回复队列、通过 TaskID 匹配任务结果，以及使用 Inspector 监控队列状态。这个示例将降低上手门槛，展示最佳实践。

## What Changes

- 新增 `examples/taskqueue-reply-demo/` 目录，包含完整的生产者-消费者示例
- Producer 和 Consumer 分离为独立的 main.go 文件
- 演示多队列命名规范（使用 `:` 连接多个字符）
- 演示 ReplySpec 的使用：Producer 指定回复队列，Consumer 处理完成后向指定队列回复结果
- Payload 中包含 `task_id` 字段，Consumer 回复时根据该字段匹配
- 演示 Inspector 接口的使用（查询队列统计、任务列表等）
- 提供基于 Docker 的 Redis 测试环境配置

## Capabilities

### New Capabilities
- `taskqueue-reply-example`: 完整的生产者-消费者双向通信示例，展示多队列、回复机制、Inspector 使用

### Modified Capabilities
- 无

## Impact

- 新增示例代码，不影响现有业务代码
- 依赖 taskqueue 模块的现有接口（Broker, Consumer, Task, ReplySpec, Inspector）
- 需要本地 Docker 环境运行测试用 Redis
