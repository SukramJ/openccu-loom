// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package wiring_pins

import (
	"testing"

	"github.com/SukramJ/openccu-loom/tests/contract"
)

// TestPin_SetupPIN_TrivialCodeBlacklist pins that buildPaseAdapterFromCreds
// calls IsValidSetupPIN before deriving the SPAKE2+ verifier. Without this
// guard trivially-guessable passcodes (e.g. 12345678) could be accepted,
// violating chip src/crypto/CHIPCryptoPAL.cpp IsValidSetupPIN policy.
func TestPin_SetupPIN_TrivialCodeBlacklist(t *testing.T) {
	contract.MustFindCallerInFile(
		t,
		"cmd/openccu-loom/daemon.go",
		"internal/north/matter/secure/setup", "IsValidSetupPIN",
	)
}
