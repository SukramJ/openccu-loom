// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/store/visibility"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestVisibilityFlagOperationsTriple pins the FLAGS × OPERATIONS filter
// table against the Python reference (_should_skip_data_point,
// model/__init__.py:180-189).
//
// Python's two skip-conditions:
//
//  1. neither EVENT nor WRITE in OPERATIONS → skip
//  2. FLAGS & INTERNAL and not allowed and not un-ignored → skip
//
// This test exercises the Go-side [visibility.ApplyNoEventNoWriteMarks]
// (condition 1) and [visibility.ApplyInternalParameterMarks] (condition 2)
// in their canonical combinations, verifying that forced-usage is set to
// [hmenum.DataPointUsageIgnored] on the right DPs and left untouched on
// the rest.
func TestVisibilityFlagOperationsTriple(t *testing.T) {
	t.Parallel()

	type tripCase struct {
		name       string
		operations hmenum.Operations
		flags      hmenum.Flag
		wantIgnore bool // expected forced-usage after Apply* passes
	}

	cases := []tripCase{
		// ── condition 1: neither EVENT nor WRITE → ignore ────────────────
		{
			name:       "read_only_no_event_no_write",
			operations: hmenum.OperationsRead,
			flags:      hmenum.FlagVisible,
			wantIgnore: true,
		},
		{
			name:       "no_operations_at_all",
			operations: hmenum.OperationsNone,
			flags:      hmenum.FlagVisible,
			wantIgnore: true,
		},
		// ── condition 1 negated: WRITE set → keep ────────────────────────
		{
			name:       "read_write_keep",
			operations: hmenum.OperationsRead | hmenum.OperationsWrite,
			flags:      hmenum.FlagVisible,
			wantIgnore: false,
		},
		// ── condition 1 negated: EVENT set → keep ────────────────────────
		{
			name:       "read_event_keep",
			operations: hmenum.OperationsRead | hmenum.OperationsEvent,
			flags:      hmenum.FlagVisible,
			wantIgnore: false,
		},
		// ── condition 2: FLAGS_INTERNAL → ignore ─────────────────────────
		{
			name:       "internal_flag_write_ops",
			operations: hmenum.OperationsRead | hmenum.OperationsWrite,
			flags:      hmenum.FlagVisible | hmenum.FlagInternal,
			wantIgnore: true,
		},
		// ── both conditions: internal + read-only → ignore ───────────────
		{
			name:       "internal_and_read_only",
			operations: hmenum.OperationsRead,
			flags:      hmenum.FlagVisible | hmenum.FlagInternal,
			wantIgnore: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dev := device.New(device.Config{
				InterfaceID: "iface",
				Interface:   hmenum.InterfaceBidCosRF,
				Address:     "HM-TEST",
				Model:       "HM-TEST-MODEL",
			})
			ch := dev.AddChannel("HM-TEST:0", 0, "MAINTENANCE", hmenum.ParamsetKeyValues)

			param := hmenum.Parameter("TEST_PARAM")
			cfg := generic.Spec{
				Key: hmtypes.DataPointKey{
					InterfaceID:    "iface",
					ChannelAddress: ch.Address,
					ParamsetKey:    hmenum.ParamsetKeyValues,
					Parameter:      string(param),
				},
				Descriptor: hmproto.ParameterData{
					Type:       hmenum.ParameterTypeBool,
					Operations: tc.operations,
					Flags:      tc.flags,
				},
			}
			dp := generic.NewSwitch(cfg)
			ch.Put(dp)

			// Run both pipeline passes to mirror the production order.
			visibility.ApplyNoEventNoWriteMarks(dev)
			visibility.ApplyInternalParameterMarks(dev)

			gotUsage, gotSet := dp.ForcedUsage()
			if tc.wantIgnore {
				if !gotSet || gotUsage != hmenum.DataPointUsageIgnored {
					t.Errorf("case %q: want DataPointUsageIgnored set, got usage=%v set=%v",
						tc.name, gotUsage, gotSet)
				}
			} else {
				// For the "keep" cases the pass should not have touched
				// forced usage at all.
				if gotSet && gotUsage == hmenum.DataPointUsageIgnored {
					t.Errorf("case %q: want no Ignored mark, got usage=%v set=%v",
						tc.name, gotUsage, gotSet)
				}
			}
		})
	}
}
