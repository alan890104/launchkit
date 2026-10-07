package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// ─── Rate Limiter Tests ──────────────────────────────────────────

func TestRateLimiter_BasicTokenBucket(t *testing.T) {
	rl := NewRateLimiter(RateLimiterConfig{PerSecond: 10, PerHour: 10000})
	ctx := context.Background()

	// Should be able to consume 10 tokens immediately.
	for i := 0; i < 10; i++ {
		if err := rl.Wait(ctx); err != nil {
			t.Fatalf("Wait %d failed: %v", i, err)
		}
	}

	// 11th should block — use a short deadline to verify.
	shortCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	err := rl.Wait(shortCtx)
	if err == nil {
		t.Fatal("expected rate limit wait to timeout, got nil")
	}
}

func TestRateLimiter_RefillsOverTime(t *testing.T) {
	rl := NewRateLimiter(RateLimiterConfig{PerSecond: 10, PerHour: 10000})
	ctx := context.Background()

	// Drain all tokens.
	for i := 0; i < 10; i++ {
		_ = rl.Wait(ctx)
	}

	// Wait for refill (at 10 tokens/sec, 150ms should give ~1.5 tokens).
	time.Sleep(150 * time.Millisecond)

	shortCtx, cancel := context.WithTimeout(ctx, 10*time.Millisecond)
	defer cancel()
	if err := rl.Wait(shortCtx); err != nil {
		t.Fatalf("expected token to be available after refill, got: %v", err)
	}
}

func TestRateLimiter_HourlyWindow(t *testing.T) {
	rl := NewRateLimiter(RateLimiterConfig{PerSecond: 100, PerHour: 5})
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		if err := rl.Wait(ctx); err != nil {
			t.Fatalf("Wait %d failed: %v", i, err)
		}
	}

	// 6th should block (hourly limit exhausted).
	shortCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if err := rl.Wait(shortCtx); err == nil {
		t.Fatal("expected hourly limit to block, got nil")
	}
}

func TestRateLimiter_ConcurrentAccess(t *testing.T) {
	rl := NewRateLimiter(RateLimiterConfig{PerSecond: 20, PerHour: 10000})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	var passed atomic.Int64

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := rl.Wait(ctx); err == nil {
				passed.Add(1)
			}
		}()
	}

	wg.Wait()
	got := passed.Load()
	if got < 20 || got > 50 {
		t.Fatalf("expected 20-50 requests to pass, got %d", got)
	}
}

// ─── Circuit Breaker Tests ───────────────────────────────────────

func TestCircuitBreaker_ClosedByDefault(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{FailThreshold: 3, ResetTimeout: 100 * time.Millisecond, Name: "test"})
	if err := cb.Allow(); err != nil {
		t.Fatalf("expected closed circuit to allow, got: %v", err)
	}
}

func TestCircuitBreaker_OpensAfterThreshold(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{FailThreshold: 3, ResetTimeout: 100 * time.Millisecond, Name: "test"})

	cb.RecordFailure()
	cb.RecordFailure()
	if err := cb.Allow(); err != nil {
		t.Fatalf("expected circuit still closed after 2 failures, got: %v", err)
	}

	cb.RecordFailure()
	if err := cb.Allow(); err == nil {
		t.Fatal("expected circuit to be open after 3 failures")
	}
}

func TestCircuitBreaker_ResetsAfterSuccess(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{FailThreshold: 3, ResetTimeout: 100 * time.Millisecond, Name: "test"})

	cb.RecordFailure()
	cb.RecordFailure()
	cb.RecordSuccess()
	cb.RecordFailure()
	cb.RecordFailure()

	if err := cb.Allow(); err != nil {
		t.Fatalf("expected circuit closed after success reset, got: %v", err)
	}
}

func TestCircuitBreaker_HalfOpenAfterTimeout(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{FailThreshold: 2, ResetTimeout: 50 * time.Millisecond, Name: "test"})

	cb.RecordFailure()
	cb.RecordFailure()
	if err := cb.Allow(); err == nil {
		t.Fatal("expected open circuit to deny")
	}

	time.Sleep(60 * time.Millisecond)

	if err := cb.Allow(); err != nil {
		t.Fatalf("expected half-open to allow probe, got: %v", err)
	}
	cb.RecordSuccess()
	if err := cb.Allow(); err != nil {
		t.Fatalf("expected closed after probe success, got: %v", err)
	}
}

func TestCircuitBreaker_HalfOpenProbeFailure(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{FailThreshold: 2, ResetTimeout: 50 * time.Millisecond, Name: "test"})

	cb.RecordFailure()
	cb.RecordFailure()
	time.Sleep(60 * time.Millisecond)

	_ = cb.Allow() // half-open probe
	cb.RecordFailure()
	if err := cb.Allow(); err == nil {
		t.Fatal("expected circuit re-opened after probe failure")
	}
}

// ─── NameCom Integration Tests with Mock Server ──────────────────

func newTestNameCom(handler http.Handler) *NameCom {
	return &NameCom{
		username: "testuser",
		token:    "testtoken",
		baseURL:  "https://namecom.test",
		client: &http.Client{
			Timeout: 5 * time.Second,
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)
				return rec.Result(), nil
			}),
		},
		limiter: NewRateLimiter(RateLimiterConfig{PerSecond: 100, PerHour: 10000}),
		cb:      NewCircuitBreaker(CircuitBreakerConfig{FailThreshold: 5, ResetTimeout: 30 * time.Second, Name: "test"}),
	}
}

func TestNameCom_SearchDomains(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/domains:checkAvailability" || r.Method != "POST" {
			http.Error(w, "not found", 404)
			return
		}

		user, token, ok := r.BasicAuth()
		if !ok || user != "testuser" || token != "testtoken" {
			http.Error(w, "unauthorized", 401)
			return
		}

		resp := map[string]any{
			"results": []map[string]any{
				{
					"domainName": "example.com", "purchasable": true, "premium": false,
					"purchasePrice": map[string]string{"price": "12.99"},
					"renewalPrice":  map[string]string{"price": "12.99"},
				},
				{
					"domainName": "example.io", "purchasable": false, "premium": false,
					"purchasePrice": map[string]string{"price": "45.00"},
					"renewalPrice":  map[string]string{"price": "45.00"},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	nc := newTestNameCom(handler)
	results, err := nc.SearchDomains(context.Background(), []string{"example.com", "example.io"})
	if err != nil {
		t.Fatalf("SearchDomains failed: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if !results[0].Available || results[0].PriceUSD != 12.99 {
		t.Errorf("unexpected first result: %+v", results[0])
	}
	if results[1].Available {
		t.Error("expected example.io to be unavailable")
	}
}

func TestNameCom_CircuitBreakerTripsOn5xx(t *testing.T) {
	var callCount atomic.Int64
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		http.Error(w, "internal server error", 500)
	})

	nc := newTestNameCom(handler)
	nc.cb = NewCircuitBreaker(CircuitBreakerConfig{FailThreshold: 3, ResetTimeout: 1 * time.Second, Name: "test"})

	for i := 0; i < 3; i++ {
		_ = nc.doJSON(context.Background(), "GET", "/test", nil, nil)
	}

	countBefore := callCount.Load()
	err := nc.doJSON(context.Background(), "GET", "/test", nil, nil)
	countAfter := callCount.Load()

	if err == nil {
		t.Fatal("expected circuit breaker error")
	}
	if countAfter != countBefore {
		t.Error("expected circuit breaker to prevent server call")
	}
}

func TestNameCom_CreateDNSRecord(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/domains/example.com/records" || r.Method != "POST" {
			http.Error(w, "not found", 404)
			return
		}
		resp := map[string]any{
			"id": 42, "type": "CNAME", "host": "www",
			"answer": "myapp.pages.dev", "ttl": 300, "priority": 0,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	nc := newTestNameCom(handler)
	rec, err := nc.CreateDNSRecord(context.Background(), "example.com", RegistrarDNSRecord{
		Type: "CNAME", Host: "www", Value: "myapp.pages.dev", TTL: 300,
	})
	if err != nil {
		t.Fatalf("CreateDNSRecord failed: %v", err)
	}
	if rec.ID != 42 || rec.Value != "myapp.pages.dev" {
		t.Errorf("unexpected record: %+v", rec)
	}
}

func TestNameCom_LockDomain(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/domains/example.com:lock" || r.Method != "POST" {
			http.Error(w, "not found", 404)
			return
		}
		w.WriteHeader(200)
	})

	nc := newTestNameCom(handler)
	if err := nc.LockDomain(context.Background(), "example.com"); err != nil {
		t.Fatalf("LockDomain failed: %v", err)
	}
}
