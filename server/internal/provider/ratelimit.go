// Package provider — shared resilience primitives for external API providers.
//
// RateLimiter wraps golang.org/x/time/rate with an additional hourly quota.
// CircuitBreaker wraps sony/gobreaker with structured logging.
//
// Any provider (Name.com, Neon, Resend, future Dynadot/Porkbun, etc.)
// can compose these into its struct for automatic rate limiting and
// circuit breaking against external APIs.
package provider

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/sony/gobreaker/v2"
	"golang.org/x/time/rate"
)

// ═══════════════════════════════════════════════════════════════════
// RateLimiter — golang.org/x/time/rate token bucket + hourly quota
// ═══════════════════════════════════════════════════════════════════

// RateLimiterConfig configures a rate limiter.
type RateLimiterConfig struct {
	PerSecond int // max requests per second (token bucket)
	PerHour   int // max requests per hour (sliding window, 0 = unlimited)
}

// RateLimiter enforces per-second and per-hour request rate limits.
// The per-second bucket is handled by x/time/rate (lock-free, battle-tested).
// The hourly window is a simple counter reset every hour.
type RateLimiter struct {
	limiter *rate.Limiter

	mu          sync.Mutex
	hourlyCount int
	hourlyMax   int
	hourlyReset time.Time
}

// NewRateLimiter creates a rate limiter with the given limits.
func NewRateLimiter(cfg RateLimiterConfig) *RateLimiter {
	rl := &RateLimiter{
		limiter:     rate.NewLimiter(rate.Limit(cfg.PerSecond), cfg.PerSecond),
		hourlyMax:   cfg.PerHour,
		hourlyReset: time.Now().Add(time.Hour),
	}
	return rl
}

// Wait blocks until a request slot is available or ctx is cancelled.
func (rl *RateLimiter) Wait(ctx context.Context) error {
	// Check hourly quota first (cheap mutex check).
	if rl.hourlyMax > 0 {
		rl.mu.Lock()
		now := time.Now()
		if now.After(rl.hourlyReset) {
			rl.hourlyCount = 0
			rl.hourlyReset = now.Add(time.Hour)
		}
		if rl.hourlyCount >= rl.hourlyMax {
			waitUntil := rl.hourlyReset
			rl.mu.Unlock()
			// Block until hourly reset or context cancellation.
			select {
			case <-ctx.Done():
				return fmt.Errorf("rate limit wait cancelled: %w", ctx.Err())
			case <-time.After(time.Until(waitUntil)):
				return rl.Wait(ctx) // retry after reset
			}
		}
		rl.hourlyCount++
		rl.mu.Unlock()
	}

	// Per-second token bucket (x/time/rate handles blocking + context).
	if err := rl.limiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limit wait cancelled: %w", err)
	}
	return nil
}

// ═══════════════════════════════════════════════════════════════════
// CircuitBreaker — sony/gobreaker wrapper with structured logging
// ═══════════════════════════════════════════════════════════════════

// CircuitBreakerConfig configures a circuit breaker.
type CircuitBreakerConfig struct {
	FailThreshold int           // consecutive failures to trip open
	ResetTimeout  time.Duration // time in open state before half-open probe
	Name          string        // provider name for log messages
}

// CircuitBreaker wraps sony/gobreaker with a simpler Allow/Record API
// that matches how our providers work (check before call, record after).
type CircuitBreaker struct {
	gb   *gobreaker.CircuitBreaker[any]
	name string
}

// NewCircuitBreaker creates a circuit breaker with the given configuration.
func NewCircuitBreaker(cfg CircuitBreakerConfig) *CircuitBreaker {
	gb := gobreaker.NewCircuitBreaker[any](gobreaker.Settings{
		Name:        cfg.Name,
		MaxRequests: 1, // allow 1 probe in half-open state
		Interval:    0, // don't reset counts on a timer; only on state transition
		Timeout:     cfg.ResetTimeout,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return int(counts.ConsecutiveFailures) >= cfg.FailThreshold
		},
		OnStateChange: func(name string, from, to gobreaker.State) {
			slog.Warn("circuit breaker state change",
				"provider", name,
				"from", from.String(),
				"to", to.String(),
			)
		},
	})
	return &CircuitBreaker{gb: gb, name: cfg.Name}
}

// Allow checks if a request should proceed. Returns an error if the circuit is open.
func (cb *CircuitBreaker) Allow() error {
	state := cb.gb.State()
	if state == gobreaker.StateOpen {
		return fmt.Errorf("%s circuit breaker open", cb.name)
	}
	return nil
}

// Execute runs fn through the circuit breaker. If fn returns an error,
// it's counted as a failure. Use this instead of Allow+RecordSuccess/RecordFailure
// for simpler call sites.
func (cb *CircuitBreaker) Execute(fn func() (any, error)) error {
	_, err := cb.gb.Execute(fn)
	return err
}

// RecordSuccess records a successful call (resets failure count, closes half-open).
func (cb *CircuitBreaker) RecordSuccess() {
	// Execute a no-op success through gobreaker to update its internal state.
	cb.gb.Execute(func() (any, error) { return nil, nil }) //nolint:errcheck
}

// RecordFailure records a failed call (increments failure count, may trip open).
func (cb *CircuitBreaker) RecordFailure() {
	// Execute a no-op failure through gobreaker to update its internal state.
	cb.gb.Execute(func() (any, error) { return nil, fmt.Errorf("failure") }) //nolint:errcheck
}
