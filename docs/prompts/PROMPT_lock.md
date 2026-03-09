## 背景

我在开发 NSP 项目，这是一个基于 Go 的微服务平台，容器化部署在 Kubernetes 上。
我需要在公共基础库（nsp-common）中封装一个统一的分布式锁模块，
以 SDK 形式嵌入每个业务服务。

---

## 技术选型（已确定）

- 底层库：github.com/go-redsync/redsync/v4
- Redis 客户端：github.com/redis/go-redis/v9
- Redis 部署模式：Redis Cluster（3~4 个节点，集群模式）
- Go 版本：>= 1.21
- 封装目标：业务代码只依赖接口，不直接依赖 redsync 或 go-redis
  后续可以只替换实现层（如换成 etcd 或 zookeeper），业务代码不需要改动

---

## 目录结构

nsp-common/
└── pkg/
    └── lock/
        ├── lock.go          # 核心接口定义（Factory / Mutex / MutexOption / 错误变量）
        ├── option.go        # MutexOption 的函数式选项构造函数（WithXxx 系列）
        ├── redis.go         # redsync 实现（redisFactory / redisMutex）
        └── lock_test.go     # 单元测试

---

## 接口设计（lock.go）

### 错误变量

// 封装层定义的错误，业务代码只判断这两个，不引用任何底层库的错误类型
var (
    // ErrLockNotObtained 加锁失败，锁被其他节点持有
    ErrLockNotObtained = errors.New("lock not obtained")

    // ErrLockLost 续约或解锁失败，锁已因超时自动释放
    ErrLockLost = errors.New("lock lost")
)

### MutexOption 结构体

type MutexOption struct {
    // TTL 锁的自动过期时间
    // 默认值：8s
    TTL time.Duration

    // RetryCount 加锁失败时的最大重试次数
    // 仅对 Lock() 生效，TryLock() 固定不重试
    // 默认值：32
    RetryCount int

    // RetryDelay 每次重试之间的基础等待时间
    // 实际等待时间 = RetryDelay + [0, RetryDelay/2) 的随机抖动
    // 随机抖动避免多节点同时重试的惊群效应
    // 默认值：100ms
    RetryDelay time.Duration

    // EnableWatchdog 是否开启自动续约（Watchdog 机制）
    // true 时：获取锁后启动后台 goroutine，每隔 TTL/3 自动调用 Extend
    //          Unlock 时自动停止续约 goroutine
    // false 时：不自动续约，业务代码可手动调用 Extend
    // 默认值：false
    EnableWatchdog bool
}

### Factory 接口

// Factory 是分布式锁的工厂接口
// 不同底层实现（redis/etcd/zk）提供不同的 Factory 实现
// 业务代码只持有 Factory 接口，不依赖任何具体实现类型
type Factory interface {
    // NewMutex 创建一个具名互斥锁
    // name 是锁的全局唯一标识，相同 name 的锁在分布式环境下互斥
    // 推荐命名规范：{业务域}:{资源类型}:{资源ID}，如 "order:pay:ORD-123"
    NewMutex(name string, opts ...func(*MutexOption)) Mutex
}

### Mutex 接口

// Mutex 是分布式互斥锁接口
type Mutex interface {
    // Lock 阻塞加锁，直到成功或 ctx 超时/取消
    // 内部按 MutexOption.RetryCount 和 RetryDelay 进行重试
    // 返回 ErrLockNotObtained 表示重试耗尽仍未获取到锁
    // 返回 ctx.Err() 表示 ctx 已取消或超时
    Lock(ctx context.Context) error

    // TryLock 非阻塞尝试加锁，立即返回，不重试
    // 加锁失败返回 ErrLockNotObtained
    TryLock(ctx context.Context) error

    // Unlock 释放锁
    // 底层使用 Lua 脚本保证原子性，只有持有者才能释放（防误删）
    // 锁已过期时返回 ErrLockLost
    Unlock(ctx context.Context) error

    // Extend 手动续约，将锁的过期时间重置为初始 TTL
    // 锁已过期时返回 ErrLockLost
    Extend(ctx context.Context) error

    // Name 返回锁的名称
    Name() string
}

---

## 函数式选项（option.go）

提供以下 WithXxx 构造函数，业务代码通过这些函数配置 MutexOption：

// WithTTL 设置锁的过期时间
func WithTTL(d time.Duration) func(*MutexOption)

// WithRetryCount 设置最大重试次数
func WithRetryCount(n int) func(*MutexOption)

// WithRetryDelay 设置重试基础等待时间
func WithRetryDelay(d time.Duration) func(*MutexOption)

// WithWatchdog 开启自动续约
func WithWatchdog() func(*MutexOption)

defaultMutexOption 函数返回默认选项值：
  TTL            = 8s
  RetryCount     = 32
  RetryDelay     = 100ms
  EnableWatchdog = false

---

## Redis 实现层（redis.go）

### 重要说明

部署环境为 Redis Cluster 模式（3~4 个节点组成一个集群）。

Redis Cluster 与多个独立节点的本质区别：
  多个独立节点：每个节点是完全独立的 Redis 实例，redsync 使用 Redlock 算法
               同时向多个节点加锁，多数成功才算获取锁
  Redis Cluster：整个集群对外是一个逻辑 Redis，一个 key 只落在一个槽（主节点）
               集群本身通过主从复制保证高可用，不需要也不应该使用 Redlock
               使用单个 ClusterClient 连接集群即可

因此本实现：
  使用 redis.NewClusterClient() 创建单个集群客户端
  只向 redsync.New() 传入一个 Pool（不是多个）
  不使用 Redlock 算法
  Redis Cluster 只支持 DB 0，RedisOption 不提供 DB 字段

### RedisOption 结构体

type RedisOption struct {
    // Addrs Redis Cluster 节点地址列表
    // 至少填写一个节点地址，go-redis 会自动发现集群内其他节点
    // 推荐填写所有已知节点，提高初始化的容错性
    // 示例：[]string{"redis-0:6379", "redis-1:6379", "redis-2:6379"}
    Addrs []string

    // Password Redis 密码，无密码时留空
    Password string

    // PoolSize 每个节点的连接池大小，默认 10
    PoolSize int

    // DialTimeout 连接超时，默认 5s
    DialTimeout time.Duration

    // ReadTimeout 读超时，默认 3s
    ReadTimeout time.Duration

    // WriteTimeout 写超时，默认 3s
    WriteTimeout time.Duration

    // RouteByLatency 是否开启按延迟路由
    // true 时读请求自动路由到延迟最低的节点（主节点或副本节点）
    // 默认 false
    RouteByLatency bool

    // RouteRandomly 是否开启随机路由
    // true 时读请求随机路由到主节点或副本节点（分散读压力）
    // RouteByLatency 和 RouteRandomly 同时为 true 时，RouteByLatency 优先
    // 默认 false
    RouteRandomly bool
}

### NewRedisFactory 函数

// NewRedisFactory 创建基于 redsync + Redis Cluster 的 Factory 实现
func NewRedisFactory(opt RedisOption) (Factory, error)

初始化逻辑：
  1. 验证 opt.Addrs 不为空，为空时返回明确错误信息
  2. 填充默认值：
       PoolSize     为 0 时默认 10
       DialTimeout  为 0 时默认 5s
       ReadTimeout  为 0 时默认 3s
       WriteTimeout 为 0 时默认 3s
  3. 创建单个 ClusterClient：
       client := redis.NewClusterClient(&redis.ClusterOptions{
           Addrs:          opt.Addrs,
           Password:       opt.Password,
           PoolSize:       opt.PoolSize,
           DialTimeout:    opt.DialTimeout,
           ReadTimeout:    opt.ReadTimeout,
           WriteTimeout:   opt.WriteTimeout,
           RouteByLatency: opt.RouteByLatency,
           RouteRandomly:  opt.RouteRandomly,
       })
  4. 用 goredis.NewPool(client) 包装为 redsync 连接池
  5. 调用 redsync.New(pool) 创建 redsync 实例（只传一个 Pool）
  6. 连通性验证：
       调用 client.ClusterInfo(context.Background())
       返回错误时说明集群不可达，NewRedisFactory 返回包含原始错误的错误信息

### redisFactory 结构体

内部字段：
  rs     *redsync.Redsync   // redsync 实例
  client *redis.ClusterClient // 集群客户端（用于生命周期管理）

### redisFactory.NewMutex 实现

1. 应用 defaultMutexOption，再依次应用 opts 中的函数
2. 将 MutexOption 转换为 redsync 的选项：
     TTL         → redsync.WithExpiry(opt.TTL)
     RetryCount  → redsync.WithTries(opt.RetryCount)
     RetryDelay  → redsync.WithRetryDelayFunc(带随机抖动的函数)
       随机抖动实现：实际延迟 = RetryDelay + rand.Int63n(int64(RetryDelay/2))
3. 调用 rs.NewMutex(name, redsyncOpts...) 创建底层锁
4. 返回 redisMutex 包装对象

### redisMutex 结构体

内部字段：
  mu       *redsync.Mutex   // 底层 redsync 锁实例
  opt      MutexOption      // 创建时的完整选项
  watchdog *watchdog        // 自动续约控制器，nil 表示未启用
  rs       *redsync.Redsync // 用于 TryLock 创建临时 Mutex

### redisMutex.Lock 实现

1. 调用 mu.LockContext(ctx)
2. 错误转换：
     redsync.ErrFailed 及其他非 ctx 错误 → ErrLockNotObtained
     ctx 错误（ctx.Err() != nil）→ 原样返回 ctx.Err()
3. 加锁成功且 EnableWatchdog=true 时启动 watchdog goroutine

### redisMutex.TryLock 实现

用同名、tries=1 的临时 redsync.Mutex 实例实现非阻塞：
  创建临时 mutex：rs.NewMutex(name, redsync.WithTries(1), redsync.WithExpiry(opt.TTL))
  调用 tempMutex.LockContext(ctx)
  加锁成功时需要将 token 同步到原 mu（通过重新赋值或记录 token）
  错误转换：失败 → ErrLockNotObtained

注意：TryLock 成功后，后续的 Unlock/Extend 操作仍然使用原 mu 实例
     需要确保 token 一致性，具体实现时注意 redsync.Mutex 的 Value() 方法

实际可行的做法：
  TryLock 直接在原 mu 上用 tries=1 的方式尝试
  通过给 ctx 设置极短的额外超时（1ms）来模拟非阻塞
  或者：重新创建一个完整的 redisMutex 实例用于 TryLock
  选择最简洁且正确的方式实现，在注释中说明选择原因

### redisMutex.Unlock 实现

1. 如果 watchdog 不为 nil，先调用 watchdog.Stop() 停止续约
2. 调用 mu.UnlockContext(ctx)
3. 错误转换：解锁失败（锁已过期或不属于当前节点）→ ErrLockLost

### redisMutex.Extend 实现

1. 调用 mu.ExtendContext(ctx)
2. 错误转换：失败 → ErrLockLost

### redisMutex.Name 实现

返回 mu.Name()

### watchdog 实现

// watchdog 自动续约控制器
type watchdog struct {
    stop chan struct{}
    done chan struct{}
}

// newWatchdog 启动自动续约 goroutine
// interval = max(TTL/3, 1s)，防止 TTL 过短导致续约过于频繁
// extendFn 是续约函数，失败时记录日志但不停止 goroutine（下一个周期继续尝试）
// stop channel 关闭时 goroutine 退出，done channel 关闭表示 goroutine 已完全退出
func newWatchdog(interval time.Duration, extendFn func() error) *watchdog

// Stop 停止续约 goroutine
// 关闭 stop channel，阻塞等待 done channel 关闭（确认 goroutine 已退出）
func (w *watchdog) Stop()

watchdog goroutine 结构：
  for {
      select {
      case <-stop:
          close(done)
          return
      case <-time.After(interval):
          if err := extendFn(); err != nil {
              // 记录日志（使用标准库 log，不依赖业务日志模块）
              // 不退出，继续下一个周期
          }
      }
  }

---

## 测试要求（lock_test.go）

### 测试环境说明

miniredis（github.com/alicebob/miniredis/v2）不支持 Cluster 模式
测试文件使用单节点 miniredis + redis.NewClient() 替代 ClusterClient
通过接口隔离保证测试有效性：
  测试覆盖 Mutex 接口的全部行为（Lock/TryLock/Unlock/Extend/Watchdog）
  不测试 Redis Cluster 路由逻辑（这是 go-redis 的职责，有其自己的测试）
在 lock_test.go 顶部注释中说明这一点

测试辅助函数：
  newTestFactory(t *testing.T) Factory
    使用 miniredis 创建单节点测试工厂
    t.Cleanup 中关闭 miniredis

### 测试用例

基础功能：

1. TestLock_Success
   正常加锁后解锁，验证 Lock 和 Unlock 均无错误返回

2. TestLock_Blocking
   goroutine A 持有锁
   goroutine B 调用 Lock 阻塞等待
   goroutine A Unlock 后，goroutine B 成功获取锁
   使用 channel 协调顺序，超时 5s 视为失败

3. TestTryLock_Success
   锁未被持有时，TryLock 成功返回 nil

4. TestTryLock_Fail
   goroutine A 持有锁
   goroutine B 调用 TryLock，立即返回 ErrLockNotObtained
   验证 errors.Is(err, ErrLockNotObtained) == true

5. TestUnlock_LockLost
   设置 TTL=500ms
   加锁后调用 miniredis.FastForward(600ms) 快进时间使锁过期
   调用 Unlock 验证返回 ErrLockLost
   验证 errors.Is(err, ErrLockLost) == true

6. TestExtend_Success
   设置 TTL=2s
   加锁后调用 Extend，验证返回 nil
   miniredis.FastForward(1500ms) 后锁仍然有效（能 Extend 或 Unlock 成功）

7. TestExtend_LockLost
   设置 TTL=500ms
   加锁后 FastForward(600ms) 使锁过期
   调用 Extend 验证返回 ErrLockLost

8. TestName_Returns
   验证 mutex.Name() 返回创建时传入的 name 字符串

错误处理：

9. TestLock_CtxCanceled
   创建已取消的 ctx
   调用 Lock 验证返回 ctx.Err()（即 context.Canceled）
   不返回 ErrLockNotObtained

10. TestLock_CtxTimeout
    设置 RetryCount=100、RetryDelay=1s（重试间隔故意设长）
    goroutine A 持有锁不释放
    goroutine B 用 100ms 超时的 ctx 调用 Lock
    验证 100ms 后返回 context.DeadlineExceeded
    不等到 RetryCount 耗尽

选项验证：

11. TestWithTTL
    设置 TTL=500ms
    加锁后 FastForward(600ms)
    尝试再次加锁（不应被之前的锁阻塞），验证加锁成功

12. TestWithRetryCount
    设置 RetryCount=2、RetryDelay=10ms
    goroutine A 持有锁不释放
    goroutine B 调用 Lock，验证快速失败（重试 2 次后返回 ErrLockNotObtained）
    计时验证总耗时约为 2 * RetryDelay（加合理误差）

13. TestWithWatchdog
    设置 TTL=1s，开启 Watchdog
    加锁后 sleep 2500ms（超过 TTL 两倍）
    验证锁仍然有效（Unlock 返回 nil，而非 ErrLockLost）
    Unlock 后再 sleep 500ms，验证 Watchdog goroutine 已停止（不再续约）
    通过 FastForward + 再次加锁来验证锁已真正释放

互斥保证（核心测试）：

14. TestMutualExclusion
    启动 10 个 goroutine 并发竞争同名锁
    每个 goroutine 持锁期间对共享计数器执行 read-increment-write
    read 和 write 之间 sleep 1ms（放大竞争窗口）
    全部 goroutine 完成后验证计数器 == 10
    验证分布式锁正确保护了临界区

---

## 使用示例（以注释形式写在 lock.go 末尾）

// 初始化（服务启动时执行一次）
factory, err := lock.NewRedisFactory(lock.RedisOption{
    Addrs:    []string{"redis-0:6379", "redis-1:6379", "redis-2:6379"},
    Password: "your-password",
})
if err != nil {
    panic(err)
}

// 创建锁（推荐命名：{业务域}:{资源类型}:{资源ID}）
mutex := factory.NewMutex("order:pay:ORD-123",
    lock.WithTTL(10*time.Second),
    lock.WithWatchdog(),
)

// 加锁
if err := mutex.Lock(ctx); err != nil {
    if errors.Is(err, lock.ErrLockNotObtained) {
        // 锁被其他节点持有，根据业务决定重试或返回错误
    }
    return err
}
defer mutex.Unlock(ctx)

// 执行业务逻辑
// 开启 Watchdog 时无需担心执行时间超过 TTL，续约自动进行

// 手动续约示例（不使用 Watchdog 时）
mutex2 := factory.NewMutex("order:stock:ITEM-456",
    lock.WithTTL(5*time.Second),
)
if err := mutex2.Lock(ctx); err != nil {
    return err
}
defer mutex2.Unlock(ctx)
// 业务逻辑执行中途手动续约
if err := mutex2.Extend(ctx); err != nil {
    if errors.Is(err, lock.ErrLockLost) {
        // 锁已丢失，需要重新加锁或终止当前操作
    }
}

---

## 输出要求

1. 按文件分别输出完整代码，每个文件顶部注释标注文件名和包名
2. 所有导出的类型、函数、方法均需有 godoc 注释
3. 不得省略任何实现细节，不得用注释代替代码
4. 接口层（lock.go / option.go）不出现任何 redsync 或 go-redis 类型
5. redis.go 顶部注释说明为何使用 ClusterClient 而非 Redlock 多节点方案
6. lock_test.go 顶部注释说明使用单节点 miniredis 替代 Cluster 的原因
7. 代码输出完毕后，提供需要在 go.mod 中添加的依赖声明