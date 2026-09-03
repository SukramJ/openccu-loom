// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package device

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestChannelSiblingPrefersTheReceiver pins the four cases the three
// hand-written copies of this lookup used to answer differently.
//
// The receiver-wins branch is the one that had actually drifted: two
// copies short-circuited on the channel's own number, a third walked
// the device unconditionally and dereferenced a nil device on the way,
// so the same profile field resolved on one construction path and
// silently did not on another.
func TestChannelSiblingPrefersTheReceiver(t *testing.T) {
	t.Parallel()

	dev := New(Config{InterfaceID: "ccu-BidCos-RF", Address: "VCU0000001", Model: "HM-LC-Bl1-FM"})
	ch1 := dev.AddChannel("VCU0000001:1", 1, "BLIND", hmenum.ParamsetKeyValues)
	ch4 := dev.AddChannel("VCU0000001:4", 4, "BLIND", hmenum.ParamsetKeyValues)

	if got := ch4.Sibling(1); got != ch1 {
		t.Errorf("ch4.Sibling(1) = %v, want the device's channel 1", got)
	}
	if got := ch4.Sibling(4); got != ch4 {
		t.Errorf("ch4.Sibling(4) = %v, want the receiver itself", got)
	}
	if got := ch4.Sibling(9); got != nil {
		t.Errorf("ch4.Sibling(9) = %v, want nil — the device carries no channel 9", got)
	}

	// A channel built but not yet published into a device still answers
	// for its own number. Profile materialisation resolves absolute
	// channel numbers before the channel is attached, and the copy that
	// lacked this branch returned nil there — and dereferenced the nil
	// device on the way.
	detached := NewChannel("VCU0000001:2", 2, "BLIND", hmenum.ParamsetKeyValues)
	if got := detached.Sibling(2); got != detached {
		t.Errorf("detached.Sibling(2) = %v, want the receiver itself", got)
	}
	if got := detached.Sibling(1); got != nil {
		t.Errorf("detached.Sibling(1) = %v, want nil", got)
	}
	if got := (*Channel)(nil).Sibling(1); got != nil {
		t.Errorf("(*Channel)(nil).Sibling(1) = %v, want nil", got)
	}
}
