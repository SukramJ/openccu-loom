// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// CentralLinksService is an alias for the canonical interface in pkg/interfaces.
type CentralLinksService = interfaces.CentralLinksService

// CentralLinksReport is an alias for the canonical DTO in pkg/hmapi.
type CentralLinksReport = hmapi.CentralLinksReport

// CentralLinksStatus is an alias for the canonical DTO in pkg/hmapi.
type CentralLinksStatus = hmapi.CentralLinksStatus

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
			writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Central links status failed", err)
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
		report, err := svc.CreateCentralLinks(r.Context(), chi.URLParam(r, "addr"), r.URL.Query().Get("channel"))
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
		report, err := svc.RemoveCentralLinks(r.Context(), chi.URLParam(r, "addr"), r.URL.Query().Get("channel"))
		if err != nil {
			centralLinksError(w, r, err)
			return
		}
		JSON(w, http.StatusAccepted, report)
	}
}

// ErrCentralLinksUnsupported is an alias for the sentinel in pkg/hmapi.
var ErrCentralLinksUnsupported = hmapi.ErrCentralLinksUnsupported

func centralLinksError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, ErrCentralLinksUnsupported) {
		problem.Write(w, http.StatusUnprocessableEntity,
			problem.New(problem.TypeValidation, r, "Central links unsupported", err.Error()))
		return
	}
	if errors.Is(err, hmapi.ErrCentralLinksChannelNotFound) {
		problem.Write(w, http.StatusUnprocessableEntity,
			problem.New(problem.TypeValidation, r, "Central links channel not found", err.Error()))
		return
	}
	writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Central links failed", err)
}
