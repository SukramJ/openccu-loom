// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import "github.com/SukramJ/openccu-loom/pkg/hmenum"

// WireInterfaceID returns the wire-level interface identifier the daemon
// advertises to the CCU and that the CCU echoes back in every callback
// envelope. Format: `<instance_name>-<central_name>-<interface>` (ADR-0024).
//
// The triple satisfies two opposite uniqueness requirements at once:
//
//   - CCU-side: the CCU keys its callback registry by interface_id; a
//     duplicate value during init() overwrites the prior registration.
//     The `instance_name` (daemon identity) makes the id unique across
//     daemons, so two daemons against the same CCU coexist. A bare
//     `<central_name>-<interface>` would collide here — both daemons derive
//     the same central_name from the same CCU.
//   - Daemon-internal: [hmtypes.DataPointKey] carries no separate
//     central field, so per-CCU scoping rides entirely on the
//     InterfaceID. The `central_name` keeps the id distinct per CCU, so
//     addresses that repeat across CCUs (BidCoS-RF:1, INT000*, VCU
//     virtual remotes) do not collide internally.
//
// Internal routing (ModelRegistry, ValueWriter, EventBridge, etc.) uses the
// same wire-form so the InterfaceID stamped on Devices / Channels /
// DataPointKeys matches what the CCU sends in callbacks — there is exactly
// one identifier flowing through the daemon, not two with a translation layer
// in between.
//
// An empty instanceName falls back to the legacy `<central_name>-<interface>`
// form (used by tests and pre-ADR-0024 setups); an empty centralName yields
// the bare interface.
func WireInterfaceID(instanceName, centralName string, iface hmenum.Interface) string {
	switch {
	case instanceName == "" && centralName == "":
		return string(iface)
	case instanceName == "":
		return centralName + "-" + string(iface)
	case centralName == "":
		return instanceName + "-" + string(iface)
	default:
		return instanceName + "-" + centralName + "-" + string(iface)
	}
}
