// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
)

// DeviceSearchPort triggers a wired-bus device scan on one interface.
// *adapter.DeviceAdminDomain satisfies it.
type DeviceSearchPort interface {
	SearchWiredDevices(ctx context.Context, central, interfaceID string) (int, error)
}

// installModeSearchRequest is the body of POST /install-mode/search.
type installModeSearchRequest struct {
	// Interface selects the BidCos-Wired interface to scan (required).
	Interface string `json:"interface"`
	// Central disambiguates the CCU in a multi-CCU deployment.
	Central string `json:"central,omitempty"`
}

// PostInstallModeSearch triggers a wired-bus scan and returns the count
// of devices found. The scan is synchronous (200, not 202); found
// devices surface in the inbox for acceptance. Only BidCos-Wired
// supports it — other interfaces answer 422.
func PostInstallModeSearch(svc DeviceSearchPort, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "device search unwired", ""))
			return
		}
		var body installModeSearchRequest
		if err := DecodeJSON(r, &body); err != nil {
			problem.Write(w, DecodeJSONStatus(err),
				problem.New(problem.TypeBadRequest, r, "Invalid request body", err.Error()))
			return
		}
		if body.Interface == "" {
			problem.Write(w, http.StatusUnprocessableEntity,
				problem.New(problem.TypeValidation, r, "interface is required", ""))
			return
		}
		found, err := svc.SearchWiredDevices(r.Context(), body.Central, body.Interface)
		if err != nil {
			if errors.Is(err, backends.ErrUnsupported) {
				problem.Write(w, http.StatusUnprocessableEntity,
					problem.New(problem.TypeValidation, r, "Device search not supported on this interface", ""))
				return
			}
			writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Device search failed", err)
			return
		}
		if rec != nil {
			rec.Record(audit.Entry{
				User:   identityFromCtx(r.Context()),
				Action: audit.ActionDeviceSearch,
				Note:   "wired bus scan on " + body.Interface,
			})
		}
		JSON(w, http.StatusOK, map[string]any{
			"central":   body.Central,
			"interface": body.Interface,
			"found":     found,
		})
	}
}
