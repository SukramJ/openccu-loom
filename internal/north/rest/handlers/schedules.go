// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// ScheduleService is the facade for climate-schedule endpoints. Port
// of the get_schedule / set_schedule / set_active_profile surface
//
// Two flavours: explicit-channel methods are kept for back-compat
// (older SPA versions, scripted clients); the *Auto variants resolve
// the schedule channel from the device automatically — same logic
// As 's _resolve_climate_schedule_channel.
type ScheduleService interface {
	GetClimateSchedule(ctx context.Context, deviceAddress string, channelNo int) (*ClimateSchedule, error)
	PutClimateSchedule(ctx context.Context, deviceAddress string, channelNo int, schedule *ClimateSchedule) error
	SetActiveProfile(ctx context.Context, deviceAddress string, channelNo int, profile string) error

	GetClimateScheduleAuto(ctx context.Context, deviceAddress string) (*ClimateSchedule, error)
	PutClimateScheduleAuto(ctx context.Context, deviceAddress string, schedule *ClimateSchedule) error
	SetActiveProfileAuto(ctx context.Context, deviceAddress, profile string) error
	FindScheduleChannel(ctx context.Context, deviceAddress string) (int, error)
}

// --- DTOs ---------------------------------------------------------

// ClimateSchedule is the schedule payload returned by GET
// /devices/{addr}/schedule. The shape carries both flavours so the SPA can
// render a single response — "kind" disambiguates:
//
// - "climate" — `profiles` (P1..P6) is populated and `simple_entries` is
// empty. Used by thermostats with `P<n>_*` paramsets. - "simple"  —
// `simple_entries` is populated and `profiles` is empty. Used by switches /
// covers / lights with `<NN>_WP_*` paramsets (HmIP-PSM, HmIP-FSM, …).
//
// `Domain` further specialises a "simple" schedule so the SPA can pick the
// matching editor widgets ("switch" → on/off toggle, "light" → slider + ramp,
// "cover" → two sliders, "lock" → action dropdown).
type ClimateSchedule struct {
	Channel       ScheduleChannelRef `json:"channel"`
	Kind          string             `json:"kind"`
	Domain        string             `json:"domain,omitempty"`
	ActiveProfile string             `json:"active_profile,omitempty"`
	// ActiveProfileIndex is the 0-based integer index of the currently
	// active climate profile as reported by the CCU. Nil when the device
	// does not report a numeric active-profile index (e.g. simple-schedule
	// devices). The SPA uses this to pre-select the profile tab.
	ActiveProfileIndex *int                      `json:"active_profile_index,omitempty"`
	Profiles           map[string]ClimateProfile `json:"profiles,omitempty"`
	SimpleEntries      []SimpleScheduleEntry     `json:"simple_entries,omitempty"`
}

// SimpleScheduleEntry is one switching slot for a non-climate device.
// Full port of
// up to 24 such slots per channel.
//
// Trigger composition: the slot fires when the `condition` evaluates
// to true. Conditions combine `time` (fixed HH:MM) with optional
// astro events (`sunrise` / `sunset` ± `astro_offset_minutes`).
//
// Target composition: `target_channels` selects which actor channels
// the slot drives (e.g. "1_1" for channel 1, function 1). Empty list
// means "the CCU's default target".
//
// Action composition: `level` is the value the channel is set to at
// the trigger instant. Optional `level_2` carries cover slat
// position. `duration` keeps the actor at this level for a fixed
// time (auto-revert), `ramp_time` controls the dimmer ramp.
type SimpleScheduleEntry struct {
	// SlotNo is 1..24 — preserved so a partial update keeps unrelated
	// slots intact on the CCU.
	SlotNo int `json:"slot_no"`

	// --- Trigger -----------------------------------------------
	Weekdays []string `json:"weekdays"`
	// Time is the fixed switching time in 24-hour HH:MM. Required
	// even for astro conditions because the CCU stores it always.
	Time string `json:"time"`
	// Condition is one of:
	//   "fixed_time"                  — fire at Time only.
	//   "astro"                       — fire at astro event ± offset.
	//   "fixed_if_before_astro" / "astro_if_before_fixed"
	//   "fixed_if_after_astro"  / "astro_if_after_fixed"
	//   "earliest_of_fixed_and_astro" / "latest_of_fixed_and_astro"
	// Defaults to "fixed_time" when empty / unknown.
	Condition string `json:"condition,omitempty"`
	// AstroType is "sunrise" or "sunset". Required for any astro-
	// involving condition; ignored for "fixed_time".
	AstroType string `json:"astro_type,omitempty"`
	// AstroOffsetMinutes shifts the astro event (-720..+720).
	AstroOffsetMinutes int `json:"astro_offset_minutes,omitempty"`

	// --- Target ------------------------------------------------
	// TargetChannels addresses output sub-channels in "X_Y" notation
	// (X=1..8 actor channel, Y=1..3 function). Empty list = CCU
	// default routing.
	TargetChannels []string `json:"target_channels,omitempty"`

	// --- Action ------------------------------------------------
	Level  float64  `json:"level"`
	Level2 *float64 `json:"level_2,omitempty"`
	// Duration / RampTime are human-readable strings: "10s", "5min",
	// "1h", "100ms", "500ms", "2s", … The (de)serializer maps them
	// onto the CCU's TimeBase + factor pair.
	Duration string `json:"duration,omitempty"`
	RampTime string `json:"ramp_time,omitempty"`

	// --- Lock-only fields ---------------------------------------
	// LockMode is "door_lock" or "user_permission" — picks how the
	// rest of the lock-domain fields are encoded on the wire.
	// LockMode = "door_lock":
	//   * LockAction sets the LEVEL+DURATION pair via the standard
	// KeyMatic encoding (see
	//     mappings).
	//   * Permission must be empty.
	// LockMode = "user_permission":
	//   * Permission ("granted" / "not_granted") sets LEVEL.
	//   * DURATION is forced to (HOUR_1, 31) — same as
	//   * LockAction must be empty.
	LockMode   string `json:"lock_mode,omitempty"`
	LockAction string `json:"lock_action,omitempty"`
	Permission string `json:"permission,omitempty"`
}

// ScheduleChannelRef identifies the owning channel so the SPA can
// cross-reference with the device detail page without carrying the
// URL params around separately.
type ScheduleChannelRef struct {
	Address string `json:"address"`
	Number  int    `json:"number"`
	Device  string `json:"device_address"`
}

// ClimateProfile is one named profile (P1..P6) with the seven
// weekday slots. Missing weekdays are valid — the thermostat falls
// back to its base temperature.
type ClimateProfile struct {
	Weekdays map[string]ClimateWeekday `json:"weekdays"`
}

// ClimateWeekday is the simplified weekday form (base + periods), as
// opposed to the 13-slot CCU wire format. The adapter does the
// conversion in both directions.
type ClimateWeekday struct {
	BaseTemperature float64         `json:"base_temperature"`
	Periods         []ClimatePeriod `json:"periods"`
}

// ClimatePeriod is one non-base-temperature stretch.
type ClimatePeriod struct {
	StartTime   string  `json:"start_time"`
	EndTime     string  `json:"end_time"`
	Temperature float64 `json:"temperature"`
}

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
	problem.Write(w, http.StatusInternalServerError,
		problem.New(problem.TypeInternal, r, "Schedule request failed", err.Error()))
}
