// pkg/lock/option.go
// Package lock – functional options for LockOption.
package lock

import "time"

// defaultLockOption returns a LockOption pre-filled with sensible defaults.
func defaultLockOption() LockOption {
	return LockOption{
		TTL:            8 * time.Second,
		RetryCount:     32,
		RetryDelay:     100 * time.Millisecond,
		EnableWatchdog: false,
	}
}

// WithTTL sets the lock's automatic expiry duration.
//
// Choose a TTL long enough to cover the expected critical-section duration.
// Enable WithWatchdog if the duration is variable or unbounded.
func WithTTL(d time.Duration) func(*LockOption) {
	return func(o *LockOption) {
		o.TTL = d
	}
}

// WithRetryCount sets the maximum number of Acquire retries.
//
// TryAcquire always uses exactly one try regardless of this value.
func WithRetryCount(n int) func(*LockOption) {
	return func(o *LockOption) {
		o.RetryCount = n
	}
}

// WithRetryDelay sets the base wait duration between consecutive Acquire retries.
//
// The actual wait per retry is RetryDelay + random jitter in [0, RetryDelay/2)
// to reduce thundering-herd contention.
func WithRetryDelay(d time.Duration) func(*LockOption) {
	return func(o *LockOption) {
		o.RetryDelay = d
	}
}

// WithWatchdog enables automatic lock renewal.
//
// A background goroutine will call Renew every TTL/3 after a successful
// Acquire. The goroutine is stopped automatically by Release.
//
// Use this option when the critical-section duration is unknown or may
// exceed the configured TTL.
func WithWatchdog() func(*LockOption) {
	return func(o *LockOption) {
		o.EnableWatchdog = true
	}
}
