// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package device

import (
	"context"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// operationLockedWriter wraps a [ChannelWriter] so the operator channel
// lock is enforced on every command that leaves the model layer, not only
// on [Channel.Set] / [Channel.SetMany].
//
// Custom data points capture their write path once, at hydration time,
// through [Channel.Writer] and afterwards call it directly (Siren.Arm,
// Lock.Unlock, Climate.SetTemperature, …). Those commands never traverse
// Channel.Set, so without this wrapper an operator lock is advertised on
// the channel while every control surface — REST, WS, MQTT, Matter, the
// SPA tiles — still reaches the device.
//
// The lock flag is re-read on every call, so a writer captured before the
// operator toggled the lock observes the current state.
//
// The scope mirrors [Channel.Set]: VALUES writes are rejected, MASTER
// (configuration) writes and all reads are unaffected.
type operationLockedWriter struct {
	origin *Channel
	next   ChannelWriter
}

// SetValue rejects the write when the addressed channel is locked. The
// wire-level setValue always targets the VALUES paramset, so no paramset
// discrimination is needed here.
func (g *operationLockedWriter) SetValue(
	ctx context.Context,
	channelAddress string,
	parameter hmenum.Parameter,
	value any,
	priority hmenum.CommandPriority,
) error {
	if g.origin.writeTargetLocked(channelAddress) {
		return ErrChannelOperationLocked
	}
	return g.next.SetValue(ctx, channelAddress, parameter, value, priority)
}

// PutParamset rejects VALUES batches addressed at a locked channel.
// MASTER batches pass through — channel configuration stays editable
// while the channel is locked against control writes.
func (g *operationLockedWriter) PutParamset(
	ctx context.Context,
	channelAddress string,
	paramsetKey hmenum.ParamsetKey,
	values map[string]any,
	priority hmenum.CommandPriority,
) error {
	if paramsetKey == hmenum.ParamsetKeyValues && g.origin.writeTargetLocked(channelAddress) {
		return ErrChannelOperationLocked
	}
	return g.next.PutParamset(ctx, channelAddress, paramsetKey, values, priority)
}

// writeTargetLocked reports whether the channel addressed by
// channelAddress is locked against control writes.
//
// The address matters because a composed data point spans several
// channels of one device (a climate profile writes to its own channel and
// to the paired weather channel), so the lock of the channel that handed
// out the writer is not necessarily the lock that governs the write.
func (c *Channel) writeTargetLocked(channelAddress string) bool {
	return c.resolveWriteTarget(channelAddress).IsLocked()
}

// resolveWriteTarget maps a wire channel address onto the [Channel] whose
// operator flags govern a write to it. Addresses the owning device does
// not know fall back to the originating channel, so an unrecognised
// address can never widen the write surface.
func (c *Channel) resolveWriteTarget(channelAddress string) *Channel {
	if c == nil || channelAddress == "" || channelAddress == c.Address {
		return c
	}
	if d := c.Device(); d != nil {
		if target := d.Channel(channelAddress); target != nil {
			return target
		}
	}
	return c
}
