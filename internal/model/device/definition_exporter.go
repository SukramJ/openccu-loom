// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package device

import "github.com/SukramJ/openccu-loom/pkg/hmtypes"

// DefinitionExporter produces a diagnostic snapshot of the full device
// definition — channels, paramsets, generic DPs, calculated DPs, custom DPs —
// as a plain map suitable for JSON marshalling.
//
// The exported map is intentionally flat and human-readable. It mirrors the
// shape that device debug / diagnostics endpoints need without coupling to any
// specific transport layer.
type DefinitionExporter struct {
	device *Device
}

// NewDefinitionExporter constructs an exporter for d.
//
// loom:reachable:reason="instantiated by REST device-definition handler to produce JSON diagnostic snapshots"
func NewDefinitionExporter(d *Device) *DefinitionExporter {
	return &DefinitionExporter{device: d}
}

// Export returns a complete diagnostic snapshot of the device.
//
// Top-level keys:
//   - "address"       — device address
//   - "model"         — device model string
//   - "interface_id"  — CCU interface identifier
//   - "name"          — operator-assigned device name
//   - "has_custom_dp" — whether a custom-DP profile is active
//   - "channels"      — slice of per-channel maps (see exportChannel)
func (e *DefinitionExporter) Export() map[string]any {
	d := e.device
	channels := d.Channels()
	chExports := make([]map[string]any, 0, len(channels))
	for _, ch := range channels {
		chExports = append(chExports, e.exportChannel(ch))
	}
	if root := d.RootChannel(); root != nil {
		chExports = append(chExports, e.exportChannel(root))
	}
	return map[string]any{
		"address":       d.Address,
		"model":         d.Model,
		"interface_id":  d.InterfaceID,
		"name":          d.Name,
		"has_custom_dp": d.HasCustomDataPointDefinition,
		"channels":      chExports,
	}
}

// exportChannel serialises a single channel. Keys:
//   - "address"         — channel address
//   - "number"          — channel number
//   - "type"            — CCU CHANNEL_TYPE string
//   - "name"            — operator-assigned channel name
//   - "has_week_profile" — whether a week-profile is attached
//   - "values"          — []dpEntry for VALUES paramset
//   - "master"          — []dpEntry for MASTER paramset
//   - "calculated"      — []string data-point keys
//   - "custom"          — string data-point key, or nil
func (e *DefinitionExporter) exportChannel(ch *Channel) map[string]any {
	valDPs := ch.DataPoints()
	masterDPs := ch.MasterDataPoints()
	calcDPs := ch.CalculatedDataPoints()

	values := make([]map[string]any, 0, len(valDPs))
	for _, dp := range valDPs {
		values = append(values, dpEntry(dp))
	}

	master := make([]map[string]any, 0, len(masterDPs))
	for _, dp := range masterDPs {
		master = append(master, dpEntry(dp))
	}

	calcKeys := make([]string, 0, len(calcDPs))
	for _, dp := range calcDPs {
		calcKeys = append(calcKeys, formatKey(dp.DataPointKey()))
	}

	var customKey any
	if cdp := ch.CustomDataPoint(); cdp != nil {
		customKey = formatKey(cdp.DataPointKey())
	}

	return map[string]any{
		"address":          ch.Address,
		"number":           ch.Number,
		"type":             ch.Type,
		"name":             ch.Name,
		"has_week_profile": ch.HasWeekProfile(),
		"values":           values,
		"master":           master,
		"calculated":       calcKeys,
		"custom":           customKey,
	}
}

// dpEntry renders a single VALUES or MASTER data point as a map. Keys:
//   - "parameter"  — parameter name
//   - "type"       — wire type string
//   - "operations" — bitmask integer
//   - "unit"       — unit string (may be empty)
//   - "value"      — most recently observed raw value, or nil when unset
func dpEntry(dp ParameterDataPoint) map[string]any {
	desc := dp.ParameterData()
	raw, observed := dp.RawValue()
	var value any
	if observed {
		value = raw
	}
	return map[string]any{
		"parameter":  string(dp.Parameter()),
		"type":       string(desc.Type),
		"operations": int(desc.Operations),
		"unit":       desc.Unit,
		"value":      value,
	}
}

// formatKey renders a DataPointKey as "<channel_address>/<paramset>/<parameter>"
// for display in the export map.
func formatKey(k hmtypes.DataPointKey) string {
	return k.ChannelAddress + "/" + string(k.ParamsetKey) + "/" + k.Parameter
}
