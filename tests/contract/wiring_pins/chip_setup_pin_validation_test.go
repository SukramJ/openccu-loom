// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package wiring_pins

import (
	"testing"

	"github.com/SukramJ/openccu-loom/tests/contract"
)

// TestPin_SetupPIN_TrivialCodeBlacklist pins that the daemon checks the
// configured passcode with node.ValidateSetupPIN before the node derives the
// SPAKE2+ verifier from it (matterCommissioningCredentials). Without this
// guard trivially-guessable passcodes (e.g. 12345678) could be accepted,
// violating chip src/crypto/CHIPCryptoPAL.cpp IsValidSetupPIN policy.
func TestPin_SetupPIN_TrivialCodeBlacklist(t *testing.T) {
	contract.MustFindCallerInFile(
		t,
		"cmd/openccu-loom",
		"github.com/SukramJ/go-fabric/node", "ValidateSetupPIN",
	)
}
