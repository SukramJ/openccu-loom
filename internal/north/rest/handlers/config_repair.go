// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// DeviceConfigRepairService is an alias for the canonical interface in
// pkg/interfaces.
type DeviceConfigRepairService = interfaces.DeviceConfigRepairService

type configRepairRequest struct {
	// DryRun is a pointer so an omitted field keeps the safe default: a
	// repair only writes when the caller asks for it explicitly.
	DryRun   *bool    `json:"dry_run"`
	Channels []string `json:"channels"`
}

type configRepairCorrection struct {
	Parameter string `json:"parameter"`
	// Stored and Corrected are wire values of whatever type the parameter
	// carries (bool, number, string), decoded before type dispatch.
	Stored    any    `json:"stored"`
	Corrected any    `json:"corrected"`
	Reason    string `json:"reason,omitempty"`
}

type configRepairOutcome struct {
	Channel     string                     `json:"channel"`
	Status      string                     `json:"status"`
	Corrections []configRepairCorrection   `json:"corrections"`
	Foreign     []string                   `json:"foreign"`
	Error       string                     `json:"error,omitempty"`
	Result      *hmapi.ParamsetWriteResult `json:"result,omitempty"`
}

type configRepairResponse struct {
	Items []configRepairOutcome `json:"items"`
}

// RepairDeviceConfig serves POST /devices/{addr}/config/repair: rebuilding
// the device's stored MASTER configuration from its own paramset
// descriptions. The body is optional; without one the request is a dry run
// over every MASTER-bearing channel.
func RepairDeviceConfig(svc DeviceConfigRepairService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Config repair unavailable", "no backend wired"))
			return
		}
		addr := chi.URLParam(r, "addr")
		if addr == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Missing address", "addr path parameter is required"))
			return
		}
		var req configRepairRequest
		if err := DecodeJSON(r, &req); err != nil && !errors.Is(err, io.EOF) {
			problem.Write(w, DecodeJSONStatus(err),
				problem.New(problem.TypeBadRequest, r, "Invalid JSON", err.Error()))
			return
		}
		dryRun := true
		if req.DryRun != nil {
			dryRun = *req.DryRun
		}
		outcomes, err := svc.RepairDeviceConfig(r.Context(), addr, req.Channels, dryRun)
		if err != nil {
			if errors.Is(err, hmerr.ErrDescriptionNotFound) {
				problem.Write(w, http.StatusNotFound,
					problem.New(problem.TypeNotFound, r, "Device not found", err.Error()))
				return
			}
			writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Config repair failed", err)
			return
		}
		resp := configRepairResponse{Items: make([]configRepairOutcome, 0, len(outcomes))}
		for _, o := range outcomes {
			resp.Items = append(resp.Items, configRepairOutcomeDTO(o))
		}
		JSON(w, http.StatusOK, resp)
	}
}

// configRepairOutcomeDTO projects a domain outcome onto the wire shape.
// Both lists always serialise as arrays, never null.
func configRepairOutcomeDTO(o interfaces.ConfigRepairOutcome) configRepairOutcome {
	item := configRepairOutcome{
		Channel:     o.Channel,
		Status:      o.Status,
		Corrections: make([]configRepairCorrection, 0, len(o.Corrections)),
		Foreign:     make([]string, 0, len(o.Foreign)),
		Error:       o.Error,
	}
	for _, c := range o.Corrections {
		item.Corrections = append(item.Corrections, configRepairCorrection{
			Parameter: c.Parameter, Stored: c.Stored, Corrected: c.Corrected, Reason: c.Reason,
		})
	}
	item.Foreign = append(item.Foreign, o.Foreign...)
	if o.Result != nil {
		result := paramsetWriteResult(o.Result)
		item.Result = &result
	}
	return item
}
