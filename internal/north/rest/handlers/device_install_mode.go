// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
)

// DeviceInstallModePort opens a per-device pairing window so an operator
// can teach in a specific device (e.g. by its serial / address) instead
// of the whole radio. *adapter.DeviceAdminDomain satisfies it; the call
// resolves the owning backend by address and issues the CCU's
// setInstallMode with the device address bound.
type DeviceInstallModePort interface {
	SetInstallMode(ctx context.Context, address string, durationSecs int) error
}

// deviceInstallModeRequest is the body of POST /devices/{addr}/install-mode.
type deviceInstallModeRequest struct {
	// Seconds is the pairing window length; defaults to 60 when <= 0.
	Seconds int `json:"seconds,omitempty"`
}

// PostDeviceInstallMode opens a targeted pairing window for one device.
// This is the REST counterpart of the WS `device.install_mode` command
// and the serial-targeted teach-in the CCU WebUI offers.
func PostDeviceInstallMode(svc DeviceInstallModePort, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "device install-mode unwired", ""))
			return
		}
		addr := chi.URLParam(r, "addr")
		if addr == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Missing address", "addr path parameter is required"))
			return
		}
		var body deviceInstallModeRequest
		if err := DecodeJSON(r, &body); err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Invalid request body", err.Error()))
			return
		}
		seconds := body.Seconds
		if seconds <= 0 {
			seconds = 60
		}
		if err := svc.SetInstallMode(r.Context(), addr, seconds); err != nil {
			problem.Write(w, http.StatusBadGateway,
				problem.New(problem.TypeUpstreamUnavailable, r, "Install mode write failed", err.Error()))
			return
		}
		if rec != nil {
			rec.Record(audit.Entry{
				User:          identityFromCtx(r.Context()),
				Action:        audit.ActionDeviceInstallMode,
				DeviceAddress: addr,
				Note:          "targeted pairing window",
			})
		}
		w.WriteHeader(http.StatusAccepted)
	}
}
