// cmd/lock-demo/main.go
//
// NSP 分布式锁 SDK 使用示例
//
// 本 demo 通过连接真实 Redis（单节点）演示 lock 包的五个核心场景：
//
//  1. 基础获取/释放：Acquire + Release 正常流程
//  2. TryAcquire 快速失败：持有锁期间，另一个实例立即返回 ErrNotAcquired
//  3. Watchdog 自动续约：持锁时间超过 TTL 后锁依然有效
//  4. 互斥保证：10 个 goroutine 并发写同一计数器，结果必须正确
//  5. 超时放弃：5 秒内未能获取锁则自动放弃（context.WithTimeout）
//
// 运行前请先启动 Redis：
//
//	docker run -d --name redis-demo -p 6379:6379 redis:7-alpine
//
// 运行 demo：
//
//	go run ./cmd/lock-demo/
//
// 自定义 Redis 地址：
//
//	REDIS_ADDR=192.168.1.10:6379 go run ./cmd/lock-demo/
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jinleili-zz/nsp-platform/lock"
)

func main() {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		fmt.Fprintln(os.Stderr, "REDIS_ADDR environment variable is required")
		os.Exit(1)
	}

	fmt.Println("=== NSP 分布式锁 SDK Demo ===")
	fmt.Printf("Redis 地址: %s\n\n", addr)

	client, err := lock.NewStandaloneRedisClient(lock.StandaloneRedisOption{
		Addr: addr,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "连接 Redis 失败: %v\n", err)
		os.Exit(1)
	}
	defer client.Close()

	demoBasicAcquireRelease(client)
	demoTryAcquireFail(client)
	demoWatchdog(client)
	demoMutualExclusion(client)
	demoCtxTimeout(client)

	fmt.Println("\n=== 所有 Demo 执行完毕 ===")
}

// ─────────────────────────────────────────────────────────────────────────────
// 场景 1：基础 Acquire / Release
// ─────────────────────────────────────────────────────────────────────────────

func demoBasicAcquireRelease(client lock.Client) {
	fmt.Println("── 场景 1：基础 Acquire / Release ──────────────────────────")

	l := client.New("demo:order:ORD-001",
		lock.WithTTL(10*time.Second),
	)

	fmt.Println("  [1] 获取锁 demo:order:ORD-001 ...")
	ctx := context.Background()
	if err := l.Acquire(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "  Acquire 失败: %v\n", err)
		return
	}
	fmt.Println("  [1] 锁已获取，执行业务逻辑（模拟 200ms）...")
	time.Sleep(200 * time.Millisecond)

	if err := l.Release(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "  Release 失败: %v\n", err)
		return
	}
	fmt.Println("  [1] 锁已释放")
	fmt.Println()
}

// ─────────────────────────────────────────────────────────────────────────────
// 场景 2：TryAcquire 快速失败
// ─────────────────────────────────────────────────────────────────────────────

func demoTryAcquireFail(client lock.Client) {
	fmt.Println("── 场景 2：TryAcquire 快速失败 ─────────────────────────────")

	lockName := "demo:inventory:ITEM-999"
	ctx := context.Background()

	// 实例 A 持有锁
	lA := client.New(lockName, lock.WithTTL(10*time.Second))
	if err := lA.Acquire(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "  A: Acquire 失败: %v\n", err)
		return
	}
	fmt.Printf("  [A] 已持有锁 %s\n", lockName)

	// 实例 B 尝试非阻塞获取
	lB := client.New(lockName)
	start := time.Now()
	err := lB.TryAcquire(ctx)
	elapsed := time.Since(start)

	if errors.Is(err, lock.ErrNotAcquired) {
		fmt.Printf("  [B] TryAcquire 立即返回 ErrNotAcquired（耗时 %v）\n", elapsed.Round(time.Millisecond))
	} else {
		fmt.Fprintf(os.Stderr, "  [B] 期望 ErrNotAcquired，实际: %v\n", err)
	}

	_ = lA.Release(ctx)
	fmt.Println("  [A] 已释放锁")
	fmt.Println()
}

// ─────────────────────────────────────────────────────────────────────────────
// 场景 3：Watchdog 自动续约
// ─────────────────────────────────────────────────────────────────────────────

func demoWatchdog(client lock.Client) {
	fmt.Println("── 场景 3：Watchdog 自动续约 ────────────────────────────────")

	const ttl = 2 * time.Second
	const holdTime = 5 * time.Second // 远超 TTL，没有 Watchdog 必定过期

	l := client.New("demo:report:RPT-2024",
		lock.WithTTL(ttl),
		lock.WithWatchdog(), // 每 TTL/3 ≈ 667ms 自动续约一次
	)

	ctx := context.Background()
	if err := l.Acquire(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "  Acquire 失败: %v\n", err)
		return
	}
	fmt.Printf("  锁 TTL=%v，Watchdog 已启动，持锁 %v（超过 TTL x2）...\n", ttl, holdTime)

	time.Sleep(holdTime)

	// 如果 Watchdog 工作正常，Release 应该返回 nil（锁未过期）
	if err := l.Release(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "  Release 失败（锁已过期！Watchdog 未生效）: %v\n", err)
		return
	}
	fmt.Printf("  持锁 %v 后 Release 成功，Watchdog 续约正常\n", holdTime)
	fmt.Println()
}

// ─────────────────────────────────────────────────────────────────────────────
// 场景 4：互斥保证——10 个 goroutine 并发写同一计数器
// ─────────────────────────────────────────────────────────────────────────────

func demoMutualExclusion(client lock.Client) {
	fmt.Println("── 场景 4：互斥保证（10 个 goroutine 并发写计数器）───────────")

	const goroutines = 10
	lockName := "demo:counter:CNT-01"
	var counter int64
	var wg sync.WaitGroup

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			l := client.New(lockName,
				lock.WithTTL(10*time.Second),
				lock.WithRetryCount(100),
				lock.WithRetryDelay(50*time.Millisecond),
			)

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			if err := l.Acquire(ctx); err != nil {
				fmt.Fprintf(os.Stderr, "  goroutine %d: Acquire 失败: %v\n", id, err)
				return
			}
			defer l.Release(context.Background())

			// 读取 → 暂停（放大竞争窗口）→ 写入
			v := atomic.LoadInt64(&counter)
			time.Sleep(time.Millisecond)
			atomic.StoreInt64(&counter, v+1)

			fmt.Printf("  goroutine %d 完成，计数器当前值: %d\n", id, atomic.LoadInt64(&counter))
		}(i)
	}

	wg.Wait()

	if counter == goroutines {
		fmt.Printf("  最终计数器 = %d（正确，互斥保证有效）\n", counter)
	} else {
		fmt.Fprintf(os.Stderr, "  最终计数器 = %d，期望 %d（互斥失败！）\n", counter, goroutines)
	}
	fmt.Println()
}

// ─────────────────────────────────────────────────────────────────────────────
// 场景 5：超时放弃——5 秒内未获取到锁则自动放弃
// ─────────────────────────────────────────────────────────────────────────────

func demoCtxTimeout(client lock.Client) {
	fmt.Println("── 场景 5：超时放弃（5s 内未拿到锁则放弃）─────────────────")

	lockName := "demo:payment:PAY-888"
	ctx := context.Background()

	// 实例 A 持有锁 10 秒，模拟长时间占锁的业务
	lA := client.New(lockName, lock.WithTTL(10*time.Second))
	if err := lA.Acquire(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "  A: Acquire 失败: %v\n", err)
		return
	}
	fmt.Println("  [A] 已持有锁，模拟长时间占锁（10s TTL）")

	// 实例 B：最多等 5 秒，超时后自动放弃
	// RetryDelay 设短一些让重试更频繁，RetryCount 足够大确保是 ctx 先超时
	lB := client.New(lockName,
		lock.WithRetryDelay(200*time.Millisecond),
		lock.WithRetryCount(1000),
	)

	timeoutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	fmt.Println("  [B] 开始等待获取锁（超时上限 5s）...")
	start := time.Now()
	err := lB.Acquire(timeoutCtx)
	elapsed := time.Since(start)

	switch {
	case errors.Is(err, context.DeadlineExceeded):
		fmt.Printf("  [B] 等待 %v 后超时放弃，收到 context.DeadlineExceeded\n",
			elapsed.Round(time.Millisecond))
		fmt.Println("  [B] 业务逻辑：记录日志，返回「稍后重试」提示给调用方")
	case err == nil:
		fmt.Println("  [B] 意外获取到锁（A 应仍在持有）")
		_ = lB.Release(ctx)
	default:
		fmt.Fprintf(os.Stderr, "  [B] 未预期的错误: %v\n", err)
	}

	_ = lA.Release(ctx)
	fmt.Println("  [A] 已释放锁")
	fmt.Println()
}
