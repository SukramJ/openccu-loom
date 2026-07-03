// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package schema

// Read-only-attribute write gate (Matter §8.6). Every cluster attribute
// carries an access string in the matter.js model; its leading read/write
// token decides writability. matter.js derives this in
// ../matter.js/packages/model/src/aspects/Access.ts:44-46:
//
//	get writable() {
//	    return !!this.rw && this.rw !== Access.Rw.Read;
//	}
//
// so access "R V" / "R A" / "R M" (rw == "R") is read-only, while "RW …"
// (rw == "RW") and "R[W] …" (rw == "R[W]", optional write) are writable.
//
// A remote WriteRequest to a read-only attribute is rejected BEFORE any
// behavior runs:
//   - concrete path → UNSUPPORTED_WRITE
//     (../matter.js/packages/protocol/src/action/server/AttributeWriteResponse.ts:229-231
//     `if (!limits.writable) … return this.#asStatus(path, Status.UnsupportedWrite)`).
//   - wildcard path → the attribute is silently skipped
//     (AttributeWriteResponse.ts:329-331 `if (!attribute.limits.writable) return;`).
//
// The Go dispatcher has no behavior layer that carries `limits.writable`, so
// this table provides the same verdict schema-side. It is scoped to the
// clusters the bridge exposes (core clusters plus OnOff/LevelControl/
// ColorControl/Groups, WindowCovering, DoorLock, Thermostat, GenericSwitch,
// and the measurement clusters) so it stays bounded. Every entry is derived
// from the per-attribute `access` strings in
// docs/parity/matter/matter-schema-snapshot.json (the matter.js HEAD pin);
// TestReadOnlyAttributeParity reloads that snapshot and fails if any entry
// drifts from matter.js, so the table cannot silently diverge.
//
// Matter global attributes (FeatureMap, ClusterRevision, AttributeList,
// AcceptedCommandList, GeneratedCommandList, EventList) carry no explicit
// access string in the snapshot and are intentionally absent — the
// dispatcher already synthesises them read-only, and a controller write to
// one falls through to the cluster server unchanged.
var readOnlyAttributes = map[uint32]map[uint32]struct{}{
	// Identify (0x0003) — 1 read-only
	0x0003: newReadOnlySet(
		0x0001,
	),
	// Groups (0x0004) — 1 read-only
	0x0004: newReadOnlySet(
		0x0000,
	),
	// OnOff (0x0006) — 2 read-only
	0x0006: newReadOnlySet(
		0x0000, 0x4000,
	),
	// LevelControl (0x0008) — 7 read-only
	0x0008: newReadOnlySet(
		0x0000, 0x0001, 0x0002, 0x0003, 0x0004, 0x0005, 0x0006,
	),
	// Descriptor (0x001D) — 6 read-only
	0x001D: newReadOnlySet(
		0x0000, 0x0001, 0x0002, 0x0003, 0x0004, 0x0005,
	),
	// AccessControl (0x001F) — 5 read-only
	0x001F: newReadOnlySet(
		0x0002, 0x0003, 0x0004, 0x0005, 0x0006,
	),
	// BasicInformation (0x0028) — 21 read-only
	0x0028: newReadOnlySet(
		0x0000, 0x0001, 0x0002, 0x0003, 0x0004, 0x0007, 0x0008, 0x0009,
		0x000A, 0x000B, 0x000C, 0x000D, 0x000E, 0x000F, 0x0011, 0x0012,
		0x0013, 0x0014, 0x0015, 0x0016, 0x0018,
	),
	// OtaSoftwareUpdateRequestor (0x002A) — 3 read-only
	0x002A: newReadOnlySet(
		0x0001, 0x0002, 0x0003,
	),
	// PowerSource (0x002F) — 32 read-only
	0x002F: newReadOnlySet(
		0x0000, 0x0001, 0x0002, 0x0003, 0x0004, 0x0005, 0x0006, 0x0007,
		0x0008, 0x0009, 0x000A, 0x000B, 0x000C, 0x000D, 0x000E, 0x000F,
		0x0010, 0x0011, 0x0012, 0x0013, 0x0014, 0x0015, 0x0016, 0x0017,
		0x0018, 0x0019, 0x001A, 0x001B, 0x001C, 0x001D, 0x001E, 0x001F,
	),
	// GeneralCommissioning (0x0030) — 12 read-only
	0x0030: newReadOnlySet(
		0x0001, 0x0002, 0x0003, 0x0004, 0x0005, 0x0006, 0x0007, 0x0008,
		0x0009, 0x000A, 0x000B, 0x000C,
	),
	// NetworkCommissioning (0x0031) — 10 read-only
	0x0031: newReadOnlySet(
		0x0000, 0x0001, 0x0002, 0x0003, 0x0005, 0x0006, 0x0007, 0x0008,
		0x0009, 0x000A,
	),
	// GeneralDiagnostics (0x0033) — 9 read-only
	0x0033: newReadOnlySet(
		0x0000, 0x0001, 0x0002, 0x0003, 0x0004, 0x0005, 0x0006, 0x0007,
		0x0008,
	),
	// TimeSynchronization (0x0038) — 13 read-only
	0x0038: newReadOnlySet(
		0x0000, 0x0001, 0x0002, 0x0003, 0x0004, 0x0005, 0x0006, 0x0007,
		0x0008, 0x0009, 0x000A, 0x000B, 0x000C,
	),
	// GenericSwitch (0x003B) — 3 read-only
	0x003B: newReadOnlySet(
		0x0000, 0x0001, 0x0002,
	),
	// AdministratorCommissioning (0x003C) — 3 read-only
	0x003C: newReadOnlySet(
		0x0000, 0x0001, 0x0002,
	),
	// OperationalCredentials (0x003E) — 6 read-only
	0x003E: newReadOnlySet(
		0x0000, 0x0001, 0x0002, 0x0003, 0x0004, 0x0005,
	),
	// GroupKeyManagement (0x003F) — 3 read-only
	0x003F: newReadOnlySet(
		0x0001, 0x0002, 0x0003,
	),
	// BooleanState (0x0045) — 1 read-only
	0x0045: newReadOnlySet(
		0x0000,
	),
	// IcdManagement (0x0046) — 10 read-only
	0x0046: newReadOnlySet(
		0x0000, 0x0001, 0x0002, 0x0003, 0x0004, 0x0005, 0x0006, 0x0007,
		0x0008, 0x0009,
	),
	// SmokeCoAlarm (0x005C) — 12 read-only
	0x005C: newReadOnlySet(
		0x0000, 0x0001, 0x0002, 0x0003, 0x0004, 0x0005, 0x0006, 0x0007,
		0x0008, 0x0009, 0x000A, 0x000C,
	),
	// ScenesManagement (0x0062) — 3 read-only
	0x0062: newReadOnlySet(
		0x0000, 0x0001, 0x0002,
	),
	// ElectricalPowerMeasurement (0x0090) — 19 read-only
	0x0090: newReadOnlySet(
		0x0000, 0x0001, 0x0002, 0x0003, 0x0004, 0x0005, 0x0006, 0x0007,
		0x0008, 0x0009, 0x000A, 0x000B, 0x000C, 0x000D, 0x000E, 0x000F,
		0x0010, 0x0011, 0x0012,
	),
	// ElectricalEnergyMeasurement (0x0091) — 6 read-only
	0x0091: newReadOnlySet(
		0x0000, 0x0001, 0x0002, 0x0003, 0x0004, 0x0005,
	),
	// DoorLock (0x0101) — 28 read-only
	0x0101: newReadOnlySet(
		0x0000, 0x0001, 0x0002, 0x0003, 0x0011, 0x0012, 0x0013, 0x0014,
		0x0015, 0x0016, 0x0017, 0x0018, 0x0019, 0x001A, 0x001B, 0x001C,
		0x0026, 0x0027, 0x0034, 0x0080, 0x0081, 0x0082, 0x0083, 0x0084,
		0x0085, 0x0086, 0x0087, 0x0088,
	),
	// WindowCovering (0x0102) — 13 read-only
	0x0102: newReadOnlySet(
		0x0000, 0x0005, 0x0006, 0x0007, 0x0008, 0x0009, 0x000A, 0x000B,
		0x000C, 0x000D, 0x000E, 0x000F, 0x001A,
	),
	// Thermostat (0x0201) — 22 read-only
	0x0201: newReadOnlySet(
		0x0000, 0x0001, 0x0002, 0x0003, 0x0004, 0x0005, 0x0006, 0x001E,
		0x0029, 0x0030, 0x0031, 0x0032, 0x0046, 0x0048, 0x0049, 0x004A,
		0x004B, 0x004C, 0x004D, 0x004E, 0x004F, 0x0052,
	),
	// ColorControl (0x0300) — 50 read-only
	0x0300: newReadOnlySet(
		0x0000, 0x0001, 0x0002, 0x0003, 0x0004, 0x0005, 0x0006, 0x0007,
		0x0008, 0x0010, 0x0011, 0x0012, 0x0013, 0x0015, 0x0016, 0x0017,
		0x0019, 0x001A, 0x001B, 0x0020, 0x0021, 0x0022, 0x0024, 0x0025,
		0x0026, 0x0028, 0x0029, 0x002A, 0x0030, 0x0031, 0x0032, 0x0033,
		0x0034, 0x0036, 0x0037, 0x0038, 0x003A, 0x003B, 0x003C, 0x4000,
		0x4001, 0x4002, 0x4003, 0x4004, 0x4005, 0x4006, 0x400A, 0x400B,
		0x400C, 0x400D,
	),
	// IlluminanceMeasurement (0x0400) — 5 read-only
	0x0400: newReadOnlySet(
		0x0000, 0x0001, 0x0002, 0x0003, 0x0004,
	),
	// TemperatureMeasurement (0x0402) — 4 read-only
	0x0402: newReadOnlySet(
		0x0000, 0x0001, 0x0002, 0x0003,
	),
	// PressureMeasurement (0x0403) — 9 read-only
	0x0403: newReadOnlySet(
		0x0000, 0x0001, 0x0002, 0x0003, 0x0010, 0x0011, 0x0012, 0x0013,
		0x0014,
	),
	// RelativeHumidityMeasurement (0x0405) — 4 read-only
	0x0405: newReadOnlySet(
		0x0000, 0x0001, 0x0002, 0x0003,
	),
	// OccupancySensing (0x0406) — 4 read-only
	0x0406: newReadOnlySet(
		0x0000, 0x0001, 0x0002, 0x0004,
	),
	// CarbonDioxideConcentrationMeasurement (0x040D) — 11 read-only
	0x040D: newReadOnlySet(
		0x0000, 0x0001, 0x0002, 0x0003, 0x0004, 0x0005, 0x0006, 0x0007,
		0x0008, 0x0009, 0x000A,
	),
	// Pm25ConcentrationMeasurement (0x042A) — 11 read-only
	0x042A: newReadOnlySet(
		0x0000, 0x0001, 0x0002, 0x0003, 0x0004, 0x0005, 0x0006, 0x0007,
		0x0008, 0x0009, 0x000A,
	),
	// Pm10ConcentrationMeasurement (0x042D) — 11 read-only
	0x042D: newReadOnlySet(
		0x0000, 0x0001, 0x0002, 0x0003, 0x0004, 0x0005, 0x0006, 0x0007,
		0x0008, 0x0009, 0x000A,
	),
}

// newReadOnlySet builds a lookup set from the supplied attribute IDs.
func newReadOnlySet(ids ...uint32) map[uint32]struct{} {
	m := make(map[uint32]struct{}, len(ids))
	for _, id := range ids {
		m[id] = struct{}{}
	}
	return m
}

// AttributeWritable reports the matter.js writability verdict for a cluster
// attribute. It returns (writable=false, known=true) for an attribute the
// bridge-exposed schema marks read-only (access "R …"), and
// (writable=true, known=false) for every other (clusterID, attrID) pair —
// meaning "no read-only record; treat as writable and leave the outcome to
// the cluster server". Because the table records only read-only attributes,
// the writable return is authoritative only when known is true (then it is
// always false); callers gate on known && !writable.
//
// Mirrors matter.js `limits.writable`
// (../matter.js/packages/model/src/aspects/Access.ts:44-46) as consumed by
// AttributeWriteResponse.ts:229 (concrete write) and :329 (wildcard write).
func AttributeWritable(clusterID, attrID uint32) (writable, known bool) {
	attrs, ok := readOnlyAttributes[clusterID]
	if !ok {
		return true, false
	}
	if _, readOnly := attrs[attrID]; readOnly {
		return false, true
	}
	return true, false
}
