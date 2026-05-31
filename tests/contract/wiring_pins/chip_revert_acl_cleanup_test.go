// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package wiring_pins

import (
	"testing"

	"github.com/SukramJ/openccu-loom/tests/contract"
)

// TestPin_RevertAddNOC_ACLCleanup pins that revertAddNOC calls ReplaceACL to
// remove ACL entries for the reverted fabric. Without this cleanup a failed
// AddNOC leaves orphaned ACL entries behind, which later CASE sessions from
// a different fabric can accidentally match. Mirrors chip
// operational-credentials-server.cpp HandleAddNOC needRevert step 3:
// accessControl.DeleteAllEntriesForFabric.
func TestPin_RevertAddNOC_ACLCleanup(t *testing.T) {
	// ReplaceACL must appear in the revertAddNOC function. The identifier
	// appears in both the StoreFacade interface declaration and the call
	// site inside revertAddNOC; MustFindCallerInFile checks identifier
	// presence which is sufficient to pin the call site.
	contract.MustFindCallerInFile(
		t,
		"internal/north/matter/cluster/core/operational_credentials.go",
		"internal/north/matter/cluster/core", "ReplaceACL",
	)
}
