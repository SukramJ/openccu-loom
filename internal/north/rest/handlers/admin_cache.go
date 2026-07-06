// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"net/http"

	"github.com/SukramJ/openccu-loom/internal/central/cachereset"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
)

// CacheResetService is the narrow facade behind
// `POST /api/v1/admin/cache/clear`. It clears the CCU-derivable caches
// for the requested scope and re-pulls them fresh through the boot path
// (ADR 0042). *cachereset.Service satisfies it.
type CacheResetService interface {
	Clear(ctx context.Context, scope cachereset.Scope) (cachereset.Report, error)
}

// ClearCacheRequest is the body for `POST /api/v1/admin/cache/clear`. The
// fields map onto [cachereset.Scope]; central/interface/device are required
// only at or below their level (see [cachereset.Scope.Validate]).
type ClearCacheRequest struct {
	Kind      string `json:"kind"`
	Central   string `json:"central,omitempty"`
	Interface string `json:"interface,omitempty"`
	Device    string `json:"device,omitempty"`
}

// ClearCache handles `POST /api/v1/admin/cache/clear`. It maps the request
// onto a [cachereset.Scope] and drives the cache-reset service.
//
// Status codes:
//   - 200 with the [cachereset.Report] on success.
//   - 400 on an invalid JSON body or a scope that fails validation
//     (unknown kind, or a kind missing the fields its level requires).
//   - 502 with the partial [cachereset.Report] still in the body when the
//     clear ran but one or more stores reported an error. The operator
//     sees what was cleared alongside the failure.
//   - 503 when the service is not wired (south-bound never came up).
func ClearCache(svc CacheResetService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Cache reset unavailable", ""))
			return
		}
		var req ClearCacheRequest
		if err := DecodeJSON(r, &req); err != nil {
			problem.Write(w, DecodeJSONStatus(err),
				problem.New(problem.TypeBadRequest, r, "Invalid JSON", err.Error()))
			return
		}
		scope := cachereset.Scope{
			Kind:      cachereset.ScopeKind(req.Kind),
			Central:   req.Central,
			Interface: req.Interface,
			Device:    req.Device,
		}
		if err := scope.Validate(); err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Invalid scope", err.Error()))
			return
		}
		report, err := svc.Clear(r.Context(), scope)
		if err != nil {
			// The clear ran but a store failed. Return the partial report so
			// the operator sees what was cleared, with a 502 to signal that the
			// upstream stores/re-pull did not fully succeed.
			JSON(w, http.StatusBadGateway, report)
			return
		}
		JSON(w, http.StatusOK, report)
	}
}
