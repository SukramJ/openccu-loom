// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// fakeConnectivityProbe returns a fixed reachability list carrying the bare
// interface names that Interface.listInterfaces reports.
type fakeConnectivityProbe struct {
	out []coordinators.InterfaceReachability
}

func (f fakeConnectivityProbe) Probe(context.Context) ([]coordinators.InterfaceReachability, error) {
	return f.out, nil
}

// TestObserveProbeLatencyStampsWireInterfaceID pins the fix for the permanently
// "disconnected" per-interface connectivity sensors: the probe reports bare
// interface names (HmIP-RF), but the client keys its sensors on GET
// /interfaces.id — the wire form <central>-<interface>. stampWireInterfaceIDs
// must rewrite each entry's InterfaceID to exactly that wire id (and keep the
// bare enum), so connectivity lines up with /interfaces.
func TestObserveProbeLatencyStampsWireInterfaceID(t *testing.T) {
	t.Parallel()

	probe := fakeConnectivityProbe{out: []coordinators.InterfaceReachability{
		{InterfaceID: "HmIP-RF", Reachable: true, LatencyMs: 12},
		{InterfaceID: "BidCos-RF", Reachable: false, LatencyMs: 12},
	}}

	// h is nil: the latency-metric side is exercised elsewhere; here we only
	// assert the interface-id rewrite, which must not depend on a hub.
	wrapped := stampWireInterfaceIDs(probe, "ccu-a")
	got, err := wrapped(context.Background())
	if err != nil {
		t.Fatalf("wrapped probe: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2", len(got))
	}

	for i, bare := range []hmenum.Interface{hmenum.InterfaceHmIPRF, "BidCos-RF"} {
		wantID := WireInterfaceID("ccu-a", bare)
		if got[i].InterfaceID != wantID {
			t.Errorf("entry %d InterfaceID = %q, want the wire id %q (must equal GET /interfaces.id)",
				i, got[i].InterfaceID, wantID)
		}
		if got[i].Interface != bare {
			t.Errorf("entry %d Interface = %q, want the bare enum %q", i, got[i].Interface, bare)
		}
		// ResolvedInterface must yield the bare enum, not the wire id parsed
		// as an interface token — that is what keeps `interface` renderable.
		if r := got[i].ResolvedInterface(); r != bare {
			t.Errorf("entry %d ResolvedInterface = %q, want %q", i, r, bare)
		}
	}
}
