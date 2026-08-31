// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// schedule_enabled.go — SetScheduleEnabled on SchedulesDomain.
//
// Enables or disables the weekly program on a device by writing the
// COMBINED_PARAMETER VALUES data point on the device's schedule channel.
//
// The Go version does not use an async event model; it calls SetValue
// synchronously and then reads WEEK_PROGRAM_CHANNEL_LOCKS to refresh the
// in-model state.
//
// The COMBINED_PARAMETER payload and the channel-key bit table are wire
// semantics of the week-profile domain object: this file renders neither
// itself but asks internal/model/weekprofile (channel_keys.go) for both,
// so one bit assignment serves the modelled write path and this fallback.
// An empty channel_key means "all registered channels".

package adapter

import (
	"context"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// scheduleFallbackChannelKey is the channel targeted when the device
// carries no modelled week profile to enumerate: the first actor of the
// first group, which is what a single-channel device exposes.
const scheduleFallbackChannelKey = "1_1"

// scheduleFallbackBitmask resolves [scheduleFallbackChannelKey] through the
// model's bit table. Which key to fall back to is wiring policy and stays
// here; what bit that key carries is not. The error path matters: a silent
// miss would degrade to bitmask 0, and "WPTCLS=0" is a write the CCU accepts
// while it targets no channel at all.
func scheduleFallbackBitmask() (uint32, error) {
	bit, ok := weekprofile.ChannelKeyToBitmask(scheduleFallbackChannelKey)
	if !ok || bit == 0 {
		return 0, fmt.Errorf("unknown channel key %q", scheduleFallbackChannelKey)
	}
	return bit, nil
}

// SetScheduleEnabled enables or disables the weekly program on the
// device identified by deviceAddress.
//
// When channelKey is non-empty, only the schedule for that channel is
// toggled (e.g. "1_1"). An empty channelKey toggles all channels
// currently registered in the model's ProfileDataPoint.
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

	// A modelled week profile with an attached writer already performs this
	// exact CCU write: it renders the same `WPTCLS=<bits>,WPTCL=<0|2>` value,
	// targets the same schedule channel, and additionally arms the echo hold
	// window and rolls its optimistic state back when the CCU rejects the
	// write. Writing here as well sent the identical frame to the device twice
	// per operator click — double radio and duty-cycle cost — and left the
	// model reverting a write the CCU had already taken. Delegate instead; the
	// backend path below stays for devices whose schedule is not modelled or
	// whose writer the pipeline has not attached.
	if wp := s.scheduleProfileFor(deviceAddress); wp != nil && wp.ScheduleChannelAddress() != "" &&
		(channelKey != "" || len(wp.ScheduleEnabled()) > 0) {
		if err := wp.SetScheduleEnabled(ctx, channelKey, enabled, hmenum.CommandPriorityHigh); err != nil {
			return fmt.Errorf("schedules.SetScheduleEnabled: %w", err)
		}
		return nil
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

	bitmask, maskErr := s.channelKeyBitmask(ctx, deviceAddress, channelKey)
	if maskErr != nil {
		return fmt.Errorf("schedules.SetScheduleEnabled: bitmask: %w", maskErr)
	}

	combinedValue := weekprofile.BuildCombinedParameterValue(bitmask, enabled)
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

// channelKeyBitmask computes the WPTCLS bitmask for the given channel key,
// resolving every key through the model's bit table.
//
// When channelKey is empty the bitmask is the OR of all channels registered
// in the device's ProfileDataPoint. When channelKey is non-empty and unknown
// a descriptive error is returned. When no ProfileDataPoint is found in the
// model the fallback is [scheduleFallbackChannelKey], the shape of the most
// common single-channel device.
func (s *SchedulesDomain) channelKeyBitmask(ctx context.Context, deviceAddress, channelKey string) (uint32, error) {
	_ = ctx // reserved for future use

	if channelKey != "" {
		bitmask, ok := weekprofile.ChannelKeyToBitmask(channelKey)
		if !ok {
			return 0, fmt.Errorf("unknown channel key %q", channelKey)
		}
		return bitmask, nil
	}

	// Empty channelKey → all channels. Walk the model's ProfileDataPoint
	// to discover which keys are registered.
	if s.registry == nil {
		return scheduleFallbackBitmask()
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
			var bitmask uint32
			for key := range enabled {
				if b, ok := weekprofile.ChannelKeyToBitmask(key); ok {
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

	// Default: single-channel device.
	return scheduleFallbackBitmask()
}

// scheduleProfileFor returns the week-profile data point of the first
// channel of deviceAddress that carries one, or nil when the device is
// unknown or has no modelled schedule.
func (s *SchedulesDomain) scheduleProfileFor(deviceAddress string) *weekprofile.ProfileDataPoint {
	if s.registry == nil {
		return nil
	}
	for _, u := range s.registry.List() {
		dev, ok := u.ModelRegistry.Get(deviceAddress)
		if !ok {
			continue
		}
		for _, ch := range dev.Channels() {
			if wp := ch.WeekProfile(); wp != nil {
				return wp
			}
		}
		return nil
	}
	return nil
}

// applyScheduleEnabledToModel updates the in-model ProfileDataPoint for
// the device so the SPA sees the change without waiting for a CCU event.
// Only reached on the backend-write fallback, where the data point holds no
// writer of its own — so this stays an in-memory update.
// Best-effort: silently returns when the device or channel cannot be found.
func (s *SchedulesDomain) applyScheduleEnabledToModel(ctx context.Context, deviceAddress, channelKey string, enabled bool) {
	if wp := s.scheduleProfileFor(deviceAddress); wp != nil {
		_ = wp.SetScheduleEnabled(ctx, channelKey, enabled, hmenum.CommandPriorityHigh)
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
		b, ok := s.writer.Backend(u.Name(), hmtypes.ParseWireInterfaceID(dev.InterfaceID))
		if !ok {
			return nil, "", fmt.Errorf("%w: %s/%s", ErrNoScheduleBackend, u.Name(), dev.InterfaceID)
		}
		channelAddr := fmt.Sprintf("%s:%d", deviceAddress, channelNo)
		return b, channelAddr, nil
	}
	return nil, "", fmt.Errorf("%w: device %s", ErrNoScheduleBackend, deviceAddress)
}
