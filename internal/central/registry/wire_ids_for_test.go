// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package registry

import (
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// The registries are keyed by the canonical `<central>-<interface>` wire id,
// which is the only form the CCU callback path can produce. The tests build
// their keys through the same constructor production uses, under a named
// central, so a lookup that assumed the bare interface name would miss here
// exactly as it misses in a running daemon.
const testCentralName = "ccu-registry"

var (
	wireHmIPRF   = hmtypes.NewWireInterfaceID(testCentralName, hmenum.InterfaceHmIPRF)
	wireBidCosRF = hmtypes.NewWireInterfaceID(testCentralName, hmenum.InterfaceBidCosRF)
)
