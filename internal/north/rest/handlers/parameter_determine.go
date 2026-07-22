// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// ParameterDeterminer is an alias for the canonical interface in pkg/interfaces.
type ParameterDeterminer = interfaces.ParameterDeterminer

// determineRequest is the body of a determine request: the single
// parameter whose live value should be read from the device.
type determineRequest struct {
	Parameter string `json:"parameter"`
}

// determineResponse carries the value the device reported for the
// determined parameter.
type determineResponse struct {
	Value any `json:"value"`
}

// DetermineParameter serves
// `POST /devices/{addr}/channels/{no}/paramsets/{key}/determine`.
//
// It reads the current live value of one parameter straight from the
// device via the CCU's determineParameter operation (the MASTER editor's
// "Determine" button). This is a read — no edit-lock token is required —
// but it does trigger a device round-trip, so the caller names the
// parameter in the request body:
//
//	{ "parameter": "TEMPERATURE" }
//
// The {key} path segment scopes the request to the paramset the editor is
// working on; the CCU auto-selects the paramset on the wire, so the key is
// validated for a clean 400 but not forwarded.
func DetermineParameter(svc ParameterDeterminer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Determine unavailable", "no backend wired"))
			return
		}
		addr := chi.URLParam(r, "addr")
		no := chi.URLParam(r, "no")
		if addr == "" || no == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Invalid channel", "addr and no are required"))
			return
		}
		if _, ok := parseParamsetKey(chi.URLParam(r, "key")); !ok {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Invalid paramset key", chi.URLParam(r, "key")))
			return
		}
		var req determineRequest
		if err := DecodeJSON(r, &req); err != nil {
			problem.Write(w, DecodeJSONStatus(err),
				problem.New(problem.TypeBadRequest, r, "Invalid JSON", err.Error()))
			return
		}
		if req.Parameter == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Missing parameter", "the parameter field is required"))
			return
		}
		channelAddress := addr + ":" + no
		// interfaceID is resolved from the registry by the implementation.
		value, err := svc.DetermineParameter(r.Context(), "", channelAddress, req.Parameter)
		if err != nil {
			writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Determine failed", err)
			return
		}
		JSON(w, http.StatusOK, determineResponse{Value: value})
	}
}
