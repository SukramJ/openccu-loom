// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package bridge

// White-box test for rewriteInvokeResponseCommand — ADR-0013 Decision #4.
// Lives in package bridge (not bridge_test) to access the unexported
// rewriteInvokeResponseCommand helper.

import (
	"testing"

	mattercore "github.com/SukramJ/openccu-loom/internal/north/matter/cluster/core"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
)

// ─── ADR-0013 Decision #4: ArmFailSafe response command-ID rewrite ──────────

// TestArmFailSafe_ResponseRoundtrip is an ADR-0013 "earlier-catch" test.
//
// Bug-pattern: HandleInvokeRequest echoed inv.Path verbatim into the
// InvokeResponseEntry, so Path.Command remained the request command ID
// (ArmFailSafe = 0x00) instead of the response command ID
// (ArmFailSafeResponse = 0x01). chip-tool's TypedCommandCallback resolved
// the response schema by looking up Path.Command in its response-type table
// and surfaced CHIP_ERROR_SCHEMA_MISMATCH (0x8E) even when the payload was
// structurally valid.
//
// The fix lives in rewriteInvokeResponseCommand (bridge/fields_reader.go),
// which is called for every InvokeResponseEntry before the response is
// serialised. This test drives that function directly with all three
// GeneralCommissioning response types and verifies that Path.Command is
// rewritten to the correct response command ID.
func TestArmFailSafe_ResponseRoundtrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		requestCmd uint32
		response   any
		wantCmdID  uint32
	}{
		{
			name:       "ArmFailSafe request=0x00 → response=0x01",
			requestCmd: 0x00,
			response:   mattercore.ArmFailSafeResponse{ErrorCode: mattercore.CommissioningErrorOK},
			wantCmdID:  0x01,
		},
		{
			name:       "SetRegulatoryConfig request=0x02 → response=0x03",
			requestCmd: 0x02,
			response:   mattercore.SetRegulatoryConfigResponse{ErrorCode: mattercore.CommissioningErrorOK},
			wantCmdID:  0x03,
		},
		{
			name:       "CommissioningComplete request=0x04 → response=0x05",
			requestCmd: 0x04,
			response:   mattercore.CommissioningCompleteResponse{ErrorCode: mattercore.CommissioningErrorOK},
			wantCmdID:  0x05,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Simulate what HandleInvokeRequest produces: Path.Command echoes
			// the request command ID verbatim (the unfixed behaviour).
			ent := im.InvokeResponseEntry{
				Path: im.ConcreteCommandPath{
					Cluster:     0x0030,
					HasCluster:  true,
					Command:     tc.requestCmd, // ← bug: request ID
					HasCommand:  true,
					Endpoint:    0,
					HasEndpoint: true,
				},
				Response:    tc.response,
				HasResponse: true,
				IsStatus:    false,
			}

			// Apply the rewrite — this is the ADR-0013 D#4 fix.
			rewriteInvokeResponseCommand(&ent)

			// ADR-0013 D#4 invariant: Path.Command must be the response
			// command ID, not the request command ID.
			if ent.Path.Command != tc.wantCmdID {
				t.Errorf("ADR-0013 D#4: Path.Command = 0x%02X, want 0x%02X (response ID, not request ID 0x%02X)",
					ent.Path.Command, tc.wantCmdID, tc.requestCmd)
			}
		})
	}
}

// TestRewriteInvokeResponseCommand_StatusEntryUnchanged verifies that
// rewriteInvokeResponseCommand is a no-op for IsStatus==true entries.
// Status entries already carry the correct command ID per spec; mutating
// them would break chip-tool's error-path parsing.
func TestRewriteInvokeResponseCommand_StatusEntryUnchanged(t *testing.T) {
	t.Parallel()

	const requestCmdID uint32 = 0x00

	ent := im.InvokeResponseEntry{
		Path: im.ConcreteCommandPath{
			Cluster:    0x0030,
			HasCluster: true,
			Command:    requestCmdID,
			HasCommand: true,
		},
		IsStatus: true,
		Status:   im.StatusIB{Status: im.StatusUnsupportedCommand},
	}

	rewriteInvokeResponseCommand(&ent)

	if ent.Path.Command != requestCmdID {
		t.Errorf("status entry: Path.Command = 0x%02X, want 0x%02X (must be unchanged for IsStatus entries)",
			ent.Path.Command, requestCmdID)
	}
}

// TestRewriteInvokeResponseCommand_NilResponseUnchanged verifies that
// rewriteInvokeResponseCommand is a no-op when Response is nil (no
// payload, status-only success path).
func TestRewriteInvokeResponseCommand_NilResponseUnchanged(t *testing.T) {
	t.Parallel()

	const requestCmdID uint32 = 0x00

	ent := im.InvokeResponseEntry{
		Path: im.ConcreteCommandPath{
			Cluster:    0x0030,
			HasCluster: true,
			Command:    requestCmdID,
			HasCommand: true,
		},
		Response:    nil,
		HasResponse: false,
		IsStatus:    false,
	}

	rewriteInvokeResponseCommand(&ent)

	if ent.Path.Command != requestCmdID {
		t.Errorf("nil-response entry: Path.Command = 0x%02X, want 0x%02X (must be unchanged when Response is nil)",
			ent.Path.Command, requestCmdID)
	}
}
