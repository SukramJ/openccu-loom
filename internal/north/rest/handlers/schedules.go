// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
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

// CopyScheduleRequest is the body of POST .../schedules/copy. The source
// device is the path {addr}; the target device is named here.
type CopyScheduleRequest struct {
	TargetDeviceAddress string `json:"target_device_address"`
}

// CopyProfileRequest is the body of POST
// .../channels/{no}/week_profile/copy. The source channel is the path
// {addr}:{no}; the target channel and the source/target profile indices
// are named here.
type CopyProfileRequest struct {
	SourceProfile        int    `json:"source_profile"`
	TargetChannelAddress string `json:"target_channel_address"`
	TargetProfile        int    `json:"target_profile"`
}

// --- HTTP handlers ------------------------------------------------

// ScheduleDeviceSummary is an alias for the canonical DTO in pkg/hmapi.
type ScheduleDeviceSummary = hmapi.ScheduleDeviceSummary

// ListSchedulesResponse is `GET /api/v1/schedules`.
type ListSchedulesResponse struct {
	Items []ScheduleDeviceSummary `json:"items"`
}

// ListSchedules serves the fleet-wide overview of devices that carry a
// week schedule — the counterpart to the direct-links list, and the one
// way to answer "which devices have a schedule at all" without opening
// every device in turn.
func ListSchedules(svc ScheduleService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Schedule service unavailable", ""))
			return
		}
		items, err := svc.ListScheduleDevices(r.Context())
		if err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal,
				"Schedule list failed", err)
			return
		}
		if items == nil {
			items = []ScheduleDeviceSummary{}
		}
		JSON(w, http.StatusOK, ListSchedulesResponse{Items: items})
	}
}

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
			problem.Write(w, DecodeJSONStatus(err),
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
			problem.Write(w, DecodeJSONStatus(err),
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
			problem.Write(w, DecodeJSONStatus(err),
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
			problem.Write(w, DecodeJSONStatus(err),
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

// PostCopySchedule copies the whole week schedule from the path device
// to the target device named in the body. Both schedule channels are
// auto-resolved by the adapter.
func PostCopySchedule(svc ScheduleService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Schedule service unavailable", ""))
			return
		}
		addr := chi.URLParam(r, "addr")
		var body CopyScheduleRequest
		if err := DecodeJSON(r, &body); err != nil {
			problem.Write(w, DecodeJSONStatus(err),
				problem.New(problem.TypeBadRequest, r, "Invalid JSON", err.Error()))
			return
		}
		if body.TargetDeviceAddress == "" {
			problem.Write(w, http.StatusUnprocessableEntity,
				problem.New(problem.TypeValidation, r, "target_device_address is required", ""))
			return
		}
		if err := svc.CopySchedule(r.Context(), addr, body.TargetDeviceAddress); err != nil {
			writeScheduleError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

// PostCopyProfile copies a single climate profile from the path channel
// ({addr}:{no}) / source_profile to the target channel / target_profile
// named in the body.
func PostCopyProfile(svc ScheduleService) http.HandlerFunc {
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
		var body CopyProfileRequest
		if err := DecodeJSON(r, &body); err != nil {
			problem.Write(w, DecodeJSONStatus(err),
				problem.New(problem.TypeBadRequest, r, "Invalid JSON", err.Error()))
			return
		}
		if body.TargetChannelAddress == "" {
			problem.Write(w, http.StatusUnprocessableEntity,
				problem.New(problem.TypeValidation, r, "target_channel_address is required", ""))
			return
		}
		if !weekprofile.ValidProfileIndex(body.SourceProfile) || !weekprofile.ValidProfileIndex(body.TargetProfile) {
			problem.Write(w, http.StatusUnprocessableEntity,
				problem.New(problem.TypeValidation, r, "source_profile and target_profile must be 1..6", ""))
			return
		}
		srcChannelAddress := addr + ":" + strconv.Itoa(no)
		if err := svc.CopyClimateProfile(r.Context(), srcChannelAddress, body.SourceProfile, body.TargetChannelAddress, body.TargetProfile); err != nil {
			writeScheduleError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

// writeScheduleError maps a schedule-domain failure onto a problem
// response.
//
// The schedule sentinels live in pkg/hmerr, which both layers import:
// the domain package that raises them depends on this one (through the
// WebSocket command surface), so a direct import here is impossible and
// the classification would otherwise have to match error messages.
func writeScheduleError(w http.ResponseWriter, r *http.Request, err error) {
	// Device-not-found at the adapter layer maps to 404 — see
	// SchedulesDomain.resolve / FindScheduleChannel.
	if errors.Is(err, hmerr.ErrDescriptionNotFound) {
		problem.Write(w, http.StatusNotFound,
			problem.New(problem.TypeNotFound, r, "Device not found", ""))
		return
	}
	// A channel without schedule parameters is a 404 so the SPA can show
	// a friendly "device has no schedule" message. The copy source is
	// read through the same path, which wraps the sentinel.
	if errors.Is(err, hmerr.ErrNoSchedule) {
		problem.Write(w, http.StatusNotFound,
			problem.New(problem.TypeNotFound, r, "Channel has no climate schedule", ""))
		return
	}
	// A copy onto itself and an out-of-range profile index are caller
	// mistakes the domain rejects before any wire call: 422, never 502.
	if errors.Is(err, hmerr.ErrScheduleCopyNoOp) {
		problem.Write(w, http.StatusUnprocessableEntity,
			problem.New(problem.TypeValidation, r, "Copy source and destination are identical", ""))
		return
	}
	if errors.Is(err, hmerr.ErrScheduleCopyProfileRange) {
		problem.Write(w, http.StatusUnprocessableEntity,
			problem.New(problem.TypeValidation, r, "Profile index out of range (1..6)", ""))
		return
	}
	// A schedule write reaches the CCU through the backend; any remaining
	// failure is an upstream/gateway problem, not an internal one. Mirror
	// the value-write handler (devices.go) and return 502 — never 500
	// (TestRESTMutationWalker treats a mutation 500 as a bug).
	if problem.IsUpstreamUnavailable(err) {
		writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Upstream temporarily unavailable", err)
		return
	}
	writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Schedule request failed", err)
}
