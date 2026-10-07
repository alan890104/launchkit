package middleware

import (
	"log/slog"
	"net/http"
	"time"
)

func Logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(sw, r)

		// Resolve client IP: prefer proxy headers over the raw TCP address.
		ip := r.Header.Get("X-Forwarded-For")
		if ip == "" {
			ip = r.Header.Get("X-Real-IP")
		}
		if ip == "" {
			ip = r.RemoteAddr
		}

		attrs := []any{
			"trace_id", TraceIDFromContext(r.Context()),
			"method", r.Method,
			"path", r.URL.Path,
			"status", sw.status,
			"duration_ms", time.Since(start).Milliseconds(),
			"ip", ip,
			"user_agent", r.UserAgent(),
		}

		switch {
		case sw.status >= 500:
			slog.ErrorContext(r.Context(), "http request", attrs...)
		case sw.status >= 400:
			slog.WarnContext(r.Context(), "http request", attrs...)
		default:
			slog.InfoContext(r.Context(), "http request", attrs...)
		}
	})
}

type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *statusWriter) WriteHeader(code int) {
	if !w.wroteHeader {
		w.status = code
		w.wroteHeader = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
