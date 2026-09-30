// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// ParamsetApplyService is an alias for the canonical interface in pkg/interfaces.
type ParamsetApplyService = interfaces.ParamsetApplyService

// paramsetApplyTarget is the wire shape of one eligible target channel.
type paramsetApplyTarget struct {
	Address       string `json:"address"`
	Name          string `json:"name,omitempty"`
	DeviceAddress string `json:"device_address,omitempty"`
	DeviceName    string `json:"device_name,omitempty"`
	DeviceModel   string `json:"device_model,omitempty"`
	InterfaceID   string `json:"interface_id,omitempty"`
}

type paramsetApplyTargetsResponse struct {
	Items []paramsetApplyTarget `json:"items"`
}

type paramsetApplyRequest struct {
	// Values is decoded JSON before per-target coercion against each
	// target's paramset description.
	Values  map[string]any `json:"values"`
	Targets []string       `json:"targets"`
	DryRun  bool           `json:"dry_run"`
}

type paramsetApplyOutcome struct {
	Address string                     `json:"address"`
	Status  string                     `json:"status"`
	Reason  string                     `json:"reason,omitempty"`
	Result  *hmapi.ParamsetWriteResult `json:"result,omitempty"`
}

type paramsetApplyResponse struct {
	Items []paramsetApplyOutcome `json:"items"`
}

// requireMasterKey answers 400 unless the path key is MASTER: the
// multi-apply is defined only for configuration paramsets whose description
// identity can be established.
func requireMasterKey(w http.ResponseWriter, r *http.Request) bool {
	if chi.URLParam(r, "key") == string(hmenum.ParamsetKeyMaster) {
		return true
	}
	problem.Write(w, http.StatusBadRequest,
		problem.New(problem.TypeBadRequest, r, "Only MASTER can be applied to other channels", chi.URLParam(r, "key")))
	return false
}

func writeParamsetApplyUnavailable(w http.ResponseWriter, r *http.Request) {
	problem.Write(w, http.StatusServiceUnavailable,
		problem.New(problem.TypeServiceUnready, r, "Paramset apply unavailable", "no backend wired"))
}

// GetParamsetApplyTargets serves GET /devices/{addr}/paramsets/{key}/apply-targets.
func GetParamsetApplyTargets(svc ParamsetApplyService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeParamsetApplyUnavailable(w, r)
			return
		}
		if !requireMasterKey(w, r) {
			return
		}
		addr := chi.URLParam(r, "addr")
		targets, err := svc.ApplyTargets(r.Context(), addr)
		if err != nil {
			if errors.Is(err, hmerr.ErrDescriptionNotFound) {
				problem.Write(w, http.StatusNotFound,
					problem.New(problem.TypeNotFound, r, "No stored MASTER description", err.Error()))
				return
			}
			writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Apply-target lookup failed", err)
			return
		}
		resp := paramsetApplyTargetsResponse{Items: make([]paramsetApplyTarget, 0, len(targets))}
		for _, t := range targets {
			resp.Items = append(resp.Items, paramsetApplyTarget{
				Address:       t.Address,
				Name:          t.Name,
				DeviceAddress: t.DeviceAddress,
				DeviceName:    t.DeviceName,
				DeviceModel:   t.DeviceModel,
				InterfaceID:   t.InterfaceID,
			})
		}
		JSON(w, http.StatusOK, resp)
	}
}

// ApplyParamsetToChannels serves POST /devices/{addr}/paramsets/{key}/apply-to.
// The caller holds the SOURCE channel's MASTER edit lock; the targets are not
// locked individually — the per-target description-identity gate and
// validation are what make the batch safe. `locks` may be nil only in tests.
func ApplyParamsetToChannels(svc ParamsetApplyService, locks *EditSessions) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeParamsetApplyUnavailable(w, r)
			return
		}
		if !requireMasterKey(w, r) {
			return
		}
		addr := chi.URLParam(r, "addr")
		if !enforceEditLock(w, r, locks, "channel:"+addr+":"+string(hmenum.ParamsetKeyMaster)) {
			return
		}
		var req paramsetApplyRequest
		if err := DecodeJSON(r, &req); err != nil {
			problem.Write(w, DecodeJSONStatus(err),
				problem.New(problem.TypeBadRequest, r, "Invalid JSON", err.Error()))
			return
		}
		if len(req.Values) == 0 || len(req.Targets) == 0 {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "values and targets are required", "both must be non-empty"))
			return
		}
		outcomes, err := svc.ApplyToChannels(r.Context(), addr, req.Values, req.Targets, req.DryRun)
		if err != nil {
			writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Paramset apply failed", err)
			return
		}
		resp := paramsetApplyResponse{Items: make([]paramsetApplyOutcome, 0, len(outcomes))}
		for _, o := range outcomes {
			item := paramsetApplyOutcome{Address: o.Address, Status: o.Status, Reason: o.Reason}
			if o.Result != nil {
				result := paramsetWriteResult(o.Result)
				item.Result = &result
			}
			resp.Items = append(resp.Items, item)
		}
		JSON(w, http.StatusOK, resp)
	}
}
