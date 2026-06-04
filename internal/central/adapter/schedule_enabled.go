// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// schedule_enabled.go — SetScheduleEnabled on SchedulesDomain.
//
// Enables or disables the weekly program on a device by writing the
// COMBINED_PARAMETER VALUES data point on the device's schedule channel.
//
// The Go version does not use an async event model; it calls SetValue
// synchronously and then reads WEEK_PROGRAM_CHANNEL_LOCKS to refresh the
// in-model state.
//
// Wire format:
//
// COMBINED_PARAMETER = "WPTCLS={bitmask},WPTCL={mode}"
//
// where bitmask is derived from channel_key (e.g. "1_1" → 1) and mode is 0
// (MANU / disabled) or 2 (AUTO / enabled). A nil channel_key means "all
// registered channels".

package adapter

import (
	"context"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

const (
	// wptclAuto is the mode value that enables the weekly program (AUTO).
	wptclAuto = 2

	// wptclManu is the mode value that disables the weekly program (MANU).
	wptclManu = 0
)

// scheduleActorChannelBitmasks maps the canonical channel key (e.g. "1_1") to
// its bitmask value used in WPTCLS.
var scheduleActorChannelBitmasks = map[string]int{
	"1_1": 1,
	"1_2": 2,
	"1_3": 4,
	"2_1": 8,
	"2_2": 16,
	"2_3": 32,
	"3_1": 64,
	"3_2": 128,
	"3_3": 256,
	"4_1": 512,
	"4_2": 1024,
	"4_3": 2048,
	"5_1": 4096,
	"5_2": 8192,
	"5_3": 16384,
	"6_1": 32768,
	"6_2": 65536,
	"6_3": 131072,
	"7_1": 262144,
	"7_2": 524288,
	"7_3": 1048576,
	"8_1": 2097152,
	"8_2": 4194304,
	"8_3": 8388608,
}

// SetScheduleEnabled enables or disables the weekly program on the
// device identified by deviceAddress.
//
// When channelKey is non-empty, only the schedule for that channel is
// toggled (e.g. "1_1"). An empty channelKey toggles all channels
// currently registered in the model's ProfileDataPoint (same semantics
// As
//
// The operation writes the COMBINED_PARAMETER values data point on the
// schedule channel. The schedule channel is resolved via
// [SchedulesDomain.FindScheduleChannel].
//
// Implements ws.ScheduleEnabler.
func (s *SchedulesDomain) SetScheduleEnabled(
	ctx context.Context,
	deviceAddress string,
	enabled bool,
	channelKey string,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// Resolve the backend and the schedule channel number.
	channelNo, err := s.FindScheduleChannel(ctx, deviceAddress)
	if err != nil {
		return fmt.Errorf("schedules.SetScheduleEnabled: find channel: %w", err)
	}
	b, scheduleChannelAddr, err := s.resolveOps(deviceAddress, channelNo)
	if err != nil {
		return fmt.Errorf("schedules.SetScheduleEnabled: resolve backend: %w", err)
	}

	mode := wptclAuto
	if !enabled {
		mode = wptclManu
	}

	bitmask, maskErr := s.channelKeyBitmask(ctx, deviceAddress, channelKey)
	if maskErr != nil {
		return fmt.Errorf("schedules.SetScheduleEnabled: bitmask: %w", maskErr)
	}

	combinedValue := fmt.Sprintf("WPTCLS=%d,WPTCL=%d", bitmask, mode)
	if err := b.SetValue(
		ctx,
		scheduleChannelAddr,
		hmenum.ParameterCombinedParameter,
		combinedValue,
		hmenum.CommandPriorityHigh,
		hmenum.CommandRxModeUnset,
	); err != nil {
		return fmt.Errorf("schedules.SetScheduleEnabled: SetValue: %w", err)
	}

	// Update the in-model ProfileDataPoint to reflect the new state so
	// the SPA sees the change immediately without waiting for a CCU
	// callback. Best-effort: if the channel or data point is not yet
	// wired we skip silently.
	s.applyScheduleEnabledToModel(ctx, deviceAddress, channelKey, enabled)

	return nil
}

// channelKeyBitmask computes the WPTCLS bitmask for the given channel key.
// When channelKey is empty the bitmask is the OR of all channels registered
// In the device's ProfileDataPoint (same as
// path). When channelKey is non-empty and unknown a descriptive error is
// returned. When no ProfileDataPoint is found in the model the fallback is
// the bitmask for the single key "1_1" (the most common single-channel device).
func (s *SchedulesDomain) channelKeyBitmask(ctx context.Context, deviceAddress, channelKey string) (int, error) {
	_ = ctx // reserved for future use

	if channelKey != "" {
		bitmask, ok := scheduleActorChannelBitmasks[channelKey]
		if !ok {
			return 0, fmt.Errorf("unknown channel key %q", channelKey)
		}
		return bitmask, nil
	}

	// Empty channelKey → all channels. Walk the model's ProfileDataPoint
	// to discover which keys are registered.
	if s.registry == nil {
		return scheduleActorChannelBitmasks["1_1"], nil
	}
	for _, u := range s.registry.List() {
		dev, ok := u.ModelRegistry.Get(deviceAddress)
		if !ok {
			continue
		}
		for _, ch := range dev.Channels() {
			if ch.WeekProfile() == nil {
				continue
			}
			enabled := ch.WeekProfile().ScheduleEnabled()
			if len(enabled) == 0 {
				break
			}
			bitmask := 0
			for key := range enabled {
				if b, ok := scheduleActorChannelBitmasks[key]; ok {
					bitmask |= b
				}
			}
			if bitmask != 0 {
				return bitmask, nil
			}
			break
		}
		// Device found but no week profile with registered channels.
		break
	}

	// Default: single-channel device with key "1_1".
	return scheduleActorChannelBitmasks["1_1"], nil
}

// applyScheduleEnabledToModel updates the in-model ProfileDataPoint for
// the device so the SPA sees the change without waiting for a CCU event.
// Best-effort: silently returns when the device or channel cannot be found.
func (s *SchedulesDomain) applyScheduleEnabledToModel(ctx context.Context, deviceAddress, channelKey string, enabled bool) {
	if s.registry == nil {
		return
	}
	for _, u := range s.registry.List() {
		dev, ok := u.ModelRegistry.Get(deviceAddress)
		if !ok {
			continue
		}
		for _, ch := range dev.Channels() {
			wp := ch.WeekProfile()
			if wp == nil {
				continue
			}
			_ = wp.SetScheduleEnabled(ctx, channelKey, enabled, hmenum.CommandPriorityHigh)
			return
		}
		return
	}
}

// resolveOps locates the backend for the given device + channel number
// and returns the full backends.Operations interface (which includes
// SetValue) along with the qualified channel address.
func (s *SchedulesDomain) resolveOps(
	deviceAddress string, channelNo int,
) (backends.Operations, string, error) {
	if s.registry == nil || s.writer == nil {
		return nil, "", ErrNoScheduleBackend
	}
	for _, u := range s.registry.List() {
		dev, ok := u.ModelRegistry.Get(deviceAddress)
		if !ok {
			continue
		}
		b, ok := s.writer.Backend(u.Name(), dev.InterfaceID)
		if !ok {
			return nil, "", fmt.Errorf("%w: %s/%s", ErrNoScheduleBackend, u.Name(), dev.InterfaceID)
		}
		channelAddr := fmt.Sprintf("%s:%d", deviceAddress, channelNo)
		return b, channelAddr, nil
	}
	return nil, "", fmt.Errorf("%w: device %s", ErrNoScheduleBackend, deviceAddress)
}
