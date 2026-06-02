// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// channel_switch.go — ChannelSwitch is a per-channel boolean DP that
// exposes the schedule-enabled flag of one schedule target channel.
// One switch is created per available schedule-target channel; toggling
// it calls ProfileDataPoint.SetScheduleEnabled for the corresponding
// channel key.

package weekprofile

import (
	"context"
	"sync"

	"github.com/SukramJ/openccu-loom/internal/model/datapoint"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ChannelSwitch is a boolean data point that reflects and controls the
// schedule participation of one channel on a device. Its value mirrors the
// per-key entry in [ProfileDataPoint.ScheduleEnabled].
//
// # The stable unique ID is
//
// <central>:<deviceAddress>:SCHEDULE_CHANNEL_LOCK_<channelKey>
//
// Thread-safe: the embedded [datapoint.BaseDataPointFields] and the local mu
// guard all mutable state.
type ChannelSwitch struct {
	datapoint.BaseDataPointFields

	mu         sync.RWMutex
	channelKey string
	profile    *ProfileDataPoint
}

// NewChannelSwitch constructs a ChannelSwitch for the given device address
// and channel key. The channelKey must match one of the keys registered on
// profile via [ProfileDataPoint.RegisterChannel].
//
// The UniqueID is computed as
//
//	<central>:<deviceAddress>:SCHEDULE_CHANNEL_LOCK_<channelKey>
func NewChannelSwitch(centralName, deviceAddress, channelKey string, profile *ProfileDataPoint) *ChannelSwitch {
	keyName := "SCHEDULE_CHANNEL_LOCK_" + channelKey
	cs := &ChannelSwitch{
		BaseDataPointFields: datapoint.NewBaseDataPointFields(centralName, deviceAddress, keyName),
		channelKey:          channelKey,
		profile:             profile,
	}
	return cs
}

// ChannelKey returns the schedule channel key this switch controls.
func (cs *ChannelSwitch) ChannelKey() string {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	return cs.channelKey
}

// Value returns whether the schedule is currently enabled for this channel,
// or nil when the parent ProfileDataPoint has no schedule-enabled map yet.
func (cs *ChannelSwitch) Value() *bool {
	if cs.profile == nil {
		return nil
	}
	enabled := cs.profile.ScheduleEnabled()
	if enabled == nil {
		return nil
	}
	cs.mu.RLock()
	key := cs.channelKey
	cs.mu.RUnlock()
	v, ok := enabled[key]
	if !ok {
		return nil
	}
	return &v
}

// TurnOn enables the schedule for this channel. Dispatches the wire
// write through the bound ProfileDataPoint's writer.
func (cs *ChannelSwitch) TurnOn(ctx context.Context, priority hmenum.CommandPriority) error {
	cs.mu.RLock()
	key := cs.channelKey
	prof := cs.profile
	cs.mu.RUnlock()
	if prof == nil {
		return nil
	}
	return prof.SetScheduleEnabled(ctx, key, true, priority)
}

// TurnOff disables the schedule for this channel.
func (cs *ChannelSwitch) TurnOff(ctx context.Context, priority hmenum.CommandPriority) error {
	cs.mu.RLock()
	key := cs.channelKey
	prof := cs.profile
	cs.mu.RUnlock()
	if prof == nil {
		return nil
	}
	return prof.SetScheduleEnabled(ctx, key, false, priority)
}

// Set is a convenience wrapper that calls [TurnOn] or [TurnOff] depending
// on the supplied flag. It uses the default command priority so callers
// that don't need fine-grained priority control can use one method instead
// of branching between TurnOn and TurnOff.
func (cs *ChannelSwitch) Set(ctx context.Context, enabled bool) error {
	if enabled {
		return cs.TurnOn(ctx, hmenum.CommandPriorityHigh)
	}
	return cs.TurnOff(ctx, hmenum.CommandPriorityHigh)
}

// Subscribe registers a callback that is invoked whenever the schedule-
// enabled state of this channel changes. The callback receives the new
// boolean value. The returned function unsubscribes idempotently.
//
// The subscription is backed by [ProfileDataPoint.OnChange]: the parent
// DP fires its change callbacks after every [ProfileDataPoint.SetScheduleEnabled]
// or [ProfileDataPoint.SyncScheduleEnabled] call, and this wrapper reads
// the per-channel value from the DP's map at callback time so the caller
// always sees the current state.
func (cs *ChannelSwitch) Subscribe(fn func(enabled bool)) func() {
	return cs.profile.OnChange(func() {
		v := cs.Value()
		if v == nil {
			return
		}
		fn(*v)
	})
}

// Category returns [hmenum.DataPointCategoryScheduleSwitch] so
// the north-bound layer can route the entity to the correct HA
// `switch` component and MQTT topic plane.
func (cs *ChannelSwitch) Category() hmenum.DataPointCategory {
	return hmenum.DataPointCategoryScheduleSwitch
}

// Signature returns a compact identifier in the form
//
// schedule_switch/<parameter>
func (cs *ChannelSwitch) Signature() string {
	cs.mu.RLock()
	key := cs.channelKey
	cs.mu.RUnlock()
	return string(hmenum.DataPointCategoryScheduleSwitch) + "/SCHEDULE_CHANNEL_LOCK_" + key
}
