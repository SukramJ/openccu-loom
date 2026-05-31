// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
)

// MQTTReloadService is the DI surface for POST /api/v1/admin/mqtt/reload.
// The daemon binds this to an adapter that reads the current config
// snapshot and calls mqttSupervisor.Swap. A nil service makes the
// route 404 — operators without the supervisor (e.g. test
// configurations) get a clean "not configured" signal rather than
// a 500.
type MQTTReloadService interface {
	// Reload tears down the running MQTT stack and rebuilds it from
	// the current config. Returns the wall-clock duration of the
	// rebuild on success or an error when the new stack failed to
	// connect (the previous stack continues unchanged in that case).
	Reload(ctx context.Context) (took time.Duration, err error)
}

// MQTTReloadResponse is the JSON body returned on success.
type MQTTReloadResponse struct {
	Reloaded bool  `json:"reloaded"`
	TookMS   int64 `json:"took_ms"`
}

// MQTTReload handles POST /admin/mqtt/reload. Tears down the running
// MQTT stack and rebuilds it from the live config. Returns 503 when
// the rebuild fails (the operator retries against the previous
// running stack), 200 with a small JSON body on success.
//
// No request body is required. The endpoint is idempotent in the
// sense that two consecutive calls produce two reconnects — operators
// who want to "kick" a stuck broker can issue the call multiple
// times without side effects beyond the broker connection bounce.
func MQTTReload(svc MQTTReloadService, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		took, err := svc.Reload(r.Context())
		if err != nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeInternal, r, "MQTT reload failed", err.Error()))
			return
		}
		actor := identityFromCtx(r.Context())
		if rec != nil {
			rec.Record(audit.Entry{
				User:   actor,
				Action: audit.ActionConfigSectionUpdate,
				Note:   "mqtt.reload",
			})
		}
		JSON(w, http.StatusOK, MQTTReloadResponse{
			Reloaded: true,
			TookMS:   took.Milliseconds(),
		})
	}
}
