# Saga Demo

可运行的 saga 示例，演示两种调用路径：

- `Submit`：事务持久化后立即返回，调用方再通过 `Query` 轮询终态
- `SubmitAndWait`：提交后直接阻塞到终态返回

## 运行

先准备 PostgreSQL 连接串：

```bash
export SAGA_EXAMPLE_DSN="<your-postgres-dsn>"
```

然后在仓库根目录执行：

```bash
SAGA_EXAMPLE_DSN="$SAGA_EXAMPLE_DSN" go run ./examples/saga-demo
```

说明：

- 如果未设置 `SAGA_EXAMPLE_DSN`，示例会回退读取 `TEST_DSN`
- 示例启动时会自动尝试执行 `saga/migrations/saga.sql`
- 示例内部自带一个本地 HTTP demo 服务，不需要额外准备业务服务地址
