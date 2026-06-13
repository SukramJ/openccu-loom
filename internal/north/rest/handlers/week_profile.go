// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// WeekProfileResponse is the read-only metadata descriptor returned by
// GET /api/v1/devices/{addr}/channels/{no}/week_profile.
//
// It surfaces the pipeline-attached [weekprofile.ProfileDataPoint] so
// north-bound consumers (SPA, external tooling) can discover schedule
// capabilities without parsing raw MASTER paramset keys.
//
// JSON contract:
//   - schedule_type: "climate" | "default"
//   - schedule_enabled: omitted when the map is empty (saves bytes for
//     devices that do not have channel-based schedule locks)
//   - has_climate_schedule: true when a ClimateProfile backend is bound;
//     false otherwise (hint only — does not trigger a Load call)
type WeekProfileResponse struct {
	Address                 string                          `json:"address"`
	ScheduleType            string                          `json:"schedule_type"`
	MinTemp                 float64                         `json:"min_temp"`
	MaxTemp                 float64                         `json:"max_temp"`
	ProfileCount            int                             `json:"profile_count"`
	CurrentProfile          string                          `json:"current_profile,omitempty"`
	AvailableProfiles       []string                        `json:"available_profiles,omitempty"`
	ScheduleEnabled         map[string]bool                 `json:"schedule_enabled,omitempty"`
	AvailableTargetChannels map[string]TargetChannelSummary `json:"available_target_channels,omitempty"`
	HasClimateSchedule      bool                            `json:"has_climate_schedule"`
}

// TargetChannelSummary describes the schedule-controllable target channel a
// channel-lock key (e.g. "1_1") maps to. It lets north-bound consumers name a
// per-channel schedule switch after the actuator channel it controls (the
// reference stack composes "<channel name> Schedule") without re-deriving the
// channel-group layout themselves.
type TargetChannelSummary struct {
	ChannelNo      int    `json:"channel_no"`
	ChannelAddress string `json:"channel_address"`
	Name           string `json:"name"`
	// ChannelType is "primary" or "secondary".
	ChannelType string `json:"channel_type"`
}

// GetWeekProfile returns the week-profile metadata descriptor for one
// channel.
//
// Route: GET /api/v1/devices/{addr}/channels/{no}/week_profile
//
// Error cases:
//   - 404 when the device or channel cannot be found (via lookupChannel)
//   - 404 with body {"error":"no week profile on channel"} when the channel
//     exists but has no attached [weekprofile.ProfileDataPoint]
//
// Spec: assets/openapi.yaml → /devices/{addr}/channels/{no}/week_profile (GET).
func GetWeekProfile(idx DeviceIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ch, err := lookupChannel(idx, r)
		if err != nil {
			problem.WriteFromError(w, r, err)
			return
		}

		wp := ch.WeekProfile()
		if wp == nil {
			problem.Write(w, http.StatusNotFound,
				problem.New(problem.TypeNotFound, r, "No week profile on channel", "no week profile on channel"))
			return
		}

		schedType := "default"
		if wp.ScheduleType() == weekprofile.ScheduleTypeClimate {
			schedType = "climate"
		}

		enabled := wp.ScheduleEnabled()
		// Omit schedule_enabled when empty — zero-length map serialises to
		// {} which wastes bytes for devices without channel-locks.
		if len(enabled) == 0 {
			enabled = nil
		}

		// Surface the channel-lock key -> target channel mapping so consumers
		// can name a per-channel schedule switch after the actuator channel.
		var targets map[string]TargetChannelSummary
		if registered := wp.AvailableTargetChannels(); len(registered) > 0 {
			targets = make(map[string]TargetChannelSummary, len(registered))
			for key, info := range registered {
				targets[key] = TargetChannelSummary{
					ChannelNo:      info.ChannelNo,
					ChannelAddress: info.ChannelAddress,
					Name:           info.Name,
					ChannelType:    info.ChannelType,
				}
			}
		}

		resp := WeekProfileResponse{
			Address:                 ch.Address,
			ScheduleType:            schedType,
			MinTemp:                 wp.MinTemp(),
			MaxTemp:                 wp.MaxTemp(),
			ProfileCount:            wp.ProfileCount(),
			CurrentProfile:          wp.CurrentProfile(),
			AvailableProfiles:       wp.AvailableProfiles(),
			ScheduleEnabled:         enabled,
			AvailableTargetChannels: targets,
			HasClimateSchedule:      wp.Climate() != nil,
		}
		JSON(w, http.StatusOK, resp)
	}
}

// ChannelLockRequest is the body of
// PUT /devices/{addr}/channels/{no}/week_profile/channel-locks/{key}.
type ChannelLockRequest struct {
	// Enabled includes (true) or excludes (false) the target channel
	// from the device's week-program schedule.
	Enabled bool `json:"enabled"`
}

// PutWeekProfileChannelLock toggles schedule participation of one target
// channel — the write half of the `schedule_enabled` map in
// [WeekProfileResponse]. External clients drive their schedule-channel
// switch entities through it.
//
// Route: PUT /api/v1/devices/{addr}/channels/{no}/week_profile/channel-locks/{key}
func PutWeekProfileChannelLock(idx DeviceIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ch, err := lookupChannel(idx, r)
		if err != nil {
			problem.WriteFromError(w, r, err)
			return
		}
		wp := ch.WeekProfile()
		if wp == nil {
			problem.Write(w, http.StatusNotFound,
				problem.New(problem.TypeNotFound, r, "No week profile on channel", "no week profile on channel"))
			return
		}
		key := chi.URLParam(r, "key")
		if _, ok := wp.ScheduleEnabled()[key]; !ok {
			problem.Write(w, http.StatusNotFound,
				problem.New(problem.TypeNotFound, r, "Unknown channel key", "no schedule target channel "+key))
			return
		}
		var req ChannelLockRequest
		if err := DecodeJSON(r, &req); err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Invalid JSON", err.Error()))
			return
		}
		if err := wp.SetScheduleEnabled(r.Context(), key, req.Enabled, hmenum.CommandPriorityHigh); err != nil {
			problem.Write(w, http.StatusBadGateway,
				problem.New(problem.TypeInternal, r, "Channel lock write failed", err.Error()))
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}
