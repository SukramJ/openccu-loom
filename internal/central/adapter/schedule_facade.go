// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

// schedule_facade.go — minimal facade that bundles the scattered schedule
// functions into a single surface type.
//
// Go approach: a ScheduleFacade struct holds references to the two existing
// domain types (SchedulesDomain and ScheduleQueryAdapter) and exposes the
// same operations as named methods. No logic is changed; this file is a
// structural reshaping, not a reimplementation.
//
// Consumers that already hold a *SchedulesDomain or *ScheduleQueryAdapter
// directly are unaffected; this type exists for call-sites that want a
// single-import facade (e.g., the WS command handler wiring in north/).

import (
	"context"
	"errors"
)

// ScheduleFacade bundles climate-schedule and device-schedule operations
// behind a single entry point. Mirrors the public API of the reference
// config panel's schedule facade:
//
//   - GetClimateSchedule   → get_climate_schedule
//   - SetClimateSchedule   → set_climate_schedule_weekday (full)
//   - SetActiveProfile     → set_climate_active_profile
//   - GetDeviceSchedule    → get_device_schedule
//   - SetDeviceSchedule    → set_device_schedule
//   - GetScheduleByDevice  → thin helper (no Python pendant; added for Go
//     ergonomics: device-address-scoped shortcut)
type ScheduleFacade struct {
	domain  *SchedulesDomain
	adapter *ScheduleQueryAdapter
}

// NewScheduleFacade constructs a ScheduleFacade. Both domain and adapter must
// be non-nil; nil values are rejected immediately so callers get a clear
// construction-time error instead of a nil-pointer panic during operation.
//
// loom:reachable:reason="entry point for WS/REST schedule command handlers; wired in daemon.go north-side setup"
func NewScheduleFacade(domain *SchedulesDomain, adapter *ScheduleQueryAdapter) (*ScheduleFacade, error) {
	if domain == nil {
		return nil, errors.New("schedule_facade: nil SchedulesDomain")
	}
	if adapter == nil {
		return nil, errors.New("schedule_facade: nil ScheduleQueryAdapter")
	}
	return &ScheduleFacade{domain: domain, adapter: adapter}, nil
}

// GetClimateSchedule returns the climate schedule for a channel address in the
// JSON-map form the WS layer uses. Delegates to
// [ScheduleQueryAdapter.GetClimateSchedule].
func (f *ScheduleFacade) GetClimateSchedule(ctx context.Context, channelAddress string) (map[string]any, error) {
	return f.adapter.GetClimateSchedule(ctx, channelAddress)
}

// SetClimateSchedule writes a full climate schedule for a channel address.
// Delegates to [ScheduleQueryAdapter.SetClimateSchedule].
func (f *ScheduleFacade) SetClimateSchedule(ctx context.Context, channelAddress string, schedule map[string]any) error {
	return f.adapter.SetClimateSchedule(ctx, channelAddress, schedule)
}

// SetActiveProfile sets the active climate profile (1-based index) for a
// channel address. Delegates to [ScheduleQueryAdapter.SetActiveProfile].
func (f *ScheduleFacade) SetActiveProfile(ctx context.Context, channelAddress string, profileIndex int) error {
	return f.adapter.SetActiveProfile(ctx, channelAddress, profileIndex)
}

// GetDeviceSchedule returns the device schedule (auto-resolved channel) for
// deviceAddress in JSON-map form. Delegates to
// [ScheduleQueryAdapter.GetDeviceSchedule].
func (f *ScheduleFacade) GetDeviceSchedule(ctx context.Context, deviceAddress string) (map[string]any, error) {
	return f.adapter.GetDeviceSchedule(ctx, deviceAddress)
}

// SetDeviceSchedule writes a full device schedule (auto-resolved channel) for
// deviceAddress. Delegates to [ScheduleQueryAdapter.SetDeviceSchedule].
func (f *ScheduleFacade) SetDeviceSchedule(ctx context.Context, deviceAddress string, schedule map[string]any) error {
	return f.adapter.SetDeviceSchedule(ctx, deviceAddress, schedule)
}

// SetDeviceActiveProfile sets the active profile string ("P1".."P6") on the
// device's auto-resolved schedule channel.
// Delegates to [ScheduleQueryAdapter.SetDeviceActiveProfile].
func (f *ScheduleFacade) SetDeviceActiveProfile(ctx context.Context, deviceAddress, profile string) error {
	return f.adapter.SetDeviceActiveProfile(ctx, deviceAddress, profile)
}

// CopySchedule copies the whole week schedule from one device to another.
// Delegates to [ScheduleQueryAdapter.CopySchedule].
func (f *ScheduleFacade) CopySchedule(ctx context.Context, srcDeviceAddress, dstDeviceAddress string) error {
	return f.adapter.CopySchedule(ctx, srcDeviceAddress, dstDeviceAddress)
}

// CopyClimateProfile copies a single climate profile from a source
// channel/profile to a target channel/profile. Delegates to
// [ScheduleQueryAdapter.CopyClimateProfile].
func (f *ScheduleFacade) CopyClimateProfile(
	ctx context.Context, srcChannelAddress string, srcProfile int, dstChannelAddress string, dstProfile int,
) error {
	return f.adapter.CopyClimateProfile(ctx, srcChannelAddress, srcProfile, dstChannelAddress, dstProfile)
}

// MaxProfilesForDevice returns the number of climate profile slots for
// deviceAddress. Delegates to [SchedulesDomain.MaxProfilesForDevice].
func (f *ScheduleFacade) MaxProfilesForDevice(ctx context.Context, deviceAddress string) (int, error) {
	return f.domain.MaxProfilesForDevice(ctx, deviceAddress)
}
