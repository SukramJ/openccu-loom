// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// The registries are keyed by the canonical `<central>-<interface>` wire id.
// Fixtures whose central announces its interface without a name prefix carry
// the bare token as the device's InterfaceID, so their registry key is the
// same string; the constructor spells that out instead of leaving a bare
// interface constant to be read as a key by accident. Fixtures that do build a
// prefixed id key their registries off that id directly.
var wireHmIPRF = hmtypes.NewWireInterfaceID("", hmenum.InterfaceHmIPRF)
