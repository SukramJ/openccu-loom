// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
)

// DeviceConfigRestorePort re-transmits the centrally stored
// configuration to a device after a factory reset.
// *adapter.DeviceAdminDomain satisfies it; the call resolves the owning
// backend by address and issues the CCU's `restoreConfigToDevice`.
type DeviceConfigRestorePort interface {
	RestoreDeviceConfig(ctx context.Context, address string) error
}

// RestoreDeviceConfig serves `POST /devices/{addr}/config/restore`: it
// asks the CCU to re-transmit every channel's stored MASTER paramset
// plus the device's link peerings — the recovery path after a device
// factory reset. The transfer runs asynchronously on the radio, so the
// endpoint returns 202; the SPA watches the CONFIG_PENDING badge for
// progress. Interfaces without the capability (BidCos-Wired, CUxD,
// VirtualDevices) answer 422.
func RestoreDeviceConfig(svc DeviceConfigRestorePort, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "device config-restore unwired", ""))
			return
		}
		addr := chi.URLParam(r, "addr")
		if addr == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Missing address", "addr path parameter is required"))
			return
		}
		if err := svc.RestoreDeviceConfig(r.Context(), addr); err != nil {
			if errors.Is(err, backends.ErrUnsupported) {
				problem.Write(w, http.StatusUnprocessableEntity,
					problem.New(problem.TypeValidation, r, "Config restore not supported on this interface", ""))
				return
			}
			writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Config restore failed", err)
			return
		}
		if rec != nil {
			rec.Record(audit.Entry{
				User:          identityFromCtx(r.Context()),
				Action:        audit.ActionDeviceConfigRestore,
				DeviceAddress: addr,
				Note:          "restore stored config to device",
			})
		}
		w.WriteHeader(http.StatusAccepted)
	}
}
