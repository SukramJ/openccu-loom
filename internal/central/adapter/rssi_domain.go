// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"

	"github.com/SukramJ/openccu-loom/internal/central"
)

// RSSIInfoDomain is the live implementation behind both the
// `ccu.get_rssi_info` WS command (ws.RSSIProvider) and the
// `GET /diagnostics/rssi` REST endpoint. It reports per-device RF reception
// strength.
type RSSIInfoDomain struct {
	registry *central.Registry
}

// NewRSSIInfoDomain wires the live adapter from the central registry. The
// data is read from the in-memory device model (the maintenance-channel
// RSSI_DEVICE / RSSI_PEER data points), so it needs no CCU round-trip.
func NewRSSIInfoDomain(r *central.Registry) *RSSIInfoDomain {
	return &RSSIInfoDomain{registry: r}
}

// RSSIInfo returns { "devices": [...] } — per-device reception strength
// (`rssi_device` = RSSI_DEVICE, `rssi_peer` = RSSI_PEER, both dBm), battery
// state (`battery_level` 0-100, `low_battery`), and reachability, for every
// device that reports an RSSI reading, across all centrals. It reads the
// device model's maintenance channel rather than the BidCos-RF-only XML-RPC
// `rssiInfo` method, so it works for HmIP and BidCos alike.
func (d *RSSIInfoDomain) RSSIInfo(_ context.Context) (map[string]any, error) {
	devices := make([]map[string]any, 0)
	if d.registry == nil {
		return map[string]any{"devices": devices}, nil
	}
	for _, u := range d.registry.List() {
		if u == nil || u.ModelRegistry == nil {
			continue
		}
		for _, dev := range u.ModelRegistry.List() {
			info := dev.AvailabilityInfo()
			if info.SignalStrength == nil && info.RSSIPeer == nil {
				continue // no RSSI reading (e.g. wired / never seen) — skip
			}
			devices = append(devices, map[string]any{
				"address":       dev.Address,
				"name":          dev.Name,
				"interface_id":  dev.InterfaceID,
				"central":       u.Name(),
				"rssi_device":   intOrNil(info.SignalStrength),
				"rssi_peer":     intOrNil(info.RSSIPeer),
				"battery_level": intOrNil(info.BatteryLevel),
				"low_battery":   boolOrNil(info.LowBattery),
				"reachable":     info.IsReachable,
			})
		}
	}
	return map[string]any{"devices": devices}, nil
}

// intOrNil unwraps an optional int into a JSON value: the int when present,
// nil (→ JSON null) otherwise.
func intOrNil(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

// boolOrNil unwraps an optional bool into a JSON value: the bool when present,
// nil (→ JSON null) otherwise.
func boolOrNil(p *bool) any {
	if p == nil {
		return nil
	}
	return *p
}
