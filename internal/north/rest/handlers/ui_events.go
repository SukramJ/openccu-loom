// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
)

// UIEventRequest is the body of the SPA's `POST /api/v1/ui/event`
// telemetry endpoint. The shape is intentionally generic — `event`
// names the action, `properties` carry whatever fields the SPA wants
// to attach. The daemon writes a structured slog line; downstream
// log aggregation does the counting and slicing (no Prometheus
// counter, no in-process metric channel — see ADR 0016 follow-ups
// where the trade-off is documented).
type UIEventRequest struct {
	Event      string         `json:"event"`
	Properties map[string]any `json:"properties,omitempty"`
}

// PostUIEvent logs a SPA-side UI telemetry event under the
// `ui.event` slog channel. Returns 204 No Content on success and
// 400 on malformed JSON. The handler never returns the event back
// to the client and never persists it — observability lives in
// the structured log line only.
//
// Anonymous endpoint: telemetry is per-installation, not per-user;
// no auth gate is enforced. The daemon's audit log handles the
// user-scoped activity stream separately.
func PostUIEvent() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req UIEventRequest
		if err := DecodeJSON(r, &req); err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Invalid JSON", err.Error()))
			return
		}
		if req.Event == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Missing field", "event"))
			return
		}
		attrs := []any{
			slog.String("event", req.Event),
			slog.Time("at", time.Now().UTC()),
		}
		for k, v := range req.Properties {
			attrs = append(attrs, slog.Any("prop_"+k, v))
		}
		slog.Debug("ui.event", attrs...)
		w.WriteHeader(http.StatusNoContent)
	}
}
