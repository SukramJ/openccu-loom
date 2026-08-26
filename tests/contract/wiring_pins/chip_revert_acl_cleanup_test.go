// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

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
	// ReplaceACL is called more than once in this file — once to install the
	// AddNOC default ACL entry, once here to clear it on revert — so the
	// pin must be scoped to revertAddNOC's own body; a file-wide identifier
	// search would stay green even with this specific call deleted, because
	// the unrelated insertion call site still carries the same name.
	contract.MustFindMethodCallInFunc(
		t,
		"internal/north/matter/cluster/core/operational_credentials.go",
		"revertAddNOC", "store", "ReplaceACL",
	)
}
