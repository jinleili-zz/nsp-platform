// pkg/lock/lock.go
// Package lock provides a distributed lock SDK.
//
// Business code depends only on the Client and Lock interfaces,
// without importing any redsync or go-redis types.
// The underlying implementation (Redis, etcd, ZooKeeper, etc.) can be
// swapped out by changing only the NewXxxClient factory functions.
package lock

import (
	"context"
	"errors"
	"time"
)

// ErrNotAcquired is returned by Acquire or TryAcquire when the lock cannot
// be obtained because another node currently holds it.
var ErrNotAcquired = errors.New("lock not acquired")

// ErrLockExpired is returned by Release or Renew when the lock has already
// expired and no longer belongs to the current node.
var ErrLockExpired = errors.New("lock expired")

// Client is the distributed lock client interface.
//
// Different underlying implementations (redis/etcd/zk) provide different
// Client implementations. Business code should hold only a Client interface
// value and never depend on any concrete implementation type.
//
// Named Client rather than Factory to emphasise that it is a long-lived
// service client, not a one-shot object factory.
type Client interface {
	// New creates a named distributed lock.
	//
	// name is the globally unique identifier for the lock; locks with the
	// same name are mutually exclusive across the distributed system.
	//
	// Recommended naming convention: {domain}:{resource_type}:{resource_id}
	// e.g. "order:pay:ORD-123"
	//
	// The same name may be passed to New multiple times; each call returns
	// an independent Lock instance backed by its own token.
	New(name string, opts ...func(*LockOption)) Lock
}

// Lock is the distributed lock interface.
//
// Named Lock rather than Mutex to avoid conflating it with operating-system
// mutex semantics. Suitable for any underlying implementation.
type Lock interface {
	// Acquire blocks until the lock is obtained, ctx is cancelled, or ctx
	// times out.
	//
	// Internal retries follow LockOption.RetryCount and RetryDelay.
	// Returns ErrNotAcquired when all retries are exhausted.
	// Returns ctx.Err() when ctx is cancelled or deadline exceeded.
	Acquire(ctx context.Context) error

	// TryAcquire attempts to obtain the lock without blocking.
	// Returns immediately; never retries.
	// Returns ErrNotAcquired on failure.
	TryAcquire(ctx context.Context) error

	// Release releases the lock atomically.
	//
	// Only the holder (identified by its token) can release the lock,
	// preventing accidental deletion by another node.
	// Returns ErrLockExpired when the lock has already expired.
	Release(ctx context.Context) error

	// Renew resets the lock TTL back to its initial value.
	//
	// Returns ErrLockExpired when the lock has already expired.
	Renew(ctx context.Context) error

	// Name returns the lock's name as passed to Client.New.
	Name() string
}

// LockOption holds configuration for a single Lock instance.
type LockOption struct {
	// TTL is the automatic expiry duration for the lock.
	// Default: 8s
	TTL time.Duration

	// RetryCount is the maximum number of retries for Acquire.
	// TryAcquire always uses exactly 1 try regardless of this value.
	// Default: 32
	RetryCount int

	// RetryDelay is the base wait time between Acquire retries.
	//
	// Actual wait = RetryDelay + random jitter in [0, RetryDelay/2).
	// Jitter prevents thundering-herd when multiple nodes retry simultaneously.
	// Default: 100ms
	RetryDelay time.Duration

	// EnableWatchdog enables automatic lock renewal.
	//
	// When true: after a successful Acquire a background goroutine is
	// started that calls Renew every TTL/3. The goroutine stops
	// automatically when Release is called.
	//
	// When false: no automatic renewal; callers may call Renew manually.
	// Default: false
	EnableWatchdog bool
}

/*
Usage example:

  // Initialise once at service startup.
  client, err := lock.NewRedisClient(lock.RedisOption{
      Addrs:    []string{"redis-0:6379", "redis-1:6379", "redis-2:6379"},
      Password: "your-password",
  })
  if err != nil {
      panic(err)
  }

  // Create a lock (recommended naming: {domain}:{resource_type}:{resource_id}).
  l := client.New("order:pay:ORD-123",
      lock.WithTTL(10*time.Second),
      lock.WithWatchdog(),
  )

  // Acquire the lock.
  if err := l.Acquire(ctx); err != nil {
      if errors.Is(err, lock.ErrNotAcquired) {
          // Lock is held by another node – retry or return an error.
      }
      return err
  }
  defer l.Release(ctx)

  // Execute business logic.
  // With Watchdog enabled the lock is renewed automatically; no manual
  // Renew calls are needed even if the critical section takes longer than TTL.

  // Manual renewal example (when Watchdog is disabled):
  l2 := client.New("order:stock:ITEM-456",
      lock.WithTTL(5*time.Second),
  )
  if err := l2.Acquire(ctx); err != nil {
      return err
  }
  defer l2.Release(ctx)

  // Renew mid-way through the business logic.
  if err := l2.Renew(ctx); err != nil {
      if errors.Is(err, lock.ErrLockExpired) {
          // Lock expired – re-acquire or abort the current operation.
      }
  }
*/
