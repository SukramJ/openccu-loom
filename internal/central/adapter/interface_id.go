// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import "github.com/SukramJ/openccu-loom/pkg/hmenum"

// WireInterfaceID returns the wire-level interface identifier the daemon
// advertises to the CCU and that the CCU echoes back in every callback
// envelope. Format: `<central_name>-<interface>`.
//
// @property def interface_id(self) -> str: return
// f"{self.central_name}-{self.interface}"
//
// Why a composite ID:
//
// - The CCU keys its callback registry by the (interface_id, callback_url)
// pair. Two daemons running against the same CCU with the same bare `HmIP-RF`
// interface_id would overwrite each other's registrations during init() — the
// second one to call init wins, the first stops receiving events. - Prefixing
// every interface_id with the central name makes the identifier globally
// unique on the CCU side. Two daemons with different central names (e.g.
// `Primary` and `Backup`) coexist cleanly; the same central name in two
// daemons is a configuration bug that surfaces at init time as a `init()
// already called` CCU error.
//
// Internal routing (ModelRegistry, ValueWriter, EventBridge, etc.) uses the
// same wire-form so the InterfaceID stamped on Devices / Channels /
// DataPointKeys matches what the CCU sends in callbacks — there is exactly
// one identifier flowing through the daemon, not two with a translation layer
// in between.
func WireInterfaceID(centralName string, iface hmenum.Interface) string {
	if centralName == "" {
		return string(iface)
	}
	return centralName + "-" + string(iface)
}
