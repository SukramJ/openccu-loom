// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package middleware collects the cross-cutting chi middlewares
// applied by the REST router: request ID, structured logging,
// recovery, and a request timeout.
package middleware

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/google/uuid"

	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
)

type ctxKey string

// Context keys.
const (
	keyRequestID ctxKey = "request_id"
)

// RequestID attaches a UUID to the request context and mirrors it
// into the `X-Request-ID` response header.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" {
			id = uuid.NewString()
		}
		ctx := context.WithValue(r.Context(), keyRequestID, id)
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// SecurityHeaders stamps baseline security response headers on every
// request. `X-Content-Type-Options: nosniff` is the key one: it stops
// browsers from MIME-sniffing a JSON/text response as HTML, which is the
// standard mitigation against reflected-XSS in a JSON API (any
// user-provided value that ends up echoed in a response body can no
// longer be coerced into executing as markup). The header is cheap and
// applies uniformly through the logging/idempotency response wrappers.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		if h.Get("X-Content-Type-Options") == "" {
			h.Set("X-Content-Type-Options", "nosniff")
		}
		next.ServeHTTP(w, r)
	})
}

// RequestIDFrom extracts the current request ID, returning "" when
// no middleware attached one.
func RequestIDFrom(ctx context.Context) string {
	if v, ok := ctx.Value(keyRequestID).(string); ok {
		return v
	}
	return ""
}

// Logger wraps each request in slog-structured logging. It records
// the method, path, status, byte count, and elapsed duration.
//
// The level is chosen by response status so the access log does not
// drown real signal: a successful request (2xx/3xx) is debug-level
// noise — the bulk of "http.request method=GET" traffic an operator
// does not need — while a client error (4xx) surfaces at WARN and a
// server error (5xx) at ERROR. Run at debug level to see every request.
func Logger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sw := &statusWriter{ResponseWriter: w, status: 200}
			next.ServeHTTP(sw, r)
			level := slog.LevelDebug
			switch {
			case sw.status >= 500:
				level = slog.LevelError
			case sw.status >= 400:
				level = slog.LevelWarn
			}
			logger.LogAttrs(
				r.Context(), level, "http.request",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", sw.status),
				slog.Int("bytes", sw.bytes),
				slog.Duration("elapsed", time.Since(start)),
				slog.String("request_id", RequestIDFrom(r.Context())),
			)
		})
	}
}

type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (sw *statusWriter) WriteHeader(status int) {
	sw.status = status
	sw.ResponseWriter.WriteHeader(status)
}

func (sw *statusWriter) Write(p []byte) (int, error) {
	n, err := sw.ResponseWriter.Write(p)
	sw.bytes += n
	return n, err
}

// Hijack forwards to the underlying ResponseWriter so WebSocket
// upgrades survive the logger wrapper. Without this method the
// `w.(http.Hijacker)` assertion in the `/api/v1/events` handler
// fails (interface embedding hides Hijack from the wrapper) and the
// WS connect drops with a 500 — which is what kept the SPA's live-
// update path silent in production.
func (sw *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := sw.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("ws: underlying ResponseWriter does not support hijack")
	}
	return hj.Hijack()
}

// Flush forwards to the underlying ResponseWriter so streaming
// responses (SSE, chunked JSON) keep working through the logger.
func (sw *statusWriter) Flush() {
	if f, ok := sw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Recover catches panics in downstream handlers, logs the stack, and
// responds with problem+json `internal`.
func Recover(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			//nolint:contextcheck // deferred panic-recovery closure logs with r.Context(); no new detached context is created
			defer func() {
				if rec := recover(); rec != nil {
					logger.LogAttrs(
						r.Context(), slog.LevelError, "http.panic",
						slog.String("panic", fmt.Sprint(rec)),
						slog.String("stack", string(debug.Stack())),
						slog.String("request_id", RequestIDFrom(r.Context())),
					)
					problem.Write(w, http.StatusInternalServerError,
						problem.New(problem.TypeInternal, r, "Internal error", "panic recovered"))
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// Timeout wraps the request context in a deadline.
func Timeout(d time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
