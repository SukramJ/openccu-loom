// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"net/http"

	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
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
	Address            string          `json:"address"`
	ScheduleType       string          `json:"schedule_type"`
	MinTemp            float64         `json:"min_temp"`
	MaxTemp            float64         `json:"max_temp"`
	ProfileCount       int             `json:"profile_count"`
	CurrentProfile     string          `json:"current_profile,omitempty"`
	AvailableProfiles  []string        `json:"available_profiles,omitempty"`
	ScheduleEnabled    map[string]bool `json:"schedule_enabled,omitempty"`
	HasClimateSchedule bool            `json:"has_climate_schedule"`
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

		resp := WeekProfileResponse{
			Address:            ch.Address,
			ScheduleType:       schedType,
			MinTemp:            wp.MinTemp(),
			MaxTemp:            wp.MaxTemp(),
			ProfileCount:       wp.ProfileCount(),
			CurrentProfile:     wp.CurrentProfile(),
			AvailableProfiles:  wp.AvailableProfiles(),
			ScheduleEnabled:    enabled,
			HasClimateSchedule: wp.Climate() != nil,
		}
		JSON(w, http.StatusOK, resp)
	}
}
