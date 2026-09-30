// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// TestRSSIMatrixNormalisesWrappedWireEncodings pins that the raw BidCos wire
// encoding never reaches the API or the ranking: the daemon fills the matrix
// with the negated unsigned frame byte, so a strong signal arrives re-wrapped
// below -128 (a true -66 dBm as -190) and a frame byte of zero arrives as 0.
// Display must fold the wrapped band back into dBm and drop the markers, and
// the best-gateway ranking must compare normalised readings — otherwise a
// strong receiver loses to a weak one and the proposal moves the device the
// wrong way.
func TestRSSIMatrixNormalisesWrappedWireEncodings(t *testing.T) {
	t.Parallel()
	const addr = "ABC0000001"
	// The daemon mirrors every reading into both rows: the device row
	// carries "gateway hears device" at index 1, the gateway row the same
	// reading at index 0. GW1 hears the device at raw -190 = true -66 dBm
	// (wrapped band); GW2 at a genuine -100 dBm — GW1 is the stronger
	// receiver. The device→GW2 direction carries a marker 0.
	matrix := map[string]map[string][2]int{
		addr:  {"GW1": {noInfo, -190}, "GW2": {0, -100}},
		"GW1": {addr: {-190, noInfo}},
	}
	d, _ := buildRSSIMatrixFixture(t, matrix, []hmproto.DeviceDescription{desc(addr, "GW2", false)})

	centrals, err := d.RSSIMatrix(context.Background())
	if err != nil {
		t.Fatalf("RSSIMatrix: %v", err)
	}
	if len(centrals) != 1 {
		t.Fatalf("centrals = %d, want 1", len(centrals))
	}
	var gw1Rx *int
	for _, dev := range centrals[0].Devices {
		if dev.Address != "GW1" {
			continue
		}
		for _, p := range dev.Partners {
			if p.Address == addr {
				gw1Rx = p.RxDBm
			}
		}
	}
	if gw1Rx == nil || *gw1Rx != -66 {
		t.Fatalf("GW1 hears %s at %v, want the unwrapped -66 dBm", addr, gw1Rx)
	}
	for _, dev := range centrals[0].Devices {
		if dev.Address != addr {
			continue
		}
		for _, p := range dev.Partners {
			if p.Address == "GW2" && p.RxDBm != nil {
				t.Fatalf("marker 0 must map to nil, got %d", *p.RxDBm)
			}
		}
	}

	proposals, err := d.ReceiverProposal(context.Background(), 6)
	if err != nil {
		t.Fatalf("ReceiverProposal: %v", err)
	}
	if len(proposals) != 1 {
		t.Fatalf("proposals = %d, want 1", len(proposals))
	}
	p := proposals[0]
	if p.BestInterface != "GW1" || p.BestRxDBm == nil || *p.BestRxDBm != -66 {
		t.Fatalf("best = %s @ %v, want GW1 @ -66 (the wrapped strong reading must win)", p.BestInterface, p.BestRxDBm)
	}
	if p.Verdict != interfaces.ReceiverSwitch {
		t.Fatalf("verdict = %s, want switch away from GW2 (-100) to GW1 (-66)", p.Verdict)
	}
}
