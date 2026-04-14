## 1. 导出常量与配置字段

- [x] 1.1 在 `taskqueue/asynqbroker/consumer.go` 中定义导出常量 `MinTaskCheckInterval`（200ms）和 `MaxTaskCheckInterval`（2s）
- [x] 1.2 在 `ConsumerConfig` 结构体中新增 `TaskCheckInterval time.Duration` 字段，添加注释说明合法范围与零值行为

## 2. 校验与 clamp 逻辑

- [x] 2.1 在 `NewConsumer` 中实现 clamp 逻辑：负值和零值跳过（保持 asynq 默认）；正值小于 MinTaskCheckInterval 设为 MinTaskCheckInterval；正值大于 MaxTaskCheckInterval 设为 MaxTaskCheckInterval
- [x] 2.2 当值被 clamp 时，通过 `runtimeLog` 输出 warn 级别日志，包含原始值和修正后的值
- [x] 2.3 将校验后的值设置到 `asynq.Config.TaskCheckInterval`

## 3. 单元测试

- [x] 3.1 新增 `TestConsumerConfigTaskCheckInterval` 测试函数，覆盖以下场景：零值保持默认、低于最小值被 clamp 至 200ms、高于最大值被 clamp 至 2s、恰好等于边界值不变、负值等同零值、合法中间值（如 500ms）正常传入
- [x] 3.2 验证 clamp 后 consumer 能正常启动和停止（使用 miniredis）

## 4. 文档更新

- [x] 4.1 更新 `taskqueue/docs/` 下的相关文档，说明 `ConsumerConfig.TaskCheckInterval` 参数、合法范围、默认行为
- [x] 4.2 更新 `AGENTS.md` 中 taskqueue 相关部分（如已描述 ConsumerConfig），补充新字段说明
