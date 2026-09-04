// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ScheduleQueryAdapter wraps a [SchedulesDomain] in the
// `map[string]any` shape that the WebSocket command set
// (`internal/north/rest/ws.ScheduleQuery`) expects.
//
// The adapter exists because the SPA WS API is JSON-shaped while the
// REST handler layer uses typed `*hmapi.ClimateSchedule` DTOs. The
// translation is a JSON round-trip — cheap and deterministic.
type ScheduleQueryAdapter struct {
	domain *SchedulesDomain
}

// NewScheduleQueryAdapter wires the adapter.
func NewScheduleQueryAdapter(domain *SchedulesDomain) *ScheduleQueryAdapter {
	return &ScheduleQueryAdapter{domain: domain}
}

// GetClimateSchedule resolves the channel from `<addr>:<no>`-shaped
// channelAddress and returns the schedule as a JSON-able map.
func (a *ScheduleQueryAdapter) GetClimateSchedule(ctx context.Context, channelAddress string) (map[string]any, error) {
	if a.domain == nil {
		return nil, errors.New("schedule_query_adapter: nil domain")
	}
	deviceAddress, channelNo := splitChannelAddress(channelAddress)
	dto, err := a.domain.GetClimateSchedule(ctx, deviceAddress, channelNo)
	if err != nil {
		return nil, err
	}
	return scheduleToMap(dto)
}

// SetClimateSchedule decodes the payload into a *hmapi.ClimateSchedule
// and writes it back via the domain.
func (a *ScheduleQueryAdapter) SetClimateSchedule(
	ctx context.Context, channelAddress string, profile map[string]any,
) ([]hmapi.ClimateTimeCorrection, error) {
	if a.domain == nil {
		return nil, errors.New("schedule_query_adapter: nil domain")
	}
	deviceAddress, channelNo := splitChannelAddress(channelAddress)
	dto, err := mapToSchedule(profile)
	if err != nil {
		return nil, err
	}
	return a.domain.PutClimateSchedule(ctx, deviceAddress, channelNo, dto)
}

// SetActiveProfile maps the int profile index (1..6) onto the "P<n>"
// string the domain expects.
func (a *ScheduleQueryAdapter) SetActiveProfile(ctx context.Context, channelAddress string, profileIndex int) error {
	if a.domain == nil {
		return errors.New("schedule_query_adapter: nil domain")
	}
	deviceAddress, channelNo := splitChannelAddress(channelAddress)
	return a.domain.SetActiveProfile(ctx, deviceAddress, channelNo, fmt.Sprintf("P%d", profileIndex))
}

// GetDeviceSchedule auto-resolves the schedule channel and returns the
// schedule.
func (a *ScheduleQueryAdapter) GetDeviceSchedule(ctx context.Context, deviceAddress string) (map[string]any, error) {
	if a.domain == nil {
		return nil, errors.New("schedule_query_adapter: nil domain")
	}
	dto, err := a.domain.GetClimateScheduleAuto(ctx, deviceAddress)
	if err != nil {
		return nil, err
	}
	return scheduleToMap(dto)
}

// SetDeviceSchedule auto-resolves the schedule channel and writes the
// schedule.
func (a *ScheduleQueryAdapter) SetDeviceSchedule(
	ctx context.Context, deviceAddress string, profile map[string]any,
) ([]hmapi.ClimateTimeCorrection, error) {
	if a.domain == nil {
		return nil, errors.New("schedule_query_adapter: nil domain")
	}
	dto, err := mapToSchedule(profile)
	if err != nil {
		return nil, err
	}
	return a.domain.PutClimateScheduleAuto(ctx, deviceAddress, dto)
}

// SetDeviceActiveProfile auto-resolves the schedule channel and sets
// the active profile.
func (a *ScheduleQueryAdapter) SetDeviceActiveProfile(ctx context.Context, deviceAddress, profile string) error {
	if a.domain == nil {
		return errors.New("schedule_query_adapter: nil domain")
	}
	return a.domain.SetActiveProfileAuto(ctx, deviceAddress, profile)
}

// CopySchedule delegates to the domain's whole-device schedule copy.
func (a *ScheduleQueryAdapter) CopySchedule(ctx context.Context, srcDeviceAddress, dstDeviceAddress string) error {
	if a.domain == nil {
		return errors.New("schedule_query_adapter: nil domain")
	}
	return a.domain.CopySchedule(ctx, srcDeviceAddress, dstDeviceAddress)
}

// CopyClimateProfile delegates to the domain's single-profile copy.
func (a *ScheduleQueryAdapter) CopyClimateProfile(
	ctx context.Context, srcChannelAddress string, srcProfile int, dstChannelAddress string, dstProfile int,
) error {
	if a.domain == nil {
		return errors.New("schedule_query_adapter: nil domain")
	}
	return a.domain.CopyClimateProfile(ctx, srcChannelAddress, srcProfile, dstChannelAddress, dstProfile)
}

// splitChannelAddress separates "<dev>:<chn>" into its components.
// What counts as a channel suffix is decided by
// [hmtypes.SplitChannelAddress], so this parser and the canonical one
// cannot disagree. An address it does not accept is handed back whole with
// channel 0 — callers use the result to look a channel up, and a malformed
// address must not resolve to the device's channel 0.
func splitChannelAddress(channelAddress string) (device string, channelIdx int) {
	dev, n, ok := hmtypes.SplitChannelAddress(channelAddress)
	if !ok {
		return channelAddress, 0
	}
	return dev, n
}

// scheduleToMap turns a typed ClimateSchedule DTO into the JSON shape
// the WS layer hands clients. We round-trip through JSON so the field
// names (camelCase / snake_case) match the OpenAPI contract.
func scheduleToMap(dto *hmapi.ClimateSchedule) (map[string]any, error) {
	if dto == nil {
		return map[string]any{}, nil
	}
	raw, err := json.Marshal(dto)
	if err != nil {
		return nil, fmt.Errorf("schedule encode: %w", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("schedule decode: %w", err)
	}
	return out, nil
}

// mapToSchedule is the inverse of scheduleToMap.
func mapToSchedule(profile map[string]any) (*hmapi.ClimateSchedule, error) {
	raw, err := json.Marshal(profile)
	if err != nil {
		return nil, fmt.Errorf("profile encode: %w", err)
	}
	var dto hmapi.ClimateSchedule
	if err := json.Unmarshal(raw, &dto); err != nil {
		return nil, fmt.Errorf("profile decode: %w", err)
	}
	return &dto, nil
}
