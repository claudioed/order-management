package http

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// RequestLogger emits one structured line per request: method, route
// pattern, status, byte count and duration, plus the RequestID middleware's
// id so a client-reported failure can be found in the logs.
//
// The route PATTERN is logged rather than the raw path, so an order id
// never becomes a distinct log dimension (and never leaks into a
// cardinality explosion or an aggregation key).
func RequestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			next.ServeHTTP(ww, r)

			route := chiRoutePattern(r)
			logger.InfoContext(r.Context(), "http request",
				"method", r.Method,
				"route", route,
				"status", ww.Status(),
				"bytes", ww.BytesWritten(),
				"duration_ms", time.Since(start).Milliseconds(),
				"request_id", middleware.GetReqID(r.Context()),
			)
		})
	}
}

// chiRoutePattern returns the matched route pattern, falling back to the
// raw path for requests that matched no route (404s).
func chiRoutePattern(r *http.Request) string {
	if rctx := chi.RouteContext(r.Context()); rctx != nil {
		if pattern := rctx.RoutePattern(); pattern != "" {
			return pattern
		}
	}
	return r.URL.Path
}
