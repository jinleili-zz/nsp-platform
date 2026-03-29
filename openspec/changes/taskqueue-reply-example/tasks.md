## 1. 项目结构搭建

- [x] 1.1 创建 `examples/taskqueue-reply-demo/` 目录结构
- [x] 1.2 创建 `cmd/producer/main.go` 文件框架
- [x] 1.3 创建 `cmd/consumer/main.go` 文件框架
- [x] 1.4 创建 `docker-compose.yml` 配置 Redis 服务
- [x] 1.5 创建 `README.md` 使用说明文档

## 2. 共享类型和常量定义

- [x] 2.1 创建 `types.go` 定义 CalcTask 结构体（包含 TaskID, Operation, Operands, ReplyTo）
- [x] 2.2 创建 `types.go` 定义 CalcResult 结构体（包含 TaskID, Result, Error）
- [x] 2.3 定义队列名称常量（CalcRequestQueue, CalcResponseQueue）
- [x] 2.4 定义任务类型常量（TaskTypeCalc）

## 3. Producer 实现

- [x] 3.1 初始化 asynqbroker Broker 连接 Redis
- [x] 3.2 实现生成 UUID 作为 task_id 的函数
- [x] 3.3 实现构建 CalcTask 并序列化为 JSON 的函数
- [x] 3.4 实现发送任务到队列的函数（设置 ReplySpec）
- [x] 3.5 实现监听回复队列的逻辑（使用 Inspector 或直接消费）
- [x] 3.6 实现根据 task_id 匹配请求和回复的逻辑
- [x] 3.7 演示发送多个任务（加法、减法、乘法）

## 4. Consumer 实现

- [x] 4.1 初始化 asynqbroker Consumer 连接 Redis
- [x] 4.2 实现任务处理器：解析 Payload，执行计算
- [x] 4.3 实现回复发送：构建 CalcResult，发送到 ReplySpec.Queue
- [x] 4.4 注册任务处理器到 Consumer
- [x] 4.5 启动 Consumer 开始监听队列

## 5. Inspector 演示

- [x] 5.1 在 Producer 中演示 Inspector.Queues() 获取队列列表
- [x] 5.2 在 Producer 中演示 GetQueueStats() 获取队列统计
- [x] 5.3 在 Producer 中演示 ListTasks() 查看任务列表
- [x] 5.4 打印 Inspector 查询结果到控制台

## 6. 测试验证

- [x] 6.1 启动 Docker Redis 服务
- [x] 6.2 编译 Consumer 程序并启动
- [x] 6.3 编译 Producer 程序并启动
- [x] 6.4 验证 Producer 发送的任务被 Consumer 处理
- [x] 6.5 验证 Consumer 的回复被 Producer 正确接收
- [x] 6.6 验证 Inspector 能正确查询队列状态
- [x] 6.7 停止 Docker Redis 服务
