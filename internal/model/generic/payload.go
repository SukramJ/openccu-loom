// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package generic

import (
	"encoding/json"

	"github.com/SukramJ/openccu-loom/internal/model/datapoint"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/internal/routingkey"
)

// Compile-time guarantees that the concrete generic data-point types
// satisfy the universal Source contract through method promotion from
// the embedded *DataPoint[T]. ADR 0007 step 3.
var (
	_ payload.Source = (*Switch)(nil)
	_ payload.Source = (*BinarySensor)(nil)
	_ payload.Source = (*Float)(nil)
	_ payload.Source = (*Integer)(nil)
	_ payload.Source = (*Select)(nil)
	_ payload.Source = (*Button)(nil)
	_ payload.Source = (*Text)(nil)
)

// CanonicalUniqueID builds the external, loom-namespaced unique_id for
// this data point. The serialSuffix (the CCU serial's last-10 lower
// suffix) is supplied by the north boundary, which holds the central →
// serial mapping; the data point contributes its channel address and
// wire parameter. Normal devices come out unprefixed
// (loom_vcu1234567_1_state); internal / virtual-remote addresses carry
// the serial suffix. See docs/external-clients/ha-unique-id-migration.md.
//
// A data point forced to a read-only sensor (the HmIP-eTRV / HmIP-HEATING
// LEVEL surface, see [IsForceSensorParameter]) carries the same "_sensor"
// disambiguation suffix the internal [datapoint.BaseDataPointFields.UniqueID]
// applies, mirroring the reference's
// `f"{unique_id}_{DataPointCategory.SENSOR}"` (model/data_point.py:1023).
// Without it the external key collides with the writable surface the same
// wire parameter would otherwise produce, and a consumer that keys its
// entity registry on this string — the Home Assistant drop-in above all —
// orphans the entity it migrated under the suffixed key and spawns a
// duplicate beside it.
func (d *DataPoint[T]) CanonicalUniqueID(serialSuffix string) string {
	if d == nil {
		return ""
	}
	parameter := string(d.Parameter())
	if d.IsForcedSensor() {
		parameter += datapoint.ForcedSensorSuffix
	}
	return routingkey.CanonicalUniqueID(serialSuffix, d.Address(), parameter, "")
}

// Info returns identity-level fields that uniquely describe
// the data point and its wire-level provenance.
//
//   - unique_id    — central-scoped, broker-stable identifier
//   - key          — full DataPointKey string (debugging / logging)
//   - parameter    — wire-level parameter name (SET_POINT_TEMPERATURE)
//   - paramset_key — VALUES / MASTER / LINK marker
//   - address      — owning channel address
//   - central      — owning Unit name (omitted in test fixtures)
//   - device_model — parent device's CCU model
//   - category     — DataPointCategory bucket (sensor / switch / number)
//   - kind         — resolved generic shape (Switch, Sensor, Number, …)
//   - has_events   — descriptor advertises EVENT
//   - is_readable / is_writable — effective read/write surface
//   - operations   — raw operations bitmask
//   - flags        — raw flags bitmask
func (d *DataPoint[T]) Info() payload.InfoPayload {
	if d == nil {
		return nil
	}
	return &payload.GenericDataPointInfo{
		UniqueID:    d.UniqueID(),
		Key:         d.Key.String(),
		Parameter:   string(d.Parameter()),
		ParamsetKey: string(d.Key.ParamsetKey),
		Address:     d.Key.ChannelAddress,
		Category:    string(d.Category()),
		Kind:        string(d.Kind),
		HasEvents:   d.HasEvents(),
		IsReadable:  d.IsReadable(),
		IsWritable:  d.IsWritable(),
		Operations:  int(d.Descriptor.Operations),
		Flags:       int(d.Descriptor.Flags),
		Central:     d.CentralName,
		DeviceModel: d.DeviceModel,
	}
}

// Config returns the configuration-level fields that drive
// north-bound entity rendering: usage classification, descriptor
// bounds, value lists, and the unit/multiplier pair the MQTT and HA
// adapters need to scale displayed values.
//
//   - usage           — DataPointUsage classification
//   - enabled_default — true when usage maps to a primary surface
//   - unit            — descriptor unit string (omitted when empty)
//   - default         — descriptor default value (omitted when nil)
//   - min / max       — numeric descriptor bounds (omitted when missing)
//   - value_list      — enum option list (omitted when empty)
func (d *DataPoint[T]) Config() payload.ConfigPayload {
	if d == nil {
		return nil
	}
	cfg := &payload.GenericDataPointConfig{
		Usage:          string(d.Usage()),
		EnabledDefault: d.EnabledByDefault(),
		Unit:           d.Descriptor.Unit,
	}
	if len(d.Descriptor.Default) > 0 {
		cfg.Default = string(d.Descriptor.Default)
	}
	if len(d.Descriptor.Min) > 0 {
		cfg.Min = string(d.Descriptor.Min)
	}
	if len(d.Descriptor.Max) > 0 {
		cfg.Max = string(d.Descriptor.Max)
	}
	// SPECIAL is embedded as raw JSON so the config topic carries the
	// sentinel set itself. A descriptor whose blob never parsed keeps its
	// bytes verbatim through normalization, and embedding those would make
	// the whole payload unmarshalable — dropping the one field is better
	// than losing the config topic.
	if len(d.Descriptor.Special) > 0 && json.Valid(d.Descriptor.Special) {
		cfg.Special = d.Descriptor.Special
	}
	if len(d.Descriptor.ValueList) > 0 {
		vl := make([]string, len(d.Descriptor.ValueList))
		copy(vl, d.Descriptor.ValueList)
		cfg.ValueList = vl
	}
	if m := d.Multiplier(); m != 0 && m != 1.0 {
		cfg.Multiplier = m
	}
	return cfg
}

// ReadingAvailable reports whether a reading may be published as available.
//
// A reading is available only when it has been refreshed AND passes the
// north-bound validity gate — its paired STATUS is acceptable, its value type
// matches, and it is within range. The value itself is still carried
// alongside, so a consumer that ignores availability can read it.
//
// Named here because every north plane answers the same question and a plane
// that restates it drifts on the day validity grows a condition: the value
// keeps flowing while one consumer greys the entity out and another does not.
func ReadingAvailable(observed, valid bool) bool {
	return observed && valid
}

// State returns the live state of the data point: current
// value, observation flag, last-modified / last-refreshed timestamps,
// and the paired parameter status when available. Callers that
// want a simple `{value, available, modified_at}` envelope can read
// just those fields.
//
//   - value        — typed value, present iff observed
//   - available    — true iff observed AND IsValid() (refreshed + status + type + range)
//   - modified_at  — RFC3339 nano of last observed update (omitted when zero)
//   - refreshed_at — RFC3339 nano of last refresh (omitted when zero)
//   - status       — ParameterStatus enum string when observed
func (d *DataPoint[T]) State() payload.StatePayload {
	if d == nil {
		return nil
	}
	v, observed := d.Value()
	st := &payload.GenericDataPointState{
		Available: ReadingAvailable(observed, d.IsValid()),
	}
	if observed {
		st.Value = v
	}
	if t := d.ModifiedAt(); !t.IsZero() {
		st.ModifiedAt = t.UTC().Format(timestampLayout)
	}
	if t := d.RefreshedAt(); !t.IsZero() {
		st.RefreshedAt = t.UTC().Format(timestampLayout)
	}
	if s, ok := d.Status(); ok {
		st.Status = string(s)
	}
	return st
}

// timestampLayout is the wire format the payload methods emit for time
// fields: RFC3339 in UTC with the fractional second padded to a fixed nine
// digits.
//
// It is deliberately not [time.RFC3339Nano], which trims trailing zeros and
// therefore varies its width with the value: an instant at .5 s renders as
// ".500000000Z" through this layout and ".5Z" through that one. Both
// spellings are live in the daemon — the MQTT bridge's state payload and the
// WebSocket value-changed payload format the same `modified_at` key with
// time.RFC3339Nano — so this constant is one plane's answer, not the
// daemon-wide one. The two re-parse to the same instant; only a consumer that
// string-compares or assumes a fixed width can tell them apart.
//
// TestW2GenTimestampLayoutIsFixedWidth pins the padding against a silent
// swap to time.RFC3339Nano.
const timestampLayout = "2006-01-02T15:04:05.000000000Z07:00"
