package middleware

import "net/http"

// Chain wraps an http.Handler with middleware, applied outermost-first.
// Chain(h, A, B) executes as A → B → h.
func Chain(h http.Handler, mws ...func(http.Handler) http.Handler) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}
