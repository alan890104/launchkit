package middleware

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

// ---------------------------------------------------------------------------
// TestChain
// ---------------------------------------------------------------------------

func TestChain(t *testing.T) {
	t.Run("two middleware execute in order A then B then handler", func(t *testing.T) {
		var order []string

		mwA := func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, "A")
				next.ServeHTTP(w, r)
			})
		}
		mwB := func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, "B")
				next.ServeHTTP(w, r)
			})
		}
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "handler")
		})

		chained := Chain(handler, mwA, mwB)

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		chained.ServeHTTP(rec, req)

		assert.Equal(t, []string{"A", "B", "handler"}, order)
	})

	t.Run("no middleware still calls handler", func(t *testing.T) {
		called := false
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			w.WriteHeader(http.StatusOK)
		})

		chained := Chain(handler)

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		chained.ServeHTTP(rec, req)

		assert.True(t, called)
		assert.Equal(t, http.StatusOK, rec.Code)
	})
}

// ---------------------------------------------------------------------------
// TestCORS
// ---------------------------------------------------------------------------

func TestCORS(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	assertCommonHeaders := func(t *testing.T, h http.Header) {
		t.Helper()
		assert.Equal(t, "GET, POST, DELETE, OPTIONS", h.Get("Access-Control-Allow-Methods"))
		assert.Equal(t, "Authorization, Content-Type, Mcp-Session-Id", h.Get("Access-Control-Allow-Headers"))
		assert.Equal(t, "Mcp-Session-Id", h.Get("Access-Control-Expose-Headers"))
		assert.Equal(t, "86400", h.Get("Access-Control-Max-Age"))
	}

	t.Run("wildcard — OPTIONS returns 204 with ACAO *", func(t *testing.T) {
		wrapped := CORS(inner)
		req := httptest.NewRequest(http.MethodOptions, "/", nil)
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusNoContent, rec.Code)
		assert.Empty(t, rec.Body.String())
		assert.Equal(t, "*", rec.Header().Get("Access-Control-Allow-Origin"))
		assertCommonHeaders(t, rec.Header())
	})

	t.Run("wildcard — GET passes through with ACAO *", func(t *testing.T) {
		wrapped := CORS(inner)
		req := httptest.NewRequest(http.MethodGet, "/resource", nil)
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "*", rec.Header().Get("Access-Control-Allow-Origin"))
		assertCommonHeaders(t, rec.Header())
	})

	t.Run("origin allowlist — matching origin is reflected", func(t *testing.T) {
		wrapped := NewCORS([]string{"https://app.launchkit.dev", "https://launchkit.dev"})(inner)
		req := httptest.NewRequest(http.MethodGet, "/resource", nil)
		req.Header.Set("Origin", "https://app.launchkit.dev")
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "https://app.launchkit.dev", rec.Header().Get("Access-Control-Allow-Origin"))
		assert.Equal(t, "Origin", rec.Header().Get("Vary"))
		assertCommonHeaders(t, rec.Header())
	})

	t.Run("origin allowlist — unlisted origin gets no ACAO header", func(t *testing.T) {
		wrapped := NewCORS([]string{"https://app.launchkit.dev"})(inner)
		req := httptest.NewRequest(http.MethodGet, "/resource", nil)
		req.Header.Set("Origin", "https://evil.example.com")
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"), "unlisted origin must not receive ACAO header")
		assertCommonHeaders(t, rec.Header())
	})

	t.Run("origin allowlist — no Origin header is fine (non-browser clients)", func(t *testing.T) {
		wrapped := NewCORS([]string{"https://app.launchkit.dev"})(inner)
		req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))
	})

	t.Run("origin allowlist — OPTIONS preflight from allowed origin", func(t *testing.T) {
		wrapped := NewCORS([]string{"https://app.launchkit.dev"})(inner)
		req := httptest.NewRequest(http.MethodOptions, "/mcp", nil)
		req.Header.Set("Origin", "https://app.launchkit.dev")
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusNoContent, rec.Code)
		assert.Equal(t, "https://app.launchkit.dev", rec.Header().Get("Access-Control-Allow-Origin"))
		assertCommonHeaders(t, rec.Header())
	})
}

// ---------------------------------------------------------------------------
// TestRecovery
// ---------------------------------------------------------------------------

func TestRecovery(t *testing.T) {
	t.Run("handler that panics returns 500 with error body", func(t *testing.T) {
		panicker := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			panic("boom")
		})
		wrapped := Recovery(panicker)

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

		body, _ := io.ReadAll(rec.Body)
		assert.Contains(t, string(body), "internal server error")
	})

	t.Run("normal handler passes through unchanged", func(t *testing.T) {
		normal := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte("created"))
		})
		wrapped := Recovery(normal)

		req := httptest.NewRequest(http.MethodPost, "/items", nil)
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusCreated, rec.Code)
		assert.Equal(t, "created", rec.Body.String())
	})
}

// ---------------------------------------------------------------------------
// TestRecovery_PanicNil
// ---------------------------------------------------------------------------

func TestRecovery_PanicNil(t *testing.T) {
	// In Go 1.21+ panic(nil) wraps into *runtime.PanicNilError,
	// so recover() returns non-nil. Verify we still return 500.
	panicker := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic(nil) //nolint:govet
	})
	wrapped := Recovery(panicker)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	body, _ := io.ReadAll(rec.Body)
	assert.Contains(t, string(body), "internal server error")
}

// ---------------------------------------------------------------------------
// TestLogging
// ---------------------------------------------------------------------------

func TestLogging(t *testing.T) {
	t.Run("200 handler passes through", func(t *testing.T) {
		called := false
		inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			w.WriteHeader(http.StatusOK)
		})
		wrapped := Logging(inner)

		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)

		assert.True(t, called, "inner handler must be called")
		assert.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("500 handler passes through", func(t *testing.T) {
		called := false
		inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			w.WriteHeader(http.StatusInternalServerError)
		})
		wrapped := Logging(inner)

		req := httptest.NewRequest(http.MethodGet, "/fail", nil)
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)

		assert.True(t, called, "inner handler must be called")
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})
}

// ---------------------------------------------------------------------------
// TestLogging_IPResolution
// ---------------------------------------------------------------------------

// captureHandler is a slog.Handler that captures all log records.
type captureHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *captureHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }

func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r)
	return nil
}

func (h *captureHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(_ string) slog.Handler      { return h }

// ipFromRecord extracts the "ip" attribute from a slog.Record.
func ipFromRecord(r slog.Record) string {
	var ip string
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "ip" {
			ip = a.Value.String()
			return false
		}
		return true
	})
	return ip
}

func TestLogging_IPResolution(t *testing.T) {
	tests := []struct {
		name       string
		xff        string // X-Forwarded-For
		xri        string // X-Real-IP
		remoteAddr string
		wantIP     string
	}{
		{
			name:       "X-Forwarded-For wins",
			xff:        "1.2.3.4",
			xri:        "",
			remoteAddr: "9.9.9.9:12345",
			wantIP:     "1.2.3.4",
		},
		{
			name:       "X-Real-IP when no X-Forwarded-For",
			xff:        "",
			xri:        "5.6.7.8",
			remoteAddr: "9.9.9.9:12345",
			wantIP:     "5.6.7.8",
		},
		{
			name:       "X-Forwarded-For takes priority over X-Real-IP",
			xff:        "1.2.3.4",
			xri:        "5.6.7.8",
			remoteAddr: "9.9.9.9:12345",
			wantIP:     "1.2.3.4",
		},
		{
			name:       "falls back to RemoteAddr",
			xff:        "",
			xri:        "",
			remoteAddr: "10.0.0.1:54321",
			wantIP:     "10.0.0.1:54321",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ch := &captureHandler{}
			oldLogger := slog.Default()
			slog.SetDefault(slog.New(ch))
			defer slog.SetDefault(oldLogger)

			inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})
			wrapped := Logging(inner)

			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			req.RemoteAddr = tt.remoteAddr
			if tt.xff != "" {
				req.Header.Set("X-Forwarded-For", tt.xff)
			}
			if tt.xri != "" {
				req.Header.Set("X-Real-IP", tt.xri)
			}
			rec := httptest.NewRecorder()
			wrapped.ServeHTTP(rec, req)

			ch.mu.Lock()
			defer ch.mu.Unlock()
			assert.NotEmpty(t, ch.records, "expected at least one log record")
			if len(ch.records) > 0 {
				assert.Equal(t, tt.wantIP, ipFromRecord(ch.records[len(ch.records)-1]))
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestRequestID
// ---------------------------------------------------------------------------

func TestRequestID(t *testing.T) {
	t.Run("valid X-Trace-Id header is preserved", func(t *testing.T) {
		const traceID = "my-trace-123"
		var ctxID string

		inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctxID = TraceIDFromContext(r.Context())
		})
		wrapped := RequestID(inner)

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-Trace-Id", traceID)
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)

		assert.Equal(t, traceID, rec.Header().Get("X-Trace-Id"))
		assert.Equal(t, traceID, ctxID)
	})

	t.Run("missing X-Trace-Id generates a UUID", func(t *testing.T) {
		var ctxID string

		inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctxID = TraceIDFromContext(r.Context())
		})
		wrapped := RequestID(inner)

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)

		respID := rec.Header().Get("X-Trace-Id")
		assert.NotEmpty(t, respID)
		assert.Equal(t, respID, ctxID)
		// UUID v4 has the format xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx (36 chars)
		assert.Len(t, respID, 36)
	})

	t.Run("X-Trace-Id longer than 128 chars generates new UUID", func(t *testing.T) {
		longID := strings.Repeat("a", 129)
		var ctxID string

		inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctxID = TraceIDFromContext(r.Context())
		})
		wrapped := RequestID(inner)

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-Trace-Id", longID)
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)

		respID := rec.Header().Get("X-Trace-Id")
		assert.NotEqual(t, longID, respID)
		assert.Len(t, respID, 36)
		assert.Equal(t, respID, ctxID)
	})

	t.Run("X-Trace-Id exactly 128 chars is preserved", func(t *testing.T) {
		exactID := strings.Repeat("b", 128)
		var ctxID string

		inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctxID = TraceIDFromContext(r.Context())
		})
		wrapped := RequestID(inner)

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-Trace-Id", exactID)
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)

		assert.Equal(t, exactID, rec.Header().Get("X-Trace-Id"))
		assert.Equal(t, exactID, ctxID)
	})

	t.Run("X-Trace-Id 100 chars is preserved", func(t *testing.T) {
		midID := strings.Repeat("c", 100)
		var ctxID string

		inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctxID = TraceIDFromContext(r.Context())
		})
		wrapped := RequestID(inner)

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-Trace-Id", midID)
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)

		assert.Equal(t, midID, rec.Header().Get("X-Trace-Id"))
		assert.Equal(t, midID, ctxID)
	})
}

// ---------------------------------------------------------------------------
// TestTraceIDFromContext
// ---------------------------------------------------------------------------

func TestTraceIDFromContext(t *testing.T) {
	t.Run("context with trace ID returns it", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), traceIDKey, "abc-123")
		assert.Equal(t, "abc-123", TraceIDFromContext(ctx))
	})

	t.Run("empty context returns empty string", func(t *testing.T) {
		assert.Equal(t, "", TraceIDFromContext(context.Background()))
	})
}

// ---------------------------------------------------------------------------
// TestStatusWriter
// ---------------------------------------------------------------------------

func TestStatusWriter(t *testing.T) {
	t.Run("WriteHeader called once captures status", func(t *testing.T) {
		rec := httptest.NewRecorder()
		sw := &statusWriter{ResponseWriter: rec, status: http.StatusOK}

		sw.WriteHeader(http.StatusNotFound)

		assert.Equal(t, http.StatusNotFound, sw.status)
		assert.True(t, sw.wroteHeader)
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("WriteHeader called twice only captures first status", func(t *testing.T) {
		rec := httptest.NewRecorder()
		sw := &statusWriter{ResponseWriter: rec, status: http.StatusOK}

		sw.WriteHeader(http.StatusCreated)
		sw.WriteHeader(http.StatusInternalServerError)

		assert.Equal(t, http.StatusCreated, sw.status)
		assert.True(t, sw.wroteHeader)
		// Note: the underlying recorder still receives both calls, but our
		// wrapper only records the first one.
	})

	t.Run("no explicit WriteHeader defaults to 200", func(t *testing.T) {
		rec := httptest.NewRecorder()
		sw := &statusWriter{ResponseWriter: rec, status: http.StatusOK}

		// Write body without calling WriteHeader — the default should remain.
		sw.Write([]byte("hello"))

		assert.Equal(t, http.StatusOK, sw.status)
		assert.False(t, sw.wroteHeader)
	})

	t.Run("Unwrap returns underlying ResponseWriter", func(t *testing.T) {
		rec := httptest.NewRecorder()
		sw := &statusWriter{ResponseWriter: rec, status: http.StatusOK}

		assert.Equal(t, rec, sw.Unwrap())
	})
}
