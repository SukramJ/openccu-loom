// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package wiring_pins

import (
	"testing"

	"github.com/SukramJ/openccu-loom/tests/contract"
)

// TestPin_ValidateRCAC_SubjectVsIssuer pins that handleAddTrustedRootCertificate
// calls ValidateRCAC before storing the trust root. Root CA certificates must
// be self-signed per Matter §6.5 and chip src/credentials/CHIPCert.cpp
// ValidateChipRCAC; removing this call allows malformed trust roots to be
// installed without structural validation.
func TestPin_ValidateRCAC_SubjectVsIssuer(t *testing.T) {
	contract.MustFindCallerInFile(
		t,
		"internal/north/matter/cluster/core/operational_credentials.go",
		"internal/north/matter/secure/mattercert", "ValidateRCAC",
	)
}
