// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package custom

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

type stubCDP struct {
	device.AttachableDataPoint
	key hmtypes.DataPointKey
}

func (s *stubCDP) DataPointKey() hmtypes.DataPointKey { return s.key }

func newWireNameRig(t *testing.T) (*device.Device, map[int]device.AttachableDataPoint) {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "DRD0001"})
	dps := make(map[int]device.AttachableDataPoint)
	// Channel group: LEVEL on 4/5/6 (dimmer + virtual channels); a
	// unique BUTTON_LOCK CDP on ch0.
	for _, no := range []int{4, 5, 6} {
		ch := d.AddChannel("DRD0001:"+string(rune('0'+no)), no, "DIMMER", hmenum.ParamsetKeyValues)
		dp := &stubCDP{key: hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: "LEVEL"}}
		ch.SetCustomDataPoint(dp)
		dps[no] = dp
	}
	ch0 := d.AddChannel("DRD0001:0", 0, "MAINTENANCE", hmenum.ParamsetKeyValues)
	lock := &stubCDP{key: hmtypes.DataPointKey{ChannelAddress: ch0.Address, Parameter: "BUTTON_LOCK"}}
	ch0.SetCustomDataPoint(lock)
	dps[0] = lock
	return d, dps
}

func TestWireNameDisambiguatesChannelGroups(t *testing.T) {
	t.Parallel()
	d, dps := newWireNameRig(t)

	if got := WireName(d, dps[4], 4); got != "LEVEL@4" {
		t.Errorf("WireName(ch4) = %q, want LEVEL@4", got)
	}
	if got := WireName(d, dps[6], 6); got != "LEVEL@6" {
		t.Errorf("WireName(ch6) = %q, want LEVEL@6", got)
	}
	// Unique CDPs keep the bare name — single-CDP devices (the common
	// case) must not churn their wire identity.
	if got := WireName(d, dps[0], 0); got != "BUTTON_LOCK" {
		t.Errorf("WireName(ch0) = %q, want BUTTON_LOCK", got)
	}
}

func TestParseWireName(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in    string
		param string
		ch    int
		exact bool
	}{
		{"LEVEL@5", "LEVEL", 5, true},
		{"LEVEL", "LEVEL", 0, false},
		{"WEIRD@NAME", "WEIRD@NAME", 0, false}, // non-numeric suffix is part of the name
		{"A@B@7", "A@B", 7, true},              // last separator wins
	} {
		param, ch, exact := ParseWireName(tc.in)
		if param != tc.param || ch != tc.ch || exact != tc.exact {
			t.Errorf("ParseWireName(%q) = (%q,%d,%v), want (%q,%d,%v)",
				tc.in, param, ch, exact, tc.param, tc.ch, tc.exact)
		}
	}
}

func TestFindByWireName(t *testing.T) {
	t.Parallel()
	d, dps := newWireNameRig(t)

	// Channel-exact form resolves the requested group member.
	dp, chNo, ok := FindByWireName(d, "LEVEL@5")
	if !ok || chNo != 5 || dp != dps[5] {
		t.Errorf("FindByWireName(LEVEL@5) = (%v,%d,%v), want ch5 member", dp, chNo, ok)
	}
	// Bare name falls back to the first match (back-compat).
	if _, _, ok := FindByWireName(d, "LEVEL"); !ok {
		t.Error("FindByWireName(LEVEL) bare form must resolve")
	}
	// Unique CDP by bare name.
	dp, chNo, ok = FindByWireName(d, "BUTTON_LOCK")
	if !ok || chNo != 0 || dp != dps[0] {
		t.Errorf("FindByWireName(BUTTON_LOCK) = (%v,%d,%v), want ch0", dp, chNo, ok)
	}
	// Wrong channel selector misses.
	if _, _, ok := FindByWireName(d, "LEVEL@9"); ok {
		t.Error("FindByWireName(LEVEL@9) must not resolve")
	}
}
