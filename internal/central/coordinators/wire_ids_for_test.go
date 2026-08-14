// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package coordinators

import (
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// testCentralName is the central every coordinator in this package's tests is
// built for; the registry keys below are derived from it.
const testCentralName = "ccu1"

// wireKey returns the canonical `<central>-<iface>` id the hydration pipeline
// keys the description, paramset and device registries by. The tests register
// under it (and pass it to the coordinator) because that is what production
// does — registering under the bare interface made whole coordinator paths
// no-ops that still looked green.
func wireKey(iface hmenum.Interface) hmtypes.WireInterfaceID {
	return hmtypes.NewWireInterfaceID(testCentralName, iface)
}
