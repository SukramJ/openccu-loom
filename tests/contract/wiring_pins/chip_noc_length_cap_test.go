// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package wiring_pins

import (
	"testing"

	"github.com/SukramJ/openccu-loom/tests/contract"
)

// TestPin_AddNOC_LengthCap pins that handleAddNOC rejects NOC and ICAC
// payloads exceeding the 400-byte limit mandated by chip
// src/credentials/CHIPCert.h kMaxCHIPCertLength. The literal "400" must
// appear in the operational-credentials source so a refactor that removes
// the cap is caught immediately.
func TestPin_AddNOC_LengthCap(t *testing.T) {
	contract.MustFindStringLiteralInFile(
		t,
		"internal/north/matter/cluster/core/operational_credentials.go",
		"NOC exceeds 400-byte limit",
	)
}
