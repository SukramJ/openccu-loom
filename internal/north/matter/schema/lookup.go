// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

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
// loom:reachable:reason="used in Matter debug logging, parity tests, and cluster-server introspection paths"
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
// loom:reachable:reason="used in Matter debug logging and device-type descriptor introspection"
func DeviceTypeName(id uint32) (string, bool) {
	name, ok := DeviceTypeNames[id]
	return name, ok
}
