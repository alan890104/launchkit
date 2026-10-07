package middleware

import (
	"net/http"
	"strings"
)

// NewCORS returns a CORS middleware.
//
// If allowedOrigins is empty or contains "*", Access-Control-Allow-Origin is
// set to "*" (wildcard — suitable for dev).
//
// Otherwise the request's Origin header is checked against the allowlist. A
// matching origin is reflected back so the browser accepts the response.
// Requests from unlisted origins still reach the handler but receive no ACAO
// header, causing the browser to block the response.
func NewCORS(allowedOrigins []string) func(http.Handler) http.Handler {
	wildcard := isWildcard(allowedOrigins)

	allowed := make(map[string]bool, len(allowedOrigins))
	for _, o := range allowedOrigins {
		if n := normalizeOrigin(o); n != "" {
			allowed[n] = true
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if wildcard {
				w.Header().Set("Access-Control-Allow-Origin", "*")
			} else if origin := r.Header.Get("Origin"); origin != "" {
				if allowed[normalizeOrigin(origin)] {
					w.Header().Set("Access-Control-Allow-Origin", normalizeOrigin(origin))
					w.Header().Add("Vary", "Origin")
				}
				// Unlisted origin: no ACAO header — browser will block (correct behavior).
			}

			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Mcp-Session-Id")
			w.Header().Set("Access-Control-Expose-Headers", "Mcp-Session-Id")
			w.Header().Set("Access-Control-Max-Age", "86400")

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// CORS is a wildcard CORS middleware kept for dev convenience and backward compatibility.
var CORS = NewCORS([]string{"*"})

func isWildcard(origins []string) bool {
	if len(origins) == 0 {
		return true
	}
	for _, o := range origins {
		if strings.TrimSpace(o) == "*" {
			return true
		}
	}
	return false
}

func normalizeOrigin(o string) string {
	return strings.TrimRight(strings.TrimSpace(o), "/")
}
