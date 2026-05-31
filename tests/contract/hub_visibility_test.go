// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/datapoint"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// V12 — pin Hub-DP visibility invariants (PR-33).
//
// hub.HubDataPoint embeds [datapoint.BaseDataPointFields] and shadows
// [datapoint.BaseDataPointFields.EnabledByDefault] so the
// EnabledDefault field wins when no forced usage is set, but a forced
// usage delegates to the foundation logic.
//
// The audit (parity_v7) flagged that no contract test pinned this
// chain — specifically that a `ForcedUsage=CDPSecondary` on a hub DP
// must hide the DP from north-bound surfaces (CDPSecondary marks an
// internal helper owned by a parent; a sysvar / program never has a
// parent, so this is a defensive guarantee for a future state we
// haven't reached yet). The contract test below makes the chain
// non-regressible.

// TestContractHubDataPointVisibilityMatrix pins every (forced-usage,
// EnabledDefault) combination on hub data points, mirroring the
// foundation matrix in `internal/model/datapoint/base_test.go`. Hub
// DPs surface system variables and CCU programs to north-bound
// adapters; a regression in the visibility chain would silently mute
// or duplicate those entities.
func TestContractHubDataPointVisibilityMatrix(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name            string
		enabledDefault  bool
		forced          *hmenum.DataPointUsage
		wantVisible     bool
		wantEnabledDflt bool
	}{
		{
			name:            "no_forced_enabledDefault_true",
			enabledDefault:  true,
			forced:          nil,
			wantVisible:     true,
			wantEnabledDflt: true,
		},
		{
			name:            "no_forced_enabledDefault_false",
			enabledDefault:  false,
			forced:          nil,
			wantVisible:     true,  // foundation default stays true
			wantEnabledDflt: false, // hub override returns the field value
		},
		{
			name:            "forced_NoCreate_overrides_field",
			enabledDefault:  true,
			forced:          ptrUsage(hmenum.DataPointUsageNoCreate),
			wantVisible:     false,
			wantEnabledDflt: false,
		},
		{
			name:            "forced_CDPSecondary_overrides_field",
			enabledDefault:  true,
			forced:          ptrUsage(hmenum.DataPointUsageCDPSecondary),
			wantVisible:     false,
			wantEnabledDflt: false,
		},
		{
			name:            "forced_DataPoint_keeps_visible",
			enabledDefault:  false,
			forced:          ptrUsage(hmenum.DataPointUsageDataPoint),
			wantVisible:     true,
			wantEnabledDflt: true, // forced takes precedence over field
		},
		{
			name:            "forced_CDPVisible_keeps_visible",
			enabledDefault:  false,
			forced:          ptrUsage(hmenum.DataPointUsageCDPVisible),
			wantVisible:     true,
			wantEnabledDflt: true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dp := hub.NewHubDataPoint("ccu", "Var", "", tc.enabledDefault)
			if tc.forced != nil {
				dp.SetForcedUsage(*tc.forced)
			}

			// Verify the BaseDataPoint contract surface matches.
			var iface datapoint.BaseDataPoint = &dp
			if got := iface.Visible(); got != tc.wantVisible {
				t.Fatalf("Visible() = %v, want %v", got, tc.wantVisible)
			}
			if got := iface.EnabledByDefault(); got != tc.wantEnabledDflt {
				t.Fatalf("EnabledByDefault() = %v, want %v", got, tc.wantEnabledDflt)
			}
		})
	}
}

// TestContractHubDataPointUniqueIDIsCentralScoped pins ADR 0002:
// every hub DP must produce a UniqueID with a non-empty central
// segment. Two CCUs with the same sysvar / program name must produce
// different identifiers; a regression here would make MQTT discovery
// collide entries from independent CCUs.
func TestContractHubDataPointUniqueIDIsCentralScoped(t *testing.T) {
	t.Parallel()

	left := hub.NewHubDataPoint("ccu-prod", "Anwesenheit", "", true)
	right := hub.NewHubDataPoint("ccu-secondary", "Anwesenheit", "", true)

	if left.UniqueID() == right.UniqueID() {
		t.Fatalf("hub UniqueID collision across centrals: %q", left.UniqueID())
	}
	if left.UniqueID() == "" || right.UniqueID() == "" {
		t.Fatalf("hub UniqueID must not be empty (left=%q right=%q)", left.UniqueID(), right.UniqueID())
	}
}

func ptrUsage(u hmenum.DataPointUsage) *hmenum.DataPointUsage { return &u }
