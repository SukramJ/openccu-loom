// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// DiagnosticsIntrospectService is an alias for the canonical interface in pkg/interfaces.
type DiagnosticsIntrospectService = interfaces.DiagnosticsIntrospectService

// ReliabilityState is an alias for the canonical DTO in pkg/hmapi.
type ReliabilityState = hmapi.ReliabilityState

// DiagnosticsEvent is an alias for the canonical DTO in pkg/hmapi.
type DiagnosticsEvent = hmapi.DiagnosticsEvent

const (
	tapDefaultWindow = 30 * time.Second
	tapMaxWindow     = 300 * time.Second
	tapBuffer        = 256
)

// DiagnosticsReliability serves GET /diagnostics/reliability — a JSON
// snapshot of per-(central, interface) reliability state, optionally
// filtered to ?central=NAME.
func DiagnosticsReliability(svc DiagnosticsIntrospectService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Diagnostics unavailable", "no introspection source"))
			return
		}
		JSON(w, http.StatusOK, svc.ReliabilitySnapshot(r.URL.Query().Get("central")))
	}
}

// DiagnosticsEventBusTap serves GET /diagnostics/eventbus/tap — a bounded
// NDJSON stream of internal event-bus traffic for one central.
//
// Query: ?central=NAME (required when more than one central exists),
// ?type=Name (repeatable; default = all curated types), ?seconds=N
// (default 30, capped at 300). The stream ends at the window deadline or
// when the client disconnects.
func DiagnosticsEventBusTap(svc DiagnosticsIntrospectService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Diagnostics unavailable", "no introspection source"))
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			problem.Write(w, http.StatusInternalServerError,
				problem.New(problem.TypeInternal, r, "Streaming unsupported", ""))
			return
		}
		central, ok := svc.ResolveCentral(r.URL.Query().Get("central"))
		if !ok {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "central required", "pass ?central=NAME (multiple or unknown central)"))
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), tapWindow(r.URL.Query().Get("seconds")))
		defer cancel()
		ch := make(chan DiagnosticsEvent, tapBuffer)
		go func() {
			svc.TapEventBus(ctx, central, r.URL.Query()["type"], func(e DiagnosticsEvent) {
				select {
				case ch <- e:
				default: // consumer too slow — drop rather than stall the bus
				}
			})
			close(ch)
		}()

		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		enc := json.NewEncoder(w)
		_ = enc.Encode(DiagnosticsEvent{TS: tapNow(), Type: "_tap_started", Event: map[string]string{"central": central}})
		flusher.Flush()
		for e := range ch {
			if err := enc.Encode(e); err != nil {
				cancel()
				return
			}
			flusher.Flush()
		}
	}
}

func tapWindow(s string) time.Duration {
	if s == "" {
		return tapDefaultWindow
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return tapDefaultWindow
	}
	if d := time.Duration(n) * time.Second; d <= tapMaxWindow {
		return d
	}
	return tapMaxWindow
}

func tapNow() string { return time.Now().UTC().Format(time.RFC3339Nano) }
