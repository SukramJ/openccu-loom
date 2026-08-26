// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/model/device/definitionexport"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
)

// DeviceDefinitionExportService produces an anonymised device-definition zip
// for a device. The implementation lives in the central adapter layer; tests
// supply stubs.
type DeviceDefinitionExportService interface {
	// ExportDefinition returns the device model and a zip archive containing
	// device_descriptions/{model}.json and paramset_descriptions/{model}.json.
	// It returns [definitionexport.ErrDeviceNotFound] when the device is
	// unknown to every central.
	ExportDefinition(ctx context.Context, deviceAddress string) (model string, zip []byte, err error)
}

// ExportDeviceDefinition handles
//
//	GET /api/v1/devices/{addr}/export-definition
//
// It fetches the device + channel descriptions and their non-LINK paramset
// descriptions straight off the CCU (preserving wire member order), anonymises
// the addresses behind a single random "VCU" id, and streams a zip whose two
// JSON members are byte-for-byte identical to the Python reference's
// export_device_definition — so the archive drops straight into godevccu
// godevccu as a device fixture.
func ExportDeviceDefinition(svc DeviceDefinitionExportService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Device definition export unavailable", "no backend wired"))
			return
		}
		addr := chi.URLParam(r, "addr")
		if addr == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Device address required", ""))
			return
		}
		model, archive, err := svc.ExportDefinition(r.Context(), addr)
		if err != nil {
			if errors.Is(err, definitionexport.ErrDeviceNotFound) {
				problem.Write(w, http.StatusNotFound,
					problem.New(problem.TypeNotFound, r, "Device not found", addr))
				return
			}
			writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Device definition export failed", err)
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", ContentDispositionAttachment(model+".zip"))
		_, _ = w.Write(archive)
	}
}
