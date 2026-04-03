// pkg/lock/lock_test.go
//
// # Test strategy: single-node miniredis instead of Redis Cluster
//
// miniredis (github.com/alicebob/miniredis/v2) does not implement the Redis
// Cluster protocol. Tests therefore use a single-node miniredis instance
// together with redis.NewClient (plain client, not ClusterClient).
//
// The test helper constructs a redisClient directly, bypassing the
// NewRedisClient factory's startup connectivity check.
//
// This approach is intentional:
//   - All Lock interface semantics (Acquire, TryAcquire, Release, Renew,
//     Watchdog, mutual exclusion) are exercised by redsync's logic, which
//     is independent of the Redis topology.
//   - Redis Cluster routing, slot assignment, and failover are the
//     responsibility of go-redis and are tested in their own test suite.
//   - miniredis.FastForward lets us simulate TTL expiry without real sleeps,
//     keeping tests fast and deterministic.
package lock

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redsync/redsync/v4"
	goredis "github.com/go-redsync/redsync/v4/redis/goredis/v9"
	"github.com/redis/go-redis/v9"
)

// ─────────────────────────────────────────────────────────────────────────────
// Test helpers
// ─────────────────────────────────────────────────────────────────────────────

type fakeMutex struct {
	name      string
	lockErr   error
	unlockOK  bool
	unlockErr error
	extendOK  bool
	extendErr error
	lockCalls int
}

func (m *fakeMutex) Name() string {
	return m.name
}

func (m *fakeMutex) LockContext(context.Context) error {
	m.lockCalls++
	return m.lockErr
}

func (m *fakeMutex) UnlockContext(context.Context) (bool, error) {
	return m.unlockOK, m.unlockErr
}

func (m *fakeMutex) ExtendContext(context.Context) (bool, error) {
	return m.extendOK, m.extendErr
}

type fakePinger struct {
	pingCalls int
	pingErr   error
}

func (p *fakePinger) Ping(ctx context.Context) *redis.StatusCmd {
	p.pingCalls++
	cmd := redis.NewStatusCmd(ctx, "ping")
	cmd.SetErr(p.pingErr)
	return cmd
}

// newTestClient starts a miniredis instance and returns a Client backed by it.
// The miniredis server is stopped automatically via t.Cleanup.
func newTestClient(t *testing.T) Client {
	t.Helper()
	mr, client := getMiniRedis(t)
	_ = mr
	return client
}

// getMiniRedis starts a miniredis instance and returns both the server (for
// FastForward control) and the Client.
func getMiniRedis(t *testing.T) (*miniredis.Miniredis, Client) {
	t.Helper()

	mr := miniredis.RunT(t)

	rdb := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})

	pool := goredis.NewPool(rdb)
	rs := redsync.New(pool)

	c := &redisClient{
		rs:     rs,
		closer: nil, // no real client to close for single-node tests
	}
	return mr, c
}

// uniqueName returns a lock name that is unique within the current test to
// prevent cross-test interference.
func uniqueName(t *testing.T, suffix string) string {
	t.Helper()
	return "test:" + t.Name() + ":" + suffix
}

// ─────────────────────────────────────────────────────────────────────────────
// Basic functionality
// ─────────────────────────────────────────────────────────────────────────────

// TestAcquire_Success verifies that a lock can be acquired and then released
// without errors.
func TestAcquire_Success(t *testing.T) {
	client := newTestClient(t)
	l := client.New(uniqueName(t, "lock"))

	if err := l.Acquire(context.Background()); err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	if err := l.Release(context.Background()); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
}

// TestAcquire_Blocking verifies that a second Acquire blocks until the first
// holder calls Release.
func TestAcquire_Blocking(t *testing.T) {
	client := newTestClient(t)
	name := uniqueName(t, "lock")

	lA := client.New(name)
	lB := client.New(name, WithRetryDelay(20*time.Millisecond))

	// A acquires the lock first.
	if err := lA.Acquire(context.Background()); err != nil {
		t.Fatalf("A: Acquire() error = %v", err)
	}

	acquired := make(chan error, 1)
	go func() {
		acquired <- lB.Acquire(context.Background())
	}()

	// Give B time to block.
	time.Sleep(50 * time.Millisecond)

	select {
	case err := <-acquired:
		t.Fatalf("B should still be blocked, got result: %v", err)
	default:
	}

	// Release A; B should now acquire.
	if err := lA.Release(context.Background()); err != nil {
		t.Fatalf("A: Release() error = %v", err)
	}

	select {
	case err := <-acquired:
		if err != nil {
			t.Fatalf("B: Acquire() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("B: Acquire() did not unblock within 5s")
	}

	_ = lB.Release(context.Background())
}

// TestTryAcquire_Success verifies that TryAcquire succeeds when no other node
// holds the lock.
func TestTryAcquire_Success(t *testing.T) {
	client := newTestClient(t)
	l := client.New(uniqueName(t, "lock"))

	if err := l.TryAcquire(context.Background()); err != nil {
		t.Fatalf("TryAcquire() error = %v", err)
	}
	_ = l.Release(context.Background())
}

// TestTryAcquire_Fail verifies that TryAcquire returns ErrNotAcquired
// immediately when another node holds the lock.
func TestTryAcquire_Fail(t *testing.T) {
	client := newTestClient(t)
	name := uniqueName(t, "lock")

	lA := client.New(name)
	lB := client.New(name)

	if err := lA.Acquire(context.Background()); err != nil {
		t.Fatalf("A: Acquire() error = %v", err)
	}
	defer lA.Release(context.Background())

	err := lB.TryAcquire(context.Background())
	if !errors.Is(err, ErrNotAcquired) {
		t.Fatalf("B: TryAcquire() error = %v, want ErrNotAcquired", err)
	}
}

// TestRelease_LockExpired verifies that releasing an expired lock returns
// ErrLockExpired.
func TestRelease_LockExpired(t *testing.T) {
	mr, client := getMiniRedis(t)
	l := client.New(uniqueName(t, "lock"), WithTTL(500*time.Millisecond))

	if err := l.Acquire(context.Background()); err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}

	mr.FastForward(600 * time.Millisecond)

	err := l.Release(context.Background())
	if !errors.Is(err, ErrLockExpired) {
		t.Fatalf("Release() error = %v, want ErrLockExpired", err)
	}
}

// TestRelease_PreservesTransportError verifies that Release does not collapse
// context or transport failures into ErrLockExpired.
func TestRelease_PreservesTransportError(t *testing.T) {
	mu := &fakeMutex{
		name:      uniqueName(t, "lock"),
		unlockErr: context.Canceled,
	}
	l := &redisLock{mu: mu}

	err := l.Release(context.Background())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Release() error = %v, want context.Canceled", err)
	}
}

// TestRenew_Success verifies that Renew extends the TTL so the lock is still
// valid after the original TTL would have expired.
func TestRenew_Success(t *testing.T) {
	mr, client := getMiniRedis(t)
	l := client.New(uniqueName(t, "lock"), WithTTL(2*time.Second))

	if err := l.Acquire(context.Background()); err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}

	if err := l.Renew(context.Background()); err != nil {
		t.Fatalf("Renew() error = %v", err)
	}

	// After renew the TTL is reset to 2s; advance 1500ms – lock should remain.
	mr.FastForward(1500 * time.Millisecond)

	if err := l.Release(context.Background()); err != nil {
		t.Fatalf("Release() after Renew error = %v (lock should still be valid)", err)
	}
}

// TestRenew_LockExpired verifies that Renew returns ErrLockExpired when the
// lock has already expired.
func TestRenew_LockExpired(t *testing.T) {
	mr, client := getMiniRedis(t)
	l := client.New(uniqueName(t, "lock"), WithTTL(500*time.Millisecond))

	if err := l.Acquire(context.Background()); err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}

	mr.FastForward(600 * time.Millisecond)

	err := l.Renew(context.Background())
	if !errors.Is(err, ErrLockExpired) {
		t.Fatalf("Renew() error = %v, want ErrLockExpired", err)
	}
}

// TestRenew_PreservesTransportError verifies that Renew does not collapse
// context or transport failures into ErrLockExpired.
func TestRenew_PreservesTransportError(t *testing.T) {
	mu := &fakeMutex{
		name:      uniqueName(t, "lock"),
		extendErr: context.DeadlineExceeded,
	}
	l := &redisLock{mu: mu}

	err := l.Renew(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Renew() error = %v, want context.DeadlineExceeded", err)
	}
}

// TestName_Returns verifies that Name() returns the value passed to New.
func TestName_Returns(t *testing.T) {
	client := newTestClient(t)
	name := uniqueName(t, "my-lock")
	l := client.New(name)

	if got := l.Name(); got != name {
		t.Fatalf("Name() = %q, want %q", got, name)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Error handling
// ─────────────────────────────────────────────────────────────────────────────

// TestAcquire_CtxCanceled verifies that Acquire returns context.Canceled (not
// ErrNotAcquired) when the context is already cancelled.
func TestAcquire_CtxCanceled(t *testing.T) {
	client := newTestClient(t)
	l := client.New(uniqueName(t, "lock"))

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err := l.Acquire(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Acquire() error = %v, want context.Canceled", err)
	}
}

// TestAcquire_CtxTimeout verifies that Acquire returns context.DeadlineExceeded
// when the context deadline is reached before RetryCount is exhausted.
func TestAcquire_CtxTimeout(t *testing.T) {
	client := newTestClient(t)
	name := uniqueName(t, "lock")

	// A holds the lock indefinitely.
	lA := client.New(name)
	if err := lA.Acquire(context.Background()); err != nil {
		t.Fatalf("A: Acquire() error = %v", err)
	}
	defer lA.Release(context.Background())

	// B has a large retry budget but a tight context deadline.
	lB := client.New(name,
		WithRetryCount(100),
		WithRetryDelay(time.Second),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := lB.Acquire(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("B: Acquire() error = %v, want context.DeadlineExceeded", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Option validation
// ─────────────────────────────────────────────────────────────────────────────

// TestNewRedisClient_UsernameField verifies that Redis ACL credentials are
// propagated into the underlying cluster client options.
func TestNewRedisClient_UsernameField(t *testing.T) {
	opt, err := newClusterOptions(RedisOption{
		Addrs:    []string{"127.0.0.1:6379"},
		Username: "svc-lock",
		Password: "secret",
	})
	if err != nil {
		t.Fatalf("newClusterOptions() error = %v", err)
	}

	if got := opt.Username; got != "svc-lock" {
		t.Fatalf("ClusterOptions.Username = %q, want %q", got, "svc-lock")
	}
	if got := opt.Password; got != "secret" {
		t.Fatalf("ClusterOptions.Password = %q, want %q", got, "secret")
	}
}

// TestPingRedisConnectivity verifies that the cluster startup probe uses a
// plain Redis PING and surfaces its result.
func TestPingRedisConnectivity(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		pinger := &fakePinger{}
		if err := pingRedisConnectivity(context.Background(), pinger); err != nil {
			t.Fatalf("pingRedisConnectivity() error = %v", err)
		}
		if pinger.pingCalls != 1 {
			t.Fatalf("Ping() calls = %d, want 1", pinger.pingCalls)
		}
	})

	t.Run("failure", func(t *testing.T) {
		pinger := &fakePinger{pingErr: context.DeadlineExceeded}
		err := pingRedisConnectivity(context.Background(), pinger)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("pingRedisConnectivity() error = %v, want context.DeadlineExceeded", err)
		}
	})
}

// TestNewRedisClient_UsesConnectivityCheck verifies that NewRedisClient
// delegates startup validation to the injectable connectivity probe.
func TestNewRedisClient_UsesConnectivityCheck(t *testing.T) {
	origCheck := clusterConnectivityCheck
	t.Cleanup(func() {
		clusterConnectivityCheck = origCheck
	})

	var calls int
	clusterConnectivityCheck = func(ctx context.Context, client redisPinger) error {
		calls++
		return nil
	}

	client, err := NewRedisClient(RedisOption{
		Addrs:       []string{"127.0.0.1:6379"},
		DialTimeout: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewRedisClient() error = %v", err)
	}
	defer client.Close()

	if calls != 1 {
		t.Fatalf("connectivity check calls = %d, want 1", calls)
	}
}

// TestNewStandaloneRedisClient_UsernameField verifies that ACL credentials are
// passed through to the standalone go-redis client and accepted by Redis.
func TestNewStandaloneRedisClient_UsernameField(t *testing.T) {
	mr := miniredis.RunT(t)
	mr.RequireUserAuth("svc-lock", "secret")

	client, err := NewStandaloneRedisClient(StandaloneRedisOption{
		Addr:     mr.Addr(),
		Username: "svc-lock",
		Password: "secret",
	})
	if err != nil {
		t.Fatalf("NewStandaloneRedisClient() error = %v", err)
	}
	defer client.Close()

	rc, ok := client.(*redisClient)
	if !ok {
		t.Fatalf("client type = %T, want *redisClient", client)
	}

	rdb, ok := rc.closer.(*redis.Client)
	if !ok {
		t.Fatalf("closer type = %T, want *redis.Client", rc.closer)
	}

	opt := rdb.Options()
	if got := opt.Username; got != "svc-lock" {
		t.Fatalf("Options.Username = %q, want %q", got, "svc-lock")
	}
	if got := opt.Password; got != "secret" {
		t.Fatalf("Options.Password = %q, want %q", got, "secret")
	}
}

// TestNewStandaloneRedisClient_AuthErrorWrapped verifies that ACL auth
// failures are returned as wrapped errors rather than panics or silent
// failures.
func TestNewStandaloneRedisClient_AuthErrorWrapped(t *testing.T) {
	mr := miniredis.RunT(t)
	mr.RequireUserAuth("svc-lock", "secret")

	_, err := NewStandaloneRedisClient(StandaloneRedisOption{
		Addr:        mr.Addr(),
		Username:    "svc-lock",
		Password:    "wrong-secret",
		DialTimeout: time.Second,
	})
	if err == nil {
		t.Fatal("NewStandaloneRedisClient() error = nil, want wrapped auth error")
	}

	cause := errors.Unwrap(err)
	if cause == nil {
		t.Fatalf("errors.Unwrap(err) = nil, want underlying auth error; err=%v", err)
	}
	if !strings.Contains(err.Error(), cause.Error()) {
		t.Fatalf("wrapped error %q does not include underlying error %q", err.Error(), cause.Error())
	}
}

// TestWithTTL verifies that a short TTL causes the lock to expire, allowing
// another Acquire to succeed afterwards.
func TestWithTTL(t *testing.T) {
	mr, client := getMiniRedis(t)
	name := uniqueName(t, "lock")

	lA := client.New(name, WithTTL(500*time.Millisecond))
	if err := lA.Acquire(context.Background()); err != nil {
		t.Fatalf("A: Acquire() error = %v", err)
	}

	mr.FastForward(600 * time.Millisecond)

	// The old lock has expired; a new acquire should succeed immediately.
	lB := client.New(name, WithTTL(500*time.Millisecond))
	if err := lB.TryAcquire(context.Background()); err != nil {
		t.Fatalf("B: TryAcquire() after TTL expiry error = %v", err)
	}
	_ = lB.Release(context.Background())
}

// TestWithRetryCount verifies that Acquire fails quickly when RetryCount is
// small and the elapsed time is proportional to RetryCount * RetryDelay.
func TestWithRetryCount(t *testing.T) {
	client := newTestClient(t)
	name := uniqueName(t, "lock")

	lA := client.New(name)
	if err := lA.Acquire(context.Background()); err != nil {
		t.Fatalf("A: Acquire() error = %v", err)
	}
	defer lA.Release(context.Background())

	const retries = 2
	const delay = 10 * time.Millisecond

	lB := client.New(name,
		WithRetryCount(retries),
		WithRetryDelay(delay),
	)

	start := time.Now()
	err := lB.Acquire(context.Background())
	elapsed := time.Since(start)

	if !errors.Is(err, ErrNotAcquired) {
		t.Fatalf("B: Acquire() error = %v, want ErrNotAcquired", err)
	}

	// Expect elapsed ≈ retries * delay; allow generous upper bound for CI.
	maxExpected := time.Duration(retries)*delay*4 + 200*time.Millisecond
	if elapsed > maxExpected {
		t.Fatalf("B: Acquire() took %v, expected at most %v", elapsed, maxExpected)
	}
}

// TestWithWatchdog verifies that the watchdog keeps the lock alive past its
// original TTL, and that Release properly stops the watchdog.
func TestWithWatchdog(t *testing.T) {
	mr, client := getMiniRedis(t)
	name := uniqueName(t, "lock")

	l := client.New(name,
		WithTTL(time.Second),
		WithWatchdog(),
	)

	if err := l.Acquire(context.Background()); err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}

	// Sleep longer than TTL – watchdog should have renewed several times.
	time.Sleep(2500 * time.Millisecond)

	// Lock should still be valid.
	if err := l.Release(context.Background()); err != nil {
		t.Fatalf("Release() after watchdog renewals error = %v (expected lock still valid)", err)
	}

	// After Release, watchdog is stopped. FastForward past TTL and verify a
	// fresh Acquire succeeds (proving the key has truly expired).
	time.Sleep(200 * time.Millisecond)
	mr.FastForward(1500 * time.Millisecond)

	l2 := client.New(name, WithTTL(time.Second))
	if err := l2.TryAcquire(context.Background()); err != nil {
		t.Fatalf("TryAcquire() after Release + expiry error = %v", err)
	}
	_ = l2.Release(context.Background())
}

// TestWithWatchdog_SubSecondTTL verifies that watchdog renewals still happen
// before the original lease expires when TTL is below one second.
func TestWithWatchdog_SubSecondTTL(t *testing.T) {
	client := newTestClient(t)
	name := uniqueName(t, "lock")

	l1 := client.New(name,
		WithTTL(500*time.Millisecond),
		WithWatchdog(),
	)
	if err := l1.Acquire(context.Background()); err != nil {
		t.Fatalf("l1.Acquire() error = %v", err)
	}

	time.Sleep(650 * time.Millisecond)

	l2 := client.New(name, WithTTL(500*time.Millisecond))
	err := l2.TryAcquire(context.Background())
	if !errors.Is(err, ErrNotAcquired) {
		t.Fatalf("l2.TryAcquire() error = %v, want ErrNotAcquired while watchdog is renewing", err)
	}

	if err := l1.Release(context.Background()); err != nil {
		t.Fatalf("l1.Release() error = %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Mutual exclusion (core correctness test)
// ─────────────────────────────────────────────────────────────────────────────

// TestMutualExclusion verifies that the distributed lock correctly serialises
// concurrent access to a shared resource.
//
// 10 goroutines compete for the same lock. Each goroutine performs an
// unsynchronised read-increment-write on a shared counter with a 1ms sleep
// between read and write to enlarge the contention window. Without the lock
// the final count would be non-deterministic; with the lock it must equal 10.
func TestMutualExclusion(t *testing.T) {
	client := newTestClient(t)
	name := uniqueName(t, "lock")

	const goroutines = 10
	var counter int64
	var wg sync.WaitGroup

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			l := client.New(name,
				WithRetryDelay(20*time.Millisecond),
				WithRetryCount(200),
			)

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			if err := l.Acquire(ctx); err != nil {
				t.Errorf("Acquire() error = %v", err)
				return
			}
			defer l.Release(context.Background())

			// Read current value.
			v := atomic.LoadInt64(&counter)

			// Simulate work – enlarge contention window.
			time.Sleep(time.Millisecond)

			// Write incremented value.
			atomic.StoreInt64(&counter, v+1)
		}()
	}

	wg.Wait()

	if counter != goroutines {
		t.Fatalf("counter = %d, want %d (mutual exclusion violated)", counter, goroutines)
	}
}
