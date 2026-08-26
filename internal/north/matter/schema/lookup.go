// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package schema

// ClusterRevision returns the matter.js HEAD revision for the given cluster
// id, or (0, false) if the cluster is not present in the snapshot.
//
// Mirrors matter.js packages/model/src/standard/elements/<name>.element.ts
// ClusterRevision attribute defaults.
func ClusterRevision(id uint32) (uint16, bool) {
	rev, ok := ClusterRevisions[id]
	return rev, ok
}

// ClusterName returns the canonical matter.js cluster name for the given id,
// or ("", false) if the cluster is not present in the snapshot.
//
// loom:reachable:reason="introspection companion to the generated cluster tables; retained for diagnostics — no production caller yet"
func ClusterName(id uint32) (string, bool) {
	name, ok := ClusterNames[id]
	return name, ok
}

// DeviceTypeRevision returns the matter.js HEAD revision for the given
// device-type id, or (0, false) if the device type is not present in the
// snapshot.
//
// This is the codegen'd counterpart of endpoint.deviceTypeRevision: callers
// that need to advertise a device-type revision in Descriptor.DeviceTypeList
// should prefer this function so updates to the matter.js snapshot propagate
// automatically on the next `make generate-matter-schema` run.
//
// Mirrors matter.js packages/node/src/devices/<name>.ts revision fields.
func DeviceTypeRevision(id uint32) (uint16, bool) {
	rev, ok := DeviceTypeRevisions[id]
	return rev, ok
}

// DeviceTypeName returns the canonical matter.js device-type name for the
// given id, or ("", false) if the device type is not present in the snapshot.
//
// loom:reachable:reason="introspection companion to the generated device-type tables; retained for diagnostics — no production caller yet"
func DeviceTypeName(id uint32) (string, bool) {
	name, ok := DeviceTypeNames[id]
	return name, ok
}

// DeviceTypeAllowsServerCluster reports whether the Matter Device Library
// permits clusterID to be mounted as a SERVER cluster on an endpoint whose
// primary device type is deviceType, and whether deviceType is known at all.
//
// A cluster the device type specifies only as a CLIENT requirement is NOT
// permitted: a client requirement means the type consumes that cluster from
// another endpoint. Thermostat (0x0301) is the canonical case — it names
// TemperatureMeasurement and RelativeHumidityMeasurement as clientCluster, so
// a thermostat that serves either is non-conformant even though the cluster
// appears in its requirement list.
//
// Callers must distinguish the two return values: an unknown device type
// (known=false) means the snapshot cannot answer the question, which is not
// the same as a definite "no".
//
// loom:reachable:reason="conformance oracle for tests/contract/matter_devicetype_conformance_test.go; the bridge itself mounts clusters from the model layer"
func DeviceTypeAllowsServerCluster(deviceType, clusterID uint32) (allowed, known bool) {
	ids, ok := DeviceTypeServerClusters[deviceType]
	if !ok {
		return false, false
	}
	for _, id := range ids {
		if id == clusterID {
			return true, true
		}
	}
	return false, true
}
