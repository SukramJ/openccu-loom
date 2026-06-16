// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// ScheduleService is an alias for the canonical interface in pkg/interfaces.
type ScheduleService = interfaces.ScheduleService

// --- DTO aliases --------------------------------------------------

// ClimateSchedule is an alias for the canonical DTO in pkg/hmapi.
type ClimateSchedule = hmapi.ClimateSchedule

// ScheduleChannelRef is an alias for the canonical DTO in pkg/hmapi.
type ScheduleChannelRef = hmapi.ScheduleChannelRef

// ClimateProfile is an alias for the canonical DTO in pkg/hmapi.
type ClimateProfile = hmapi.ClimateProfile

// ClimateWeekday is an alias for the canonical DTO in pkg/hmapi.
type ClimateWeekday = hmapi.ClimateWeekday

// ClimatePeriod is an alias for the canonical DTO in pkg/hmapi.
type ClimatePeriod = hmapi.ClimatePeriod

// SimpleScheduleEntry is an alias for the canonical DTO in pkg/hmapi.
type SimpleScheduleEntry = hmapi.SimpleScheduleEntry

// SetActiveProfileRequest is the body of POST .../schedule/active-profile.
type SetActiveProfileRequest struct {
	Profile string `json:"profile"`
}

// --- HTTP handlers ------------------------------------------------

// GetSchedule returns the full climate schedule for one channel.
func GetSchedule(svc ScheduleService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Schedule service unavailable", ""))
			return
		}
		addr := chi.URLParam(r, "addr")
		no, err := strconv.Atoi(chi.URLParam(r, "no"))
		if err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Invalid channel number", chi.URLParam(r, "no")))
			return
		}
		s, err := svc.GetClimateSchedule(r.Context(), addr, no)
		if err != nil {
			writeScheduleError(w, r, err)
			return
		}
		JSON(w, http.StatusOK, s)
	}
}

// PutSchedule replaces the climate schedule of one channel.
func PutSchedule(svc ScheduleService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Schedule service unavailable", ""))
			return
		}
		addr := chi.URLParam(r, "addr")
		no, err := strconv.Atoi(chi.URLParam(r, "no"))
		if err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Invalid channel number", chi.URLParam(r, "no")))
			return
		}
		var body ClimateSchedule
		if err := DecodeJSON(r, &body); err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Invalid JSON", err.Error()))
			return
		}
		if err := svc.PutClimateSchedule(r.Context(), addr, no, &body); err != nil {
			writeScheduleError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

// GetScheduleAuto exposes the schedule on the device level. The
// adapter resolves the right channel itself — useful for SPA tabs
// that live on the device rather than a specific channel.
func GetScheduleAuto(svc ScheduleService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Schedule service unavailable", ""))
			return
		}
		addr := chi.URLParam(r, "addr")
		s, err := svc.GetClimateScheduleAuto(r.Context(), addr)
		if err != nil {
			writeScheduleError(w, r, err)
			return
		}
		JSON(w, http.StatusOK, s)
	}
}

// PutScheduleAuto is the device-level write counterpart.
func PutScheduleAuto(svc ScheduleService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Schedule service unavailable", ""))
			return
		}
		addr := chi.URLParam(r, "addr")
		var body ClimateSchedule
		if err := DecodeJSON(r, &body); err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Invalid JSON", err.Error()))
			return
		}
		if err := svc.PutClimateScheduleAuto(r.Context(), addr, &body); err != nil {
			writeScheduleError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

// PostActiveProfileAuto is the device-level active-profile setter.
func PostActiveProfileAuto(svc ScheduleService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Schedule service unavailable", ""))
			return
		}
		addr := chi.URLParam(r, "addr")
		var body SetActiveProfileRequest
		if err := DecodeJSON(r, &body); err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Invalid JSON", err.Error()))
			return
		}
		if err := svc.SetActiveProfileAuto(r.Context(), addr, body.Profile); err != nil {
			writeScheduleError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

// PostActiveProfile switches the currently active profile on the
// thermostat (e.g. from P1 to P2).
func PostActiveProfile(svc ScheduleService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Schedule service unavailable", ""))
			return
		}
		addr := chi.URLParam(r, "addr")
		no, err := strconv.Atoi(chi.URLParam(r, "no"))
		if err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Invalid channel number", chi.URLParam(r, "no")))
			return
		}
		var body SetActiveProfileRequest
		if err := DecodeJSON(r, &body); err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Invalid JSON", err.Error()))
			return
		}
		if err := svc.SetActiveProfile(r.Context(), addr, no, body.Profile); err != nil {
			writeScheduleError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

// ErrNoSchedule is re-exported so the handler file can match against
// the adapter's sentinel without a direct dep on the adapter package.
var ErrNoSchedule = errors.New("schedule not supported on this channel")

func writeScheduleError(w http.ResponseWriter, r *http.Request, err error) {
	// Device-not-found at the adapter layer maps to 404 — see
	// SchedulesDomain.resolve / FindScheduleChannel.
	if errors.Is(err, hmerr.ErrDescriptionNotFound) {
		problem.Write(w, http.StatusNotFound,
			problem.New(problem.TypeNotFound, r, "Device not found", ""))
		return
	}
	// Map adapter-level "no schedule keys" errors to 404 so the SPA
	// can display a friendly "device has no schedule" message.
	if err != nil && err.Error() == "schedules: channel exposes no climate schedule parameters" {
		problem.Write(w, http.StatusNotFound,
			problem.New(problem.TypeNotFound, r, "Channel has no climate schedule", ""))
		return
	}
	// A schedule write reaches the CCU through the backend; any remaining
	// failure is an upstream/gateway problem, not an internal one. Mirror
	// the value-write handler (devices.go) and return 502 — never 500
	// (TestRESTMutationWalker treats a mutation 500 as a bug).
	if problem.IsUpstreamUnavailable(err) {
		problem.Write(w, http.StatusBadGateway,
			problem.New(problem.TypeUpstreamUnavailable, r, "Upstream temporarily unavailable", err.Error()))
		return
	}
	problem.Write(w, http.StatusBadGateway,
		problem.New(problem.TypeUpstreamUnavailable, r, "Schedule request failed", err.Error()))
}
