// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/event"
)

// TestEventGroupKeyFollowsTheReferenceLayout pins the external identity of a
// device-trigger event group against the layout the reference stack and the
// Python client both build:
//
//	event_group_<kind>_<channel-unique-id>      (reference)
//	loom_event_group_<kind>_<channel-unique-id> (here, namespaced)
//
// The expected values are transcribed from what the Python client's compat
// event-group model produces for the same inputs — measured, not inferred.
// They are deliberately not derived from this package's own helper, or the
// guard would prove only that it agrees with itself. Provenance is in
// notes/parity/, not here.
//
// It exists because the two disagreed. The daemon used to emit
// `loom_<channel>_event_group/homematic.keypress` — the channel first, a
// slash, and the kind unshortened — while `EventGroupSummary.unique_id`
// invited a client to key its registry on exactly that. No consumer could:
// the client recomputed the reference spelling instead, so the field was
// wrong in a way nothing failed on.
//
// The BidCoS case is the one that matters most. The central-id slot belongs
// inside the channel id, not in front of the whole key — building this with
// the generic CanonicalUniqueID prefix argument yields
// `loom_11a0001234_event_group_keypress_bidcos_rf_1`, which is a different
// string and therefore a different entity.
func TestEventGroupKeyFollowsTheReferenceLayout(t *testing.T) {
	t.Parallel()

	const serial = "11a0001234"

	cases := []struct {
		channel string
		kind    event.Kind
		want    string
	}{
		{"VCU1234567:1", event.KindKeypress, "loom_event_group_keypress_vcu1234567_1"},
		{"VCU1234567:1", event.KindImpulse, "loom_event_group_impulse_vcu1234567_1"},
		{"VCU1234567:1", event.KindDeviceError, "loom_event_group_device_error_vcu1234567_1"},
		// Virtual remote: the address repeats across CCUs, so the channel id
		// carries the central — behind the family prefix, not before it.
		{"BidCoS-RF:1", event.KindKeypress, "loom_event_group_keypress_11a0001234_bidcos_rf_1"},
		// Internal and CUxD addresses are already unique per CCU here.
		{"INT0000001:2", event.KindKeypress, "loom_event_group_keypress_int0000001_2"},
		{"CUX2801001:1", event.KindKeypress, "loom_event_group_keypress_cux2801001_1"},
	}

	for _, tc := range cases {
		g := event.NewGroupWithCentral("home", tc.channel, tc.kind)
		if got := g.CanonicalUniqueID(serial); got != tc.want {
			t.Errorf("%s / %s:\n  got  %q\n  want %q", tc.channel, tc.kind, got, tc.want)
		}
	}

	// An unresolved serial yields no key rather than one with an empty slot,
	// which would collide across CCUs for the virtual-remote family.
	if got := event.NewGroupWithCentral("home", "BidCoS-RF:1", event.KindKeypress).CanonicalUniqueID(""); got != "" {
		t.Errorf("unresolved serial produced %q, want an empty key", got)
	}
}
