// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package device

import (
	"github.com/SukramJ/openccu-loom/internal/payload"
)

// Compile-time guarantee that *Channel satisfies the universal Source
// contract. Step 2 of the ADR 0007 migration: read trio + the embedded
// ServiceRegistry that lives on the Channel struct give Channel the
// full Source surface.
var _ payload.Source = (*Channel)(nil)

// Info returns the channel's identity-level fields:
//
//   - address         — full CCU address ("HmIP-RF:000123ABCD:1")
//   - channel_no      — channel index on the parent device
//   - type            — CCU CHANNEL_TYPE marker
//   - name            — operator-assigned name (omitted when empty)
//   - rooms           — assigned rooms (omitted when empty)
//   - functions       — assigned functions (omitted when empty)
//   - room            — single resolved room with group-master fallback
//     (omitted when ambiguous or unresolved)
//   - group_no        — channel-group number (omitted when 0)
//   - is_group_master — true when this channel is the group master
//     (only present when group_no != 0)
//
// Returns nil for a nil receiver to keep callers (HA-Discovery aggregators)
// safe in degenerate fixtures.
func (c *Channel) Info() payload.InfoPayload {
	if c == nil {
		return nil
	}
	info := &payload.ChannelInfo{
		Address:   c.Address,
		ChannelNo: c.Number,
		Type:      c.Type,
		Name:      c.Name,
	}
	if len(c.Rooms) > 0 {
		info.Rooms = append([]string(nil), c.Rooms...)
	}
	if len(c.Functions) > 0 {
		info.Functions = append([]string(nil), c.Functions...)
	}
	info.Room = c.Room()
	if c.GroupNo != 0 {
		info.GroupNo = c.GroupNo
		info.IsGroupMaster = c.IsGroupMaster()
		if c.IsInMultiGroup() {
			info.IsInMultiGroup = true
			info.SubDeviceName = c.SubDeviceName()
		}
	}
	return info
}

// Config returns the channel's configuration-level fields:
//
//   - operation_mode — value of CHANNEL_OPERATION_MODE if observed
//     (omitted when not exposed or not yet read)
//   - paramset_in    — the channel's MASTER-input paramset key marker
//     (omitted when unset)
//
// Returns nil when no config-level fields apply, matching the Source
// contract that empty payloads collapse to nil.
func (c *Channel) Config() payload.ConfigPayload {
	if c == nil {
		return nil
	}
	cfg := &payload.ChannelConfig{
		OperationMode: c.OperationMode(),
		ParamsetIn:    string(c.ParamsetIn),
	}
	if cfg.OperationMode == "" && cfg.ParamsetIn == "" {
		return nil
	}
	return cfg
}

// State returns nil — Channel is a container for data points, not a
// state-holder itself. Per-parameter live state lives on the individual data
// points reachable through [Channel.DataPoints] and
// [Channel.MasterDataPoints].
func (c *Channel) State() payload.StatePayload {
	return nil
}
