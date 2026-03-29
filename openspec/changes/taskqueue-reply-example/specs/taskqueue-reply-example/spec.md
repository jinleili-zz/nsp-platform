## ADDED Requirements

### Requirement: Producer 和 Consumer 分离部署
Producer 和 Consumer 必须是独立的可执行程序，各自有自己的 main.go 入口。

#### Scenario: Producer 独立运行
- **WHEN** 启动 Producer 程序
- **THEN** Producer 连接到 Redis 并向指定队列发送任务
- **AND** Producer 不处理任何消费逻辑

#### Scenario: Consumer 独立运行
- **WHEN** 启动 Consumer 程序
- **THEN** Consumer 连接到 Redis 并监听指定队列
- **AND** Consumer 不发送原始任务（只发送回复）

### Requirement: 队列命名使用冒号分隔符
队列名称由多个语义部分组成时，必须使用冒号 `:` 作为分隔符。

#### Scenario: 多部分队列名
- **WHEN** 定义队列名称为 `service:action:direction`
- **THEN** 队列名称被正确解析为三个语义部分
- **AND** 队列在 Redis 中正确创建

#### Scenario: Inspector 按前缀查询
- **WHEN** 使用 Inspector 查询 `service:*` 模式
- **THEN** 返回所有以 `service:` 开头的队列列表

### Requirement: Producer 指定回复队列
Producer 发送任务时，必须通过 ReplySpec 指定 Consumer 回复结果的队列名称。

#### Scenario: 发送带回复地址的任务
- **WHEN** Producer 创建 Task 时设置 Reply.Queue = "calc:response:outgoing"
- **THEN** Consumer 收到任务后能读取到回复队列地址
- **AND** Consumer 处理完成后向该队列发送结果

### Requirement: Payload 包含 task_id 字段
任务的 Payload 必须包含名为 `task_id` 的字符串字段，用于端到端任务匹配。

#### Scenario: Producer 生成 task_id
- **WHEN** Producer 创建任务
- **THEN** Payload 中包含 UUID 格式的 task_id 字段

#### Scenario: Consumer 回复携带 task_id
- **WHEN** Consumer 处理完成并发送回复
- **THEN** 回复消息的 Payload 中包含原始任务的 task_id
- **AND** Producer 能通过 task_id 匹配请求和回复

### Requirement: Inspector 功能演示
示例必须展示 Inspector 接口的核心功能：查询队列列表、获取队列统计、查看任务详情。

#### Scenario: 查询队列列表
- **WHEN** 调用 Inspector.Queues()
- **THEN** 返回所有队列名称（包括请求队列和回复队列）

#### Scenario: 获取队列统计
- **WHEN** 调用 Inspector.GetQueueStats("calc:request:incoming")
- **THEN** 返回该队列的 Pending、Active、Completed 等统计信息

#### Scenario: 列出任务详情
- **WHEN** 调用 TaskReader.ListTasks()
- **THEN** 返回指定队列中的任务列表，包含 Task ID、状态、Payload 等信息

### Requirement: 基于 Docker 的测试环境
示例必须提供 Docker Compose 配置，用于启动测试所需的 Redis 服务。

#### Scenario: 启动测试环境
- **WHEN** 执行 `docker-compose up -d`
- **THEN** Redis 服务在本地端口 6379 启动
- **AND** Producer 和 Consumer 能连接到该 Redis

#### Scenario: 运行完整测试
- **WHEN** 启动 Redis、Consumer、Producer（按顺序）
- **THEN** Producer 发送的任务被 Consumer 处理
- **AND** Consumer 的回复被 Producer 接收
- **AND** Inspector 能查询到队列统计信息
