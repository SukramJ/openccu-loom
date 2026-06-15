// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
)

// CentralLinksService is the facade `/devices/{addr}/central-links` depends
// on. The implementation lives in central/adapter and routes the request to
// the per-CCU client backend.
type CentralLinksService interface {
	CreateCentralLinks(ctx context.Context, deviceAddress string) (CentralLinksReport, error)
	RemoveCentralLinks(ctx context.Context, deviceAddress string) (CentralLinksReport, error)
	CentralLinksStatus(deviceAddress string) (CentralLinksStatus, error)
}

// CentralLinksReport summarises one create/remove call. Touched is the
// number of channels for which the CCU accepted the report-value-usage
// call, Skipped the count of channels without press events (so they
// were left alone), Failed the count of channels where the CCU
// returned an error.
type CentralLinksReport struct {
	Touched int `json:"touched"`
	Skipped int `json:"skipped"`
	Failed  int `json:"failed"`
}

// CentralLinksStatus describes whether the device is eligible for
// central click-event routing.
type CentralLinksStatus struct {
	Supported        bool   `json:"supported"`
	Reason           string `json:"reason,omitempty"`
	EligibleChannels int    `json:"eligible_channels,omitempty"`
}

// GetCentralLinksStatus serves GET /devices/{addr}/central-links.
func GetCentralLinksStatus(svc CentralLinksService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Central links unavailable", ""))
			return
		}
		st, err := svc.CentralLinksStatus(chi.URLParam(r, "addr"))
		if err != nil {
			problem.Write(w, http.StatusBadGateway,
				problem.New(problem.TypeUpstreamUnavailable, r, "Central links status failed", err.Error()))
			return
		}
		JSON(w, http.StatusOK, st)
	}
}

// CreateCentralLinks serves POST /devices/{addr}/central-links.
func CreateCentralLinks(svc CentralLinksService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Central links unavailable", ""))
			return
		}
		report, err := svc.CreateCentralLinks(r.Context(), chi.URLParam(r, "addr"))
		if err != nil {
			centralLinksError(w, r, err)
			return
		}
		JSON(w, http.StatusAccepted, report)
	}
}

// DeleteCentralLinks serves DELETE /devices/{addr}/central-links.
func DeleteCentralLinks(svc CentralLinksService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Central links unavailable", ""))
			return
		}
		report, err := svc.RemoveCentralLinks(r.Context(), chi.URLParam(r, "addr"))
		if err != nil {
			centralLinksError(w, r, err)
			return
		}
		JSON(w, http.StatusAccepted, report)
	}
}

// ErrCentralLinksUnsupported is returned by adapters when the device
// is on an interface that has no concept of central event routing
// (CUxD, virtual devices, …). Surfaced as 422 to make the SPA show
// "not applicable on this device" instead of a generic upstream error.
var ErrCentralLinksUnsupported = errors.New("central-links: device interface does not support central links")

func centralLinksError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, ErrCentralLinksUnsupported) {
		problem.Write(w, http.StatusUnprocessableEntity,
			problem.New(problem.TypeValidation, r, "Central links unsupported", err.Error()))
		return
	}
	problem.Write(w, http.StatusBadGateway,
		problem.New(problem.TypeUpstreamUnavailable, r, "Central links failed", err.Error()))
}
