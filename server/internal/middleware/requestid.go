package middleware

import (
	"context"
	"net/http"

	"github.com/google/uuid"
)

type ctxKey string

const traceIDKey ctxKey = "trace_id"

func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Trace-Id")
		if id == "" || len(id) > 128 {
			id = uuid.New().String()
		}

		w.Header().Set("X-Trace-Id", id)
		ctx := context.WithValue(r.Context(), traceIDKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// TraceIDFromContext extracts the trace ID from the context.
func TraceIDFromContext(ctx context.Context) string {
	if id, ok := ctx.Value(traceIDKey).(string); ok {
		return id
	}
	return ""
}
