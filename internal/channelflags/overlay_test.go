// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package channelflags

import "testing"

func TestOverlayGetOnEmptyReturnsZeroFlags(t *testing.T) {
	t.Parallel()
	o := New()
	if got := o.Get("ccu1", "DEV:1"); got != (Flags{}) {
		t.Errorf("Get on empty overlay = %+v, want zero Flags{}", got)
	}
}

func TestOverlaySetThenGetRoundTrips(t *testing.T) {
	t.Parallel()
	o := New()
	f := Flags{Hidden: true, Locked: false}
	o.Set("ccu1", "DEV:1", f)

	if got := o.Get("ccu1", "DEV:1"); got != f {
		t.Errorf("Get after Set = %+v, want %+v", got, f)
	}
}

func TestOverlaySetZeroFlagsRemovesEntryAndPrunesCentral(t *testing.T) {
	t.Parallel()
	o := New()
	o.Set("ccu1", "DEV:1", Flags{Hidden: true})

	o.Set("ccu1", "DEV:1", Flags{}) // clear
	if got := o.Get("ccu1", "DEV:1"); got != (Flags{}) {
		t.Errorf("Get after clearing Set = %+v, want zero Flags{}", got)
	}
	// White-box: the central's map entry must be pruned entirely once
	// empty, not left behind as an empty map.
	o.mu.RLock()
	_, exists := o.m["ccu1"]
	o.mu.RUnlock()
	if exists {
		t.Error("Set with zero Flags must prune the empty central map, not leave an empty entry")
	}
}

func TestOverlayReplaceSwapsCentralContentsAndDropsAllFalse(t *testing.T) {
	t.Parallel()
	o := New()
	o.Set("ccu1", "DEV:1", Flags{Hidden: true})
	o.Set("ccu1", "DEV:2", Flags{Locked: true})
	o.Set("ccu2", "OTHER:1", Flags{Hidden: true}) // different central, must stay untouched

	o.Replace("ccu1", map[string]Flags{
		"DEV:3": {Hidden: true},
		"DEV:4": {}, // all-false: must be dropped
	})

	if got := o.Get("ccu1", "DEV:1"); got != (Flags{}) {
		t.Errorf("DEV:1 should be gone after Replace, got %+v", got)
	}
	if got := o.Get("ccu1", "DEV:2"); got != (Flags{}) {
		t.Errorf("DEV:2 should be gone after Replace, got %+v", got)
	}
	if got := o.Get("ccu1", "DEV:3"); got != (Flags{Hidden: true}) {
		t.Errorf("DEV:3 = %+v, want Hidden=true", got)
	}
	if got := o.Get("ccu1", "DEV:4"); got != (Flags{}) {
		t.Errorf("DEV:4 (all-false entry) must be dropped by Replace, got %+v", got)
	}
	// Other central left untouched by a ccu1 Replace.
	if got := o.Get("ccu2", "OTHER:1"); got != (Flags{Hidden: true}) {
		t.Errorf("ccu2 entry must be untouched by ccu1 Replace, got %+v", got)
	}
}

func TestOverlayReplaceEmptyMapClearsCentral(t *testing.T) {
	t.Parallel()
	o := New()
	o.Set("ccu1", "DEV:1", Flags{Hidden: true})

	o.Replace("ccu1", nil)

	o.mu.RLock()
	_, exists := o.m["ccu1"]
	o.mu.RUnlock()
	if exists {
		t.Error("Replace with an empty map must remove the central entirely")
	}
}

func TestOverlayNilSafe(t *testing.T) {
	t.Parallel()
	var o *Overlay

	if got := o.Get("ccu1", "DEV:1"); got != (Flags{}) {
		t.Errorf("nil overlay Get = %+v, want zero Flags{}", got)
	}
	// Must not panic.
	o.Set("ccu1", "DEV:1", Flags{Hidden: true})
	o.Replace("ccu1", map[string]Flags{"DEV:1": {Hidden: true}})
}

func TestFlagsSetReportsEitherFlag(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		f    Flags
		want bool
	}{
		{"both false", Flags{}, false},
		{"hidden only", Flags{Hidden: true}, true},
		{"locked only", Flags{Locked: true}, true},
		{"both true", Flags{Hidden: true, Locked: true}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.f.Set(); got != tc.want {
				t.Errorf("Flags%+v.Set() = %v, want %v", tc.f, got, tc.want)
			}
		})
	}
}

// TestOverlayDeleteDeviceRemovesOwnAddressAndChannelsKeepsOtherDevice
// verifies that DeleteDevice drops both the device's own-address entry and
// every "<address>:<n>" channel entry, while leaving another device's
// entries (and a same-prefixed different device) untouched.
func TestOverlayDeleteDeviceRemovesOwnAddressAndChannelsKeepsOtherDevice(t *testing.T) {
	t.Parallel()
	o := New()
	o.Set("ccu1", "DEVICE-A", Flags{Locked: true})    // device-level entry
	o.Set("ccu1", "DEVICE-A:1", Flags{Hidden: true})  // channel entry
	o.Set("ccu1", "DEVICE-A:2", Flags{Locked: true})  // second channel entry
	o.Set("ccu1", "DEVICE-A2:1", Flags{Hidden: true}) // different device sharing the prefix
	o.Set("ccu1", "DEVICE-B:1", Flags{Hidden: true})  // unrelated device
	o.Set("ccu2", "DEVICE-A:1", Flags{Hidden: true})  // same address, different central

	o.DeleteDevice("ccu1", "DEVICE-A")

	if got := o.Get("ccu1", "DEVICE-A"); got != (Flags{}) {
		t.Errorf("DEVICE-A own-address entry survived DeleteDevice: %+v", got)
	}
	if got := o.Get("ccu1", "DEVICE-A:1"); got != (Flags{}) {
		t.Errorf("DEVICE-A:1 survived DeleteDevice: %+v", got)
	}
	if got := o.Get("ccu1", "DEVICE-A:2"); got != (Flags{}) {
		t.Errorf("DEVICE-A:2 survived DeleteDevice: %+v", got)
	}
	if got := o.Get("ccu1", "DEVICE-A2:1"); got != (Flags{Hidden: true}) {
		t.Errorf("DEVICE-A2:1 (prefix collision) must survive, got %+v", got)
	}
	if got := o.Get("ccu1", "DEVICE-B:1"); got != (Flags{Hidden: true}) {
		t.Errorf("DEVICE-B:1 must survive, got %+v", got)
	}
	if got := o.Get("ccu2", "DEVICE-A:1"); got != (Flags{Hidden: true}) {
		t.Errorf("ccu2's DEVICE-A:1 must survive a ccu1 DeleteDevice, got %+v", got)
	}
}

// TestOverlayDeleteDeviceOnEmptyOrNilIsNoOp verifies that DeleteDevice never
// panics on an empty overlay, an unknown central, or a nil receiver.
func TestOverlayDeleteDeviceOnEmptyOrNilIsNoOp(t *testing.T) {
	t.Parallel()
	o := New()
	o.DeleteDevice("ccu1", "DEVICE-A") // unknown central: no-op

	var nilOverlay *Overlay
	nilOverlay.DeleteDevice("ccu1", "DEVICE-A") // nil receiver: no-op
}
