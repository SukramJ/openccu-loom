// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"net/http"
	"time"

	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/restapi"
)

// HealthReader is an alias for the canonical interface in internal/restapi.
type HealthReader = restapi.HealthReader

// HealthComponent is one entry in the response.
type HealthComponent struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Note   string `json:"note,omitempty"`
	// NoteKey is the i18n catalogue key for the localized display of a static
	// note; empty for interpolated notes (render Note verbatim). Clients that
	// localize (SPA, /health page) prefer NoteKey and fall back to Note.
	NoteKey    string    `json:"note_key,omitempty"`
	RecordedAt time.Time `json:"recorded_at"`
}

// HealthResponse is the body of `GET /api/v1/health`.
type HealthResponse struct {
	Status     string            `json:"status"`
	Components []HealthComponent `json:"components"`
}

// Health returns a handler reporting the current health snapshot.
// Unhealthy composite status maps to HTTP 503 so load-balancers can
// drain the instance; everything else is 200.
func Health(tracker HealthReader) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		snap := tracker.Snapshot()
		out := make([]HealthComponent, 0, len(snap))
		for _, c := range snap {
			out = append(out, HealthComponent{
				Name:       c.Name,
				Status:     string(c.Status),
				Note:       c.LastSample.Note,
				NoteKey:    c.LastSample.NoteKey,
				RecordedAt: c.LastSample.Timestamp,
			})
		}
		// Use the service-availability collapse rather than the raw worst
		// case: a single south-bound interface (or the MQTT bridge) down on a
		// multi-CCU daemon degrades service but does not make the REST/UI
		// surface unavailable. Only a fatal dependency (persistence) or a
		// total south-bound outage maps to 503.
		overall := health.ServiceAvailability(snap)
		code := http.StatusOK
		if overall == health.StatusUnhealthy {
			code = http.StatusServiceUnavailable
		}
		JSON(w, code, HealthResponse{Status: string(overall), Components: out})
	}
}
