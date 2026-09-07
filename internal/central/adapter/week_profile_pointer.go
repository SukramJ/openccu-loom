// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// profilePointer names the data point a device uses to select its active
// week program, together with the address and paramset key a write to it
// must target.
//
// Two shapes exist and they do not live in the same place:
//
//   - HmIP thermostats (HmIP-eTRV, -BWTH, -STH, …) declare ACTIVE_PROFILE
//     as a 1-based INTEGER in the VALUES paramset of the climate channel.
//   - Classic RF thermostats (HM-TC-IT-WM-W-EU, HM-CC-VG-1) declare
//     WEEK_PROGRAM_POINTER as a 0-based option type in the *device-root*
//     MASTER paramset — there is no channel that carries it.
//
// Walking only `Device.Channels()` and only `Channel.Parameter` (the
// VALUES map) therefore misses the whole classic-RF family, which is why
// every consumer goes through this one lookup.
type profilePointer struct {
	dataPoint      device.ParameterDataPoint
	parameter      hmenum.Parameter
	paramsetKey    hmenum.ParamsetKey
	channelAddress string
}

// findProfilePointer locates the device's week-program pointer.
//
// Precedence is ACTIVE_PROFILE over WEEK_PROGRAM_POINTER across the whole
// device (a device that declares both is an HmIP model whose integer index
// is authoritative), then, per channel, VALUES over MASTER. The device-root
// pseudo-channel is searched last so a real channel always wins.
//
// Mirrors the custom-DP resolver `resolveWeekProgramPointer`
// (internal/model/custom/climate/climate.go), which already walks both
// paramsets and the device root; the schedule surfaces shared none of it.
func findProfilePointer(d *device.Device) (profilePointer, bool) {
	if d == nil {
		return profilePointer{}, false
	}
	channels := append([]*device.Channel(nil), d.Channels()...)
	if root := d.RootChannel(); root != nil {
		channels = append(channels, root)
	}
	for _, p := range []hmenum.Parameter{
		hmenum.ParameterActiveProfile,
		hmenum.ParameterWeekProgramPointer,
	} {
		for _, ch := range channels {
			if ch == nil {
				continue
			}
			if dp := ch.Parameter(p); dp != nil {
				return profilePointer{
					dataPoint:      dp,
					parameter:      p,
					paramsetKey:    hmenum.ParamsetKeyValues,
					channelAddress: ch.Address,
				}, true
			}
			if dp := ch.MasterParameter(p); dp != nil {
				return profilePointer{
					dataPoint:      dp,
					parameter:      p,
					paramsetKey:    hmenum.ParamsetKeyMaster,
					channelAddress: ch.Address,
				}, true
			}
		}
	}
	return profilePointer{}, false
}

// profileCount derives how many week programs the device advertises from
// the pointer's wire descriptor. Returns 0 when the descriptor carries no
// usable maximum, which callers treat as "unknown", never as "zero".
//
//   - ACTIVE_PROFILE is 1-based, so count == MAX.
//   - WEEK_PROGRAM_POINTER is 0-based, so count == MAX+1.
func (p profilePointer) profileCount() int {
	if p.dataPoint == nil {
		return 0
	}
	pd := p.dataPoint.ParameterData()
	n, ok := rawJSONInt(pd.Max)
	if !ok {
		return 0
	}
	switch p.parameter { //nolint:exhaustive // only the two pointer parameters reach here
	case hmenum.ParameterActiveProfile:
		if n >= 1 {
			return n
		}
	case hmenum.ParameterWeekProgramPointer:
		if n >= 0 {
			return n + 1
		}
	}
	return 0
}

// wireValue renders the 0-based profile index `idx` in the shape the
// pointer's own descriptor declares.
//
// ACTIVE_PROFILE is a 1-based INTEGER, so it takes idx+1. For
// WEEK_PROGRAM_POINTER the descriptor decides: an option type
// (TYPE=ENUM) is written by its case-sensitive label "WEEK PROGRAM N"
// — the shape the custom-DP climate path already sends and the shape the
// reference sends — while the INTEGER form a few HmIPW models declare
// takes the raw 0-based ordinal. The type is read from the descriptor
// rather than assumed, so neither family gets the other's shape.
func (p profilePointer) wireValue(idx int) any {
	if p.parameter == hmenum.ParameterActiveProfile {
		return idx + 1
	}
	if p.dataPoint != nil && p.dataPoint.ParameterData().Type == hmenum.ParameterTypeEnum {
		return fmt.Sprintf("WEEK PROGRAM %d", idx+1)
	}
	return idx
}
