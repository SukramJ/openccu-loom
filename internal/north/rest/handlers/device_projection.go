// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/north/projection"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
)

// DeviceProjector answers how one device stands on the north-bound
// planes. The second result is false for an address no central holds.
// plane narrows the answer to one plane; "" asks all of them.
type DeviceProjector interface {
	ProjectDevice(ctx context.Context, address, plane string) (projection.Device, bool, error)
}

// GetDeviceProjection serves GET /devices/{addr}/projection: per plane,
// what is published for the device under which identifiers, what is
// carried inside another entry, and what is left out and why.
func GetDeviceProjection(p DeviceProjector) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		addr := chi.URLParam(r, "addr")
		out, ok, err := p.ProjectDevice(r.Context(), addr, r.URL.Query().Get("plane"))
		if err != nil {
			problem.WriteFromError(w, r, err)
			return
		}
		if !ok {
			problem.Write(w, http.StatusNotFound,
				problem.New(problem.TypeNotFound, r, "Device not found", addr))
			return
		}
		JSON(w, http.StatusOK, out)
	}
}
