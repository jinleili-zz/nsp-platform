// pkg/lock/redis.go
//
// # Why ClusterClient instead of Redlock multi-node
//
// Redlock is designed for N completely independent Redis instances where
// quorum (N/2+1) is required to grant a lock, providing safety when an
// individual instance fails.
//
// Redis Cluster is a different topology: the entire cluster is a single
// logical Redis. Each key maps to exactly one hash-slot, which is owned by
// one primary node and replicated to its replicas. High-availability is
// achieved through automatic primary-replica failover, not through
// multi-master quorum.
//
// Therefore:
//   - Using Redlock against cluster nodes would be incorrect – those nodes
//     share the same slot space and the same key would only be reachable
//     through the coordinating node anyway.
//   - A single redis.ClusterClient handles topology discovery, slot routing,
//     failover, and connection pooling automatically.
//   - redsync.New(pool) is called with exactly one pool wrapping the
//     ClusterClient, not multiple pools.
//
// # Redis Cluster and database index
//
// Redis Cluster supports only DB 0. RedisOption intentionally omits a DB
// field to prevent misconfiguration.
package lock

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"sync"
	"time"

	"github.com/go-redsync/redsync/v4"
	goredis "github.com/go-redsync/redsync/v4/redis/goredis/v9"
	"github.com/jinleili-zz/nsp-platform/logger"
	"github.com/redis/go-redis/v9"
)

var clusterConnectivityCheck = pingRedisConnectivity

// RedisOption holds configuration for the Redis Cluster client.
type RedisOption struct {
	// Addrs is the list of Redis Cluster node addresses.
	//
	// At least one address is required; go-redis will discover the remaining
	// nodes automatically. Providing all known nodes improves initialisation
	// resilience.
	// Example: []string{"redis-0:6379", "redis-1:6379", "redis-2:6379"}
	Addrs []string

	// Username is the Redis ACL username. Leave empty to use password-only auth.
	Username string

	// Password is the Redis AUTH password. Leave empty when no password is set.
	Password string

	// PoolSize is the number of connections per node. Default: 10.
	PoolSize int

	// DialTimeout is the connection establishment timeout. Default: 5s.
	DialTimeout time.Duration

	// ReadTimeout is the per-command read timeout. Default: 3s.
	ReadTimeout time.Duration

	// WriteTimeout is the per-command write timeout. Default: 3s.
	WriteTimeout time.Duration

	// RouteByLatency routes read commands to the lowest-latency node when true.
	// Default: false.
	RouteByLatency bool

	// RouteRandomly routes read commands randomly across primary and replica
	// nodes to distribute read load when true.
	//
	// When both RouteByLatency and RouteRandomly are true, RouteByLatency
	// takes precedence.
	// Default: false.
	RouteRandomly bool
}

// NewRedisClient creates a Client backed by redsync + Redis Cluster.
//
// A Redis PING is performed to validate connectivity before returning.
// Returns an error if opt.Addrs is empty or the cluster is unreachable.
func NewRedisClient(opt RedisOption) (Client, error) {
	clusterOpt, err := newClusterOptions(opt)
	if err != nil {
		return nil, err
	}

	clusterClient := redis.NewClusterClient(clusterOpt)

	// Validate connectivity with a bounded timeout.
	checkCtx, cancel := context.WithTimeout(context.Background(), clusterOpt.DialTimeout)
	defer cancel()
	if err := clusterConnectivityCheck(checkCtx, clusterClient); err != nil {
		_ = clusterClient.Close()
		return nil, fmt.Errorf("lock: redis cluster unreachable: %w", err)
	}

	pool := goredis.NewPool(clusterClient)
	rs := redsync.New(pool)

	return &redisClient{rs: rs, closer: clusterClient}, nil
}

type redisPinger interface {
	Ping(ctx context.Context) *redis.StatusCmd
}

func pingRedisConnectivity(ctx context.Context, client redisPinger) error {
	return client.Ping(ctx).Err()
}

func newClusterOptions(opt RedisOption) (*redis.ClusterOptions, error) {
	if len(opt.Addrs) == 0 {
		return nil, fmt.Errorf("lock: RedisOption.Addrs must not be empty")
	}

	if opt.PoolSize == 0 {
		opt.PoolSize = 10
	}
	if opt.DialTimeout == 0 {
		opt.DialTimeout = 5 * time.Second
	}
	if opt.ReadTimeout == 0 {
		opt.ReadTimeout = 3 * time.Second
	}
	if opt.WriteTimeout == 0 {
		opt.WriteTimeout = 3 * time.Second
	}

	return &redis.ClusterOptions{
		Addrs:          opt.Addrs,
		Username:       opt.Username,
		Password:       opt.Password,
		PoolSize:       opt.PoolSize,
		DialTimeout:    opt.DialTimeout,
		ReadTimeout:    opt.ReadTimeout,
		WriteTimeout:   opt.WriteTimeout,
		RouteByLatency: opt.RouteByLatency,
		RouteRandomly:  opt.RouteRandomly,
	}, nil
}

// redisClient implements Client using redsync.
type redisClient struct {
	rs     *redsync.Redsync
	closer io.Closer // underlying Redis client (ClusterClient or Client)
}

// New creates a named distributed Lock.
//
// Applies defaultLockOption first, then each option in opts in order.
func (c *redisClient) New(name string, opts ...func(*LockOption)) Lock {
	opt := defaultLockOption()
	for _, fn := range opts {
		fn(&opt)
	}

	// Build redsync options from LockOption.
	rsOpts := []redsync.Option{
		redsync.WithExpiry(opt.TTL),
		redsync.WithTries(opt.RetryCount),
		redsync.WithRetryDelayFunc(func(tries int) time.Duration {
			jitter := time.Duration(rand.Int63n(int64(opt.RetryDelay / 2)))
			return opt.RetryDelay + jitter
		}),
	}

	mu := c.rs.NewMutex(name, rsOpts...)
	return &redisLock{
		mu:  mu,
		opt: opt,
		rs:  c.rs,
	}
}

// Close releases the underlying Redis connection pool.
func (c *redisClient) Close() error {
	if c.closer != nil {
		return c.closer.Close()
	}
	return nil
}

type redisMutex interface {
	Name() string
	LockContext(ctx context.Context) error
	UnlockContext(ctx context.Context) (bool, error)
	ExtendContext(ctx context.Context) (bool, error)
}

// redisLock implements Lock using a redsync.Mutex.
type redisLock struct {
	muMu sync.Mutex       // protects mu and wd fields
	mu   redisMutex       // underlying redsync lock instance
	wd   *watchdog        // non-nil when auto-renewal is active
	opt  LockOption       // options supplied at creation time
	rs   *redsync.Redsync // used by TryAcquire to create a temporary instance
}

// startWatchdog starts the watchdog goroutine if EnableWatchdog is true.
// Must be called with muMu held.
func (l *redisLock) startWatchdog() {
	if !l.opt.EnableWatchdog {
		return
	}
	// Stop an existing watchdog if Acquire is called again without Release.
	if l.wd != nil {
		l.wd.Stop()
		l.wd = nil
	}
	interval := watchdogInterval(l.opt.TTL)
	l.wd = newWatchdog(interval, func() error {
		return l.Renew(context.Background())
	}, func(format string, args ...any) {
		logger.Warn(fmt.Sprintf(format, args...))
	})
}

// Acquire blocks until the lock is obtained or ctx is done.
func (l *redisLock) Acquire(ctx context.Context) error {
	l.muMu.Lock()
	mu := l.mu
	l.muMu.Unlock()

	if err := mu.LockContext(ctx); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrNotAcquired
	}

	l.muMu.Lock()
	l.startWatchdog()
	l.muMu.Unlock()

	return nil
}

// TryAcquire attempts to obtain the lock without blocking.
//
// A temporary redsync.Mutex with tries=1 is used so that exactly one
// attempt is made. On success the internal mu is replaced with the
// temporary instance because the lock token (random value) is stored
// inside the redsync.Mutex; subsequent Release/Renew calls must use the
// same token to satisfy the anti-deletion guarantee.
func (l *redisLock) TryAcquire(ctx context.Context) error {
	tempMu := l.rs.NewMutex(l.mu.Name(),
		redsync.WithTries(1),
		redsync.WithExpiry(l.opt.TTL),
	)

	if err := tempMu.LockContext(ctx); err != nil {
		return ErrNotAcquired
	}

	// Replace mu atomically so future Release/Renew use the new token.
	l.muMu.Lock()
	l.mu = tempMu
	l.startWatchdog()
	l.muMu.Unlock()

	return nil
}

// Release releases the lock.
func (l *redisLock) Release(ctx context.Context) error {
	l.muMu.Lock()
	wd := l.wd
	l.wd = nil
	mu := l.mu
	l.muMu.Unlock()

	if wd != nil {
		wd.Stop()
	}

	ok, err := mu.UnlockContext(ctx)
	if err != nil {
		if errors.Is(err, redsync.ErrLockAlreadyExpired) {
			return ErrLockExpired
		}
		return err
	}
	if !ok {
		return ErrLockExpired
	}
	return nil
}

// Renew resets the lock TTL back to its initial value.
func (l *redisLock) Renew(ctx context.Context) error {
	l.muMu.Lock()
	mu := l.mu
	l.muMu.Unlock()

	ok, err := mu.ExtendContext(ctx)
	if err != nil {
		var takenErr *redsync.ErrTaken
		if errors.Is(err, redsync.ErrExtendFailed) || errors.As(err, &takenErr) {
			return ErrLockExpired
		}
		return err
	}
	if !ok {
		return ErrLockExpired
	}
	return nil
}

// Name returns the lock's name.
func (l *redisLock) Name() string {
	l.muMu.Lock()
	defer l.muMu.Unlock()
	return l.mu.Name()
}

// ─────────────────────────────────────────────────────────────────────────────
// watchdog – automatic lock renewal
// ─────────────────────────────────────────────────────────────────────────────

// watchdog automatically renews a lock at a fixed interval.
type watchdog struct {
	stop chan struct{}
	done chan struct{}
}

// newWatchdog starts a background goroutine that calls renewFn every interval.
//
// logFn is used to report renewal failures. Renewal failures do not stop
// the goroutine; the next tick will retry. The goroutine exits when Stop
// is called.
func newWatchdog(interval time.Duration, renewFn func() error, logFn func(string, ...any)) *watchdog {
	w := &watchdog{
		stop: make(chan struct{}),
		done: make(chan struct{}),
	}
	go func() {
		for {
			select {
			case <-w.stop:
				close(w.done)
				return
			case <-time.After(interval):
				if err := renewFn(); err != nil {
					logFn("lock watchdog: renew failed: %v", err)
					// Do not exit; attempt again on the next tick.
				}
			}
		}
	}()
	return w
}

func watchdogInterval(ttl time.Duration) time.Duration {
	interval := ttl / 3
	if interval > 0 {
		return interval
	}

	interval = ttl / 2
	if interval > 0 {
		return interval
	}

	if ttl > 0 {
		return ttl
	}

	return time.Millisecond
}

// Stop halts the renewal goroutine and waits for it to exit completely.
func (w *watchdog) Stop() {
	close(w.stop)
	<-w.done
}

// ─────────────────────────────────────────────────────────────────────────────
// Standalone Redis client (single-node, for development and demo use)
// ─────────────────────────────────────────────────────────────────────────────

// StandaloneRedisOption holds configuration for a single-node Redis client.
//
// Intended for local development and demo scenarios where a Redis Cluster is
// not available. For production use, prefer NewRedisClient with a Redis
// Cluster.
type StandaloneRedisOption struct {
	// Addr is the Redis server address, e.g. "localhost:6379".
	Addr string

	// Username is the Redis ACL username. Leave empty to use password-only auth.
	Username string

	// Password is the Redis AUTH password. Leave empty when no password is set.
	Password string

	// PoolSize is the size of the connection pool. Default: 10.
	PoolSize int

	// DialTimeout is the connection establishment timeout. Default: 5s.
	DialTimeout time.Duration

	// ReadTimeout is the per-command read timeout. Default: 3s.
	ReadTimeout time.Duration

	// WriteTimeout is the per-command write timeout. Default: 3s.
	WriteTimeout time.Duration
}

// NewStandaloneRedisClient creates a Client backed by redsync + a single Redis
// node. A Ping is performed to validate connectivity before returning.
//
// Use this for local development and demos only. For production, use
// NewRedisClient which connects to a Redis Cluster.
func NewStandaloneRedisClient(opt StandaloneRedisOption) (Client, error) {
	redisOpt, err := newStandaloneOptions(opt)
	if err != nil {
		return nil, err
	}

	rdb := redis.NewClient(redisOpt)

	// Validate connectivity with a bounded timeout.
	checkCtx, cancel := context.WithTimeout(context.Background(), redisOpt.DialTimeout)
	defer cancel()
	if err := rdb.Ping(checkCtx).Err(); err != nil {
		_ = rdb.Close()
		return nil, fmt.Errorf("lock: redis unreachable: %w", err)
	}

	pool := goredis.NewPool(rdb)
	rs := redsync.New(pool)

	return &redisClient{rs: rs, closer: rdb}, nil
}

func newStandaloneOptions(opt StandaloneRedisOption) (*redis.Options, error) {
	if opt.Addr == "" {
		return nil, fmt.Errorf("lock: StandaloneRedisOption.Addr must not be empty")
	}

	if opt.PoolSize == 0 {
		opt.PoolSize = 10
	}
	if opt.DialTimeout == 0 {
		opt.DialTimeout = 5 * time.Second
	}
	if opt.ReadTimeout == 0 {
		opt.ReadTimeout = 3 * time.Second
	}
	if opt.WriteTimeout == 0 {
		opt.WriteTimeout = 3 * time.Second
	}

	return &redis.Options{
		Addr:         opt.Addr,
		Username:     opt.Username,
		Password:     opt.Password,
		PoolSize:     opt.PoolSize,
		DialTimeout:  opt.DialTimeout,
		ReadTimeout:  opt.ReadTimeout,
		WriteTimeout: opt.WriteTimeout,
	}, nil
}
