// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// CCURebootPort reboots one CCU host addressed by central name.
// *adapter.CCUMaintenanceDomain satisfies it; the call resolves the
// central's primary backend and runs the reboot_ccu ReGa script.
type CCURebootPort interface {
	RebootCCU(ctx context.Context, central string) error
}

// PostCCUReboot reboots the named CCU. The router gates it on the admin
// role. It reboots the CCU hardware — not the OpenCCU-Loom daemon (that is
// POST /system/restart). The southbound connection to the central drops for
// the reboot and recovers automatically via the readiness gate.
//
// Responses: 202 once the reboot was triggered, 404 when the central is
// unknown, 502 when the CCU-side call failed, 503 when the service is
// unwired. Audit-logs via the AuditRecorder pattern of PostDeviceInstallMode.
func PostCCUReboot(svc CCURebootPort, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "CCU maintenance unwired", ""))
			return
		}
		centralName := chi.URLParam(r, "central")
		if centralName == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Missing central", "central path parameter is required"))
			return
		}
		if err := svc.RebootCCU(r.Context(), centralName); err != nil {
			if errors.Is(err, hmerr.ErrUnknownCentral) {
				problem.Write(w, http.StatusNotFound,
					problem.New(problem.TypeNotFound, r, "Unknown central", centralName))
				return
			}
			writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "CCU reboot failed", err)
			return
		}
		if rec != nil {
			rec.Record(audit.Entry{
				User:   identityFromCtx(r.Context()),
				Action: audit.ActionSystemCCUReboot,
				Note:   centralName,
			})
		}
		w.WriteHeader(http.StatusAccepted)
	}
}
