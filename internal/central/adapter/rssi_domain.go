// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
)

// rssiNoInfo is the CCU's wire sentinel for "no reception data" in an RSSI
// slot; it is surfaced to clients as null.
const rssiNoInfo = 65536

// RSSIInfoDomain is the live implementation behind both the
// `ccu.get_rssi_info` WS command (ws.RSSIMatrixProvider) and the
// `GET /diagnostics/rssi` REST endpoint. It exposes the CCU's pairwise RF
// reception matrix (device ↔ communication-partner RSSI pairs) read from the
// XML-RPC `rssiInfo` method, across every central and RF interface.
type RSSIInfoDomain struct {
	registry *central.Registry
	writer   *client.ValueWriter
}

// NewRSSIInfoDomain wires the live adapter from the registry + value writer
// (the latter resolves the per-interface backend that answers rssiInfo).
func NewRSSIInfoDomain(r *central.Registry, w *client.ValueWriter) *RSSIInfoDomain {
	return &RSSIInfoDomain{registry: r, writer: w}
}

// RSSIInfo returns { "devices": [...] } across every central / RF interface.
// It collects the distinct interfaces from each central's devices, fetches
// each backend's reception matrix, resolves device/channel addresses to
// names, and normalises the CCU's 65536 "no data" sentinel to null. Backends
// that do not speak RF (e.g. CUxD over BIN-RPC) do not implement
// backends.RSSIInfoProvider, so the type assertion fails and the interface is
// skipped; a per-interface fetch error is skipped likewise rather than
// sinking the whole sweep.
func (d *RSSIInfoDomain) RSSIInfo(ctx context.Context) (map[string]any, error) {
	devices := make([]map[string]any, 0)
	if d.registry == nil || d.writer == nil {
		return map[string]any{"devices": devices}, nil
	}
	for _, u := range d.registry.List() {
		if u == nil || u.ModelRegistry == nil {
			continue
		}
		// Build an address→name lookup (devices + channels) and the set of
		// distinct interface IDs to query, in one pass over the model.
		nameByAddr := map[string]string{}
		ifaceIDs := map[string]struct{}{}
		for _, dev := range u.ModelRegistry.List() {
			nameByAddr[dev.Address] = dev.Name
			for _, ch := range dev.Channels() {
				nameByAddr[ch.Address] = ch.Name
			}
			if dev.InterfaceID != "" {
				ifaceIDs[dev.InterfaceID] = struct{}{}
			}
		}
		for ifaceID := range ifaceIDs {
			backend, ok := d.writer.Backend(u.Name(), ifaceID)
			if !ok {
				continue
			}
			provider, ok := backend.(backends.RSSIInfoProvider)
			if !ok {
				continue // non-RF backend (e.g. CUxD) — no rssiInfo
			}
			matrix, err := provider.RSSIInfo(ctx)
			if err != nil {
				// A failed / unsupported interface must not sink the whole
				// command; other interfaces may still answer.
				continue
			}
			devices = append(devices, buildRSSIDeviceEntries(matrix, ifaceID, u.Name(), nameByAddr)...)
		}
	}
	return map[string]any{"devices": devices}, nil
}

// buildRSSIDeviceEntries shapes one interface's raw reception matrix into the
// wire entries: it resolves device and partner addresses to names via
// nameByAddr (empty string when unknown) and normalises the CCU's 65536
// "no data" sentinel to null. Pure — no registry / backend access — so the
// name-resolution and normalisation logic is unit-testable on its own.
func buildRSSIDeviceEntries(
	matrix map[string]map[string][2]int, ifaceID, centralName string, nameByAddr map[string]string,
) []map[string]any {
	devices := make([]map[string]any, 0, len(matrix))
	for devAddr, partners := range matrix {
		ps := make([]map[string]any, 0, len(partners))
		for partnerAddr, pair := range partners {
			ps = append(ps, map[string]any{
				"address":     partnerAddr,
				"name":        nameByAddr[partnerAddr],
				"rssi_device": rssiOrNull(pair[0]),
				"rssi_peer":   rssiOrNull(pair[1]),
			})
		}
		devices = append(devices, map[string]any{
			"address":      devAddr,
			"name":         nameByAddr[devAddr],
			"interface_id": ifaceID,
			"central":      centralName,
			"partners":     ps,
		})
	}
	return devices
}

// rssiOrNull maps the CCU's 65536 "no data" sentinel to nil and passes any
// real reading through unchanged.
func rssiOrNull(v int) any {
	if v == rssiNoInfo {
		return nil
	}
	return v
}
