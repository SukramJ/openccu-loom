// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package occulited

// Test access to unexported helpers and fixtures.
var (
	SortedScopes  = sortedScopes
	IsInitRefusal = isInitRefusal

	AnswerBodyLimit = answerBodyLimit

	PairingAccessControl = PairingAccess{Devices: "operate", Names: "read", System: "read"}
	PairingAccessRead    = PairingAccess{Devices: "read", Names: "read", System: "read"}
)
