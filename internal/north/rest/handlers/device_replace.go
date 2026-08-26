// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// DeviceReplacePort drives the guided device-replace workflow.
// *adapter.DeviceAdminDomain satisfies it.
type DeviceReplacePort interface {
	ReplaceCandidates(ctx context.Context, centralName, newAddress string) ([]hmapi.ReplaceCandidate, error)
	ReplaceDevice(ctx context.Context, centralName, oldAddress, newAddress string) error
}

// deviceReplaceRequest is the body of POST /devices/{addr}/replace.
type deviceReplaceRequest struct {
	// OldAddress is the paired device the new device (the {addr} path
	// parameter) replaces.
	OldAddress string `json:"old_address"`
	// Central disambiguates the CCU in a multi-CCU deployment. Optional
	// for single-CCU setups.
	Central string `json:"central,omitempty"`
}

// GetDeviceReplaceCandidates serves
// `GET /devices/{addr}/replace-candidates`: the already-paired devices
// the new (inbox) device at {addr} may replace. A pure CCU read, so it
// stays on the viewer tier like GET /inbox.
func GetDeviceReplaceCandidates(svc DeviceReplacePort) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "device replace unwired", ""))
			return
		}
		addr := chi.URLParam(r, "addr")
		if addr == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Missing address", "addr path parameter is required"))
			return
		}
		candidates, err := svc.ReplaceCandidates(r.Context(), r.URL.Query().Get("central"), addr)
		if err != nil {
			writeReplaceError(w, r, err, "Replace candidates lookup failed")
			return
		}
		if candidates == nil {
			candidates = []hmapi.ReplaceCandidate{}
		}
		JSON(w, http.StatusOK, map[string]any{"candidates": candidates})
	}
}

// PostDeviceReplace serves `POST /devices/{addr}/replace`: swap the
// paired old_address for the new device at {addr}. Admin-gated and
// audit-logged — the old device is unpaired, so the write is
// irreversible like DELETE /devices/{addr}. Returns 202; the radio
// config transfer to the new device continues CCU-side afterwards.
func PostDeviceReplace(svc DeviceReplacePort, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "device replace unwired", ""))
			return
		}
		newAddr := chi.URLParam(r, "addr")
		if newAddr == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Missing address", "addr path parameter is required"))
			return
		}
		var body deviceReplaceRequest
		if err := DecodeJSON(r, &body); err != nil {
			problem.Write(w, DecodeJSONStatus(err),
				problem.New(problem.TypeBadRequest, r, "Invalid request body", err.Error()))
			return
		}
		if body.OldAddress == "" {
			problem.Write(w, http.StatusUnprocessableEntity,
				problem.New(problem.TypeValidation, r, "old_address is required", ""))
			return
		}
		if err := svc.ReplaceDevice(r.Context(), body.Central, body.OldAddress, newAddr); err != nil {
			writeReplaceError(w, r, err, "Replace failed")
			return
		}
		if rec != nil {
			rec.Record(audit.Entry{
				User:          identityFromCtx(r.Context()),
				Action:        audit.ActionDeviceReplace,
				DeviceAddress: body.OldAddress,
				Note:          "replaced by " + newAddr,
			})
		}
		JSON(w, http.StatusAccepted, map[string]any{
			"status":      "replacing",
			"old_address": body.OldAddress,
			"new_address": newAddr,
			"central":     body.Central,
		})
	}
}

// writeReplaceError maps a replace failure: an unknown central is 404,
// an ineligible interface (backends.ErrUnsupported) is 422, everything
// else is an upstream 502.
func writeReplaceError(w http.ResponseWriter, r *http.Request, err error, title string) {
	switch {
	case errors.Is(err, hmerr.ErrUnknownCentral):
		problem.Write(w, http.StatusNotFound,
			problem.New(problem.TypeNotFound, r, "Central not found", err.Error()))
	case errors.Is(err, backends.ErrUnsupported):
		problem.Write(w, http.StatusUnprocessableEntity,
			problem.New(problem.TypeValidation, r, "Replace not supported on this interface", ""))
	default:
		writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, title, err)
	}
}
