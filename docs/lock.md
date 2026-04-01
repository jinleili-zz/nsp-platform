# Lock 模块

> 包路径：`github.com/paic/nsp-common/pkg/lock`

## 功能说明

Lock 模块解决以下问题：

- **分布式互斥**：多实例部署时保证同一资源同一时刻只有一个实例操作
- **自动续约**：Watchdog 机制防止业务执行超时导致锁意外释放
- **防误删保护**：基于 Token 的释放机制，只有持有者才能释放锁
- **优雅降级**：支持阻塞等待（Acquire）和快速失败（TryAcquire）两种模式
- **Redis Cluster 支持**：生产环境直接对接 Redis 集群

---

## 核心接口

```go
// Client 分布式锁客户端接口
type Client interface {
    // New 创建一个命名的分布式锁
    // name 是锁的全局唯一标识，推荐命名规范：{domain}:{resource_type}:{resource_id}
    // 示例："order:pay:ORD-123"
    New(name string, opts ...func(*LockOption)) Lock

    // Close 释放底层连接池
    Close() error
}

// Lock 分布式锁接口
type Lock interface {
    // Acquire 阻塞获取锁，直到成功、ctx 取消或超时
    // 内部按 RetryCount 和 RetryDelay 重试
    // 返回 ErrNotAcquired 表示重试耗尽仍未获取
    // 返回 ctx.Err() 表示上下文取消或超时
    Acquire(ctx context.Context) error

    // TryAcquire 非阻塞尝试获取锁
    // 立即返回，不重试
    // 返回 ErrNotAcquired 表示锁被其他节点持有
    TryAcquire(ctx context.Context) error

    // Release 释放锁
    // 只有持有者（通过 Token 标识）才能释放
    // 返回 ErrLockExpired 表示锁已过期或不属于当前持有者
    Release(ctx context.Context) error

    // Renew 续约，重置 TTL 为初始值
    // 返回 ErrLockExpired 表示锁已过期
    Renew(ctx context.Context) error

    // Name 返回锁名称
    Name() string
}
```

---

## 配置项

### LockOption

| 字段名 | 类型 | 默认值 | 说明 |
|--------|------|--------|------|
| `TTL` | `time.Duration` | `8s` | 锁自动过期时间 |
| `RetryCount` | `int` | `32` | Acquire 最大重试次数 |
| `RetryDelay` | `time.Duration` | `100ms` | 重试基础间隔（实际 = RetryDelay + 随机抖动） |
| `EnableWatchdog` | `bool` | `false` | 启用自动续约（每 TTL/3 续约一次） |

### RedisOption（Redis Cluster）

| 字段名 | 类型 | 默认值 | 说明 |
|--------|------|--------|------|
| `Addrs` | `[]string` | **必填** | Redis Cluster 节点地址列表 |
| `Username` | `string` | `""` | Redis ACL 用户名，空值时退回传统密码认证 |
| `Password` | `string` | `""` | Redis AUTH 密码 |
| `PoolSize` | `int` | `10` | 每节点连接池大小 |
| `DialTimeout` | `time.Duration` | `5s` | 连接超时 |
| `ReadTimeout` | `time.Duration` | `3s` | 读取超时 |
| `WriteTimeout` | `time.Duration` | `3s` | 写入超时 |

### StandaloneRedisOption（单节点，仅开发/测试）

| 字段名 | 类型 | 默认值 | 说明 |
|--------|------|--------|------|
| `Addr` | `string` | **必填** | Redis 地址，如 `localhost:6379` |
| `Username` | `string` | `""` | Redis ACL 用户名，空值时退回传统密码认证 |
| `Password` | `string` | `""` | Redis AUTH 密码 |
| `PoolSize` | `int` | `10` | 连接池大小 |

---

## 快速使用

### 基础获取/释放

```go
package main

import (
    "context"
    "errors"
    "fmt"
    "time"

    "github.com/paic/nsp-common/pkg/lock"
)

func main() {
    // 1. 创建客户端（生产环境使用 Redis Cluster）
    client, err := lock.NewRedisClient(lock.RedisOption{
        Addrs:    []string{"redis-0:6379", "redis-1:6379", "redis-2:6379"},
        Password: "your-password",
    })
    if err != nil {
        panic(err)
    }
    defer client.Close()

    // 2. 创建锁（推荐命名：{domain}:{resource_type}:{resource_id}）
    orderLock := client.New("order:pay:ORD-123",
        lock.WithTTL(10*time.Second),
    )

    // 3. 获取锁
    ctx := context.Background()
    if err := orderLock.Acquire(ctx); err != nil {
        if errors.Is(err, lock.ErrNotAcquired) {
            fmt.Println("锁被其他节点持有，稍后重试")
            return
        }
        panic(err)
    }
    defer orderLock.Release(ctx)

    // 4. 执行业务逻辑（持锁期间）
    fmt.Println("正在处理订单支付...")
    time.Sleep(500 * time.Millisecond)
    fmt.Println("支付完成")
}
```

### Redis ACL 认证

```go
client, err := lock.NewRedisClient(lock.RedisOption{
    Addrs:    []string{"redis-0:6379", "redis-1:6379", "redis-2:6379"},
    Username: "svc-lock",
    Password: "your-password",
})
if err != nil {
    panic(err)
}
defer client.Close()
```

### 快速失败模式（TryAcquire）

```go
func processPayment(client lock.Client, orderID string) error {
    payLock := client.New("order:pay:"+orderID,
        lock.WithTTL(30*time.Second),
    )

    ctx := context.Background()

    // 非阻塞尝试获取锁
    if err := payLock.TryAcquire(ctx); err != nil {
        if errors.Is(err, lock.ErrNotAcquired) {
            // 锁被占用，立即返回，不等待
            return fmt.Errorf("订单 %s 正在被其他实例处理", orderID)
        }
        return err
    }
    defer payLock.Release(ctx)

    // 执行支付逻辑
    return doPayment(orderID)
}
```

### 启用 Watchdog 自动续约

```go
func longRunningTask(client lock.Client, taskID string) error {
    // 业务执行时间可能超过 TTL，启用 Watchdog 自动续约
    taskLock := client.New("task:report:"+taskID,
        lock.WithTTL(5*time.Second),
        lock.WithWatchdog(),  // 每 TTL/3 ~ 1.6s 自动续约
    )

    ctx := context.Background()
    if err := taskLock.Acquire(ctx); err != nil {
        return err
    }
    defer taskLock.Release(ctx)  // Release 会自动停止 Watchdog

    // 执行耗时任务（可能超过 5 秒）
    fmt.Println("开始生成报表...")
    time.Sleep(15 * time.Second)  // 模拟耗时操作
    fmt.Println("报表生成完成")

    return nil
}
```

### 带超时的获取

```go
func acquireWithTimeout(client lock.Client, resourceID string) error {
    resourceLock := client.New("resource:process:"+resourceID,
        lock.WithTTL(10*time.Second),
        lock.WithRetryCount(100),
        lock.WithRetryDelay(200*time.Millisecond),
    )

    // 最多等待 5 秒
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    if err := resourceLock.Acquire(ctx); err != nil {
        if errors.Is(err, context.DeadlineExceeded) {
            return fmt.Errorf("等待锁超时，资源 %s 繁忙", resourceID)
        }
        return err
    }
    defer resourceLock.Release(context.Background())

    // 执行业务逻辑
    return processResource(resourceID)
}
```

---

## 与其他模块集成

### 结合 Logger 记录锁操作

```go
import (
    "github.com/paic/nsp-common/pkg/lock"
    "github.com/paic/nsp-common/pkg/logger"
)

func processWithLock(ctx context.Context, client lock.Client, orderID string) error {
    lockName := "order:pay:" + orderID
    orderLock := client.New(lockName, lock.WithTTL(10*time.Second))

    logger.InfoContext(ctx, "尝试获取分布式锁", "lock_name", lockName)

    if err := orderLock.Acquire(ctx); err != nil {
        logger.WarnContext(ctx, "获取锁失败", "lock_name", lockName, logger.FieldError, err)
        return err
    }
    defer func() {
        if err := orderLock.Release(ctx); err != nil {
            logger.ErrorContext(ctx, "释放锁失败", "lock_name", lockName, logger.FieldError, err)
        } else {
            logger.InfoContext(ctx, "锁已释放", "lock_name", lockName)
        }
    }()

    logger.InfoContext(ctx, "成功获取锁，开始处理业务", "lock_name", lockName)

    // 业务逻辑
    return nil
}
```

### 在 Gin Handler 中使用

```go
import (
    "net/http"

    "github.com/gin-gonic/gin"
    "github.com/paic/nsp-common/pkg/lock"
    "github.com/paic/nsp-common/pkg/logger"
)

type PaymentHandler struct {
    lockClient lock.Client
}

func (h *PaymentHandler) Pay(c *gin.Context) {
    orderID := c.Param("order_id")
    ctx := c.Request.Context()

    payLock := h.lockClient.New("order:pay:"+orderID,
        lock.WithTTL(30*time.Second),
        lock.WithWatchdog(),
    )

    // 非阻塞尝试，避免用户长时间等待
    if err := payLock.TryAcquire(ctx); err != nil {
        if errors.Is(err, lock.ErrNotAcquired) {
            c.JSON(http.StatusConflict, gin.H{
                "code":    409,
                "message": "订单正在处理中，请勿重复提交",
            })
            return
        }
        logger.ErrorContext(ctx, "获取锁异常", logger.FieldError, err)
        c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "系统繁忙"})
        return
    }
    defer payLock.Release(ctx)

    // 处理支付
    if err := h.processPayment(ctx, orderID); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
        return
    }

    c.JSON(http.StatusOK, gin.H{"code": 0, "message": "支付成功"})
}
```

---

## 注意事项

### 锁命名规范

推荐格式：`{domain}:{resource_type}:{resource_id}`

```go
// 正确示例
"order:pay:ORD-123"          // 订单支付
"inventory:deduct:SKU-456"   // 库存扣减
"user:bindphone:U-789"       // 用户绑定手机
"task:report:TASK-001"       // 任务处理

// 错误示例
"lock1"                      // 无语义，难以追踪
"order-pay-ORD-123"          // 使用连字符，不便解析
```

### 性能提示

- **TTL 设置**：略大于业务最长执行时间，避免误过期
- **Watchdog 使用**：执行时间不确定时启用，但会增加 Redis 负载
- **连接池大小**：按并发量调整，默认 10 通常足够

### 常见错误

```go
// 错误：忘记释放锁
func bad1(client lock.Client) {
    l := client.New("demo:lock")
    l.Acquire(ctx)
    // 缺少 defer l.Release(ctx)
    doSomething()
}  // 锁会在 TTL 后自动过期，但期间其他实例无法获取

// 正确：始终 defer Release
func good1(client lock.Client) {
    l := client.New("demo:lock")
    if err := l.Acquire(ctx); err != nil {
        return err
    }
    defer l.Release(ctx)
    doSomething()
}

// 正确：Release 使用 Background context 确保能正常释放
func good2(client lock.Client, ctx context.Context) {
    l := client.New("demo:lock")
    if err := l.Acquire(ctx); err != nil {
        return err
    }
    defer l.Release(context.Background())
    doSomething()
}
```

---

## 错误类型

| 错误 | 说明 | 处理建议 |
|------|------|----------|
| `ErrNotAcquired` | 锁被其他节点持有 | 重试或返回"资源繁忙" |
| `ErrLockExpired` | 锁已过期或不属于当前持有者 | 检查 TTL 设置，考虑启用 Watchdog |
