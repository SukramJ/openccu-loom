// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package device

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ─── Device.IdentifyChannel ────────────────────────────────────────────

// TestDeviceIdentifyChannelEmptyText verifies that an empty text never
// matches, regardless of the device/channel setup.
func TestDeviceIdentifyChannelEmptyText(t *testing.T) {
	t.Parallel()

	d := New(Config{Address: "0001ABCD", IseID: 100})
	ch := d.AddChannel("0001ABCD:1", 1, "SWITCH", hmenum.ParamsetKeyValues)
	ch.SetIseID(200)

	if got := d.IdentifyChannel(""); got != nil {
		t.Fatalf("IdentifyChannel(\"\") = %v, want nil", got)
	}
}

// TestDeviceIdentifyChannelMatchesByAddressSuffix verifies that a text
// ending with a channel's address matches that channel directly, ahead of
// any ise_id heuristics.
func TestDeviceIdentifyChannelMatchesByAddressSuffix(t *testing.T) {
	t.Parallel()

	d := New(Config{Address: "0001ABCD", IseID: 999})
	ch := d.AddChannel("0001ABCD:1", 1, "SWITCH", hmenum.ParamsetKeyValues)
	ch.SetIseID(111)

	got := d.IdentifyChannel("svEnergy_0001ABCD:1")
	if got == nil {
		t.Fatal("IdentifyChannel(): expected a match, got nil")
	}
	if got.Address != "0001ABCD:1" {
		t.Fatalf("IdentifyChannel(): matched channel %q, want %q", got.Address, "0001ABCD:1")
	}
}

// TestDeviceIdentifyChannelMatchesByChannelIseID verifies that a text
// carrying the channel's ise_id as a standalone token matches that channel.
func TestDeviceIdentifyChannelMatchesByChannelIseID(t *testing.T) {
	t.Parallel()

	d := New(Config{Address: "0001ABCD", IseID: 999})
	ch := d.AddChannel("0001ABCD:1", 1, "SWITCH", hmenum.ParamsetKeyValues)
	ch.SetIseID(200)

	// '_' is itself a word character (see isWordChar), so it does not act as a
	// token boundary — use a space to bound the ise_id unambiguously.
	got := d.IdentifyChannel("sv 200 x")
	if got == nil {
		t.Fatal("IdentifyChannel(): expected a match, got nil")
	}
	if got.Address != "0001ABCD:1" {
		t.Fatalf("IdentifyChannel(): matched channel %q, want %q", got.Address, "0001ABCD:1")
	}
}

// TestDeviceIdentifyChannelMatchesByDeviceIseID verifies that when only the
// device-wide ise_id appears in text, the first channel in sorted-address
// order is returned.
func TestDeviceIdentifyChannelMatchesByDeviceIseID(t *testing.T) {
	t.Parallel()

	d := New(Config{Address: "0001ABCD", IseID: 100})
	// Add out of address order; neither channel's own ise_id appears in text.
	ch2 := d.AddChannel("0001ABCD:2", 2, "SWITCH", hmenum.ParamsetKeyValues)
	ch2.SetIseID(666)
	ch1 := d.AddChannel("0001ABCD:1", 1, "SWITCH", hmenum.ParamsetKeyValues)
	ch1.SetIseID(555)

	got := d.IdentifyChannel("sv 100")
	if got == nil {
		t.Fatal("IdentifyChannel(): expected a match, got nil")
	}
	if got.Address != "0001ABCD:1" {
		t.Fatalf("IdentifyChannel(): matched channel %q, want first-sorted %q", got.Address, "0001ABCD:1")
	}
}

// TestDeviceIdentifyChannelWordBoundaryNegative verifies that an ise_id
// which is a substring of a larger number in text does NOT match.
func TestDeviceIdentifyChannelWordBoundaryNegative(t *testing.T) {
	t.Parallel()

	d := New(Config{Address: "0001ABCD", IseID: 123})
	ch := d.AddChannel("0001ABCD:1", 1, "SWITCH", hmenum.ParamsetKeyValues)
	ch.SetIseID(0) // ignored — must not participate in matching

	if got := d.IdentifyChannel("sv_41234"); got != nil {
		t.Fatalf("IdentifyChannel(\"sv_41234\") = %v, want nil (123 is a substring of 41234, not a standalone word)", got)
	}
}

// TestDeviceIdentifyChannelWordBoundaryPositive verifies that an ise_id
// bounded by non-word characters on both sides does match.
func TestDeviceIdentifyChannelWordBoundaryPositive(t *testing.T) {
	t.Parallel()

	d := New(Config{Address: "0001ABCD", IseID: 123})
	ch := d.AddChannel("0001ABCD:1", 1, "SWITCH", hmenum.ParamsetKeyValues)
	ch.SetIseID(0) // ignored — must not participate in matching

	// '_' is itself a word character (see isWordChar), so it does not act as a
	// token boundary — use a hyphen to bound the ise_id unambiguously.
	got := d.IdentifyChannel("sv-123-x")
	if got == nil {
		t.Fatal("IdentifyChannel(\"sv-123-x\"): expected a match, got nil")
	}
	if got.Address != "0001ABCD:1" {
		t.Fatalf("IdentifyChannel(): matched channel %q, want %q", got.Address, "0001ABCD:1")
	}
}

// TestDeviceIdentifyChannelZeroIseIDIgnored verifies that a device and
// channel both carrying ise_id == 0 are never matched, even when the text
// literally contains "0".
func TestDeviceIdentifyChannelZeroIseIDIgnored(t *testing.T) {
	t.Parallel()

	d := New(Config{Address: "0001ABCD", IseID: 0})
	ch := d.AddChannel("0001ABCD:1", 1, "SWITCH", hmenum.ParamsetKeyValues)
	ch.SetIseID(0)

	if got := d.IdentifyChannel("sv_0"); got != nil {
		t.Fatalf("IdentifyChannel(\"sv_0\") = %v, want nil (ise_id == 0 must be ignored)", got)
	}
}

// TestDeviceIdentifyChannelChannelMatchTakesPriorityOverDeviceMatch verifies
// that a channel-specific ise_id match wins over the device-wide ise_id
// fallback, even when the device-wide ise_id also appears in text and would
// otherwise resolve to a different (first-sorted) channel.
func TestDeviceIdentifyChannelChannelMatchTakesPriorityOverDeviceMatch(t *testing.T) {
	t.Parallel()

	d := New(Config{Address: "0001ABCD", IseID: 100})
	// chX is first in sorted-address order and would be picked by the
	// device-wide fallback, but its own ise_id is 0 (ignored).
	chX := d.AddChannel("0001ABCD:1", 1, "SWITCH", hmenum.ParamsetKeyValues)
	chX.SetIseID(0)
	// chY is second in sorted-address order but carries an ise_id that
	// appears in text — the channel-specific match must win.
	chY := d.AddChannel("0001ABCD:2", 2, "SWITCH", hmenum.ParamsetKeyValues)
	chY.SetIseID(200)

	got := d.IdentifyChannel("sv 100 200")
	if got == nil {
		t.Fatal("IdentifyChannel(): expected a match, got nil")
	}
	if got.Address != "0001ABCD:2" {
		t.Fatalf("IdentifyChannel(): matched channel %q, want channel-specific match %q (not device-wide %q)",
			got.Address, "0001ABCD:2", "0001ABCD:1")
	}
}

// TestDeviceIdentifyChannelDeviceWideMatchIsDeterministic verifies that a
// device-wide ise_id match resolves to the same first-sorted channel across
// repeated calls (map iteration would otherwise make this non-deterministic).
func TestDeviceIdentifyChannelDeviceWideMatchIsDeterministic(t *testing.T) {
	t.Parallel()

	d := New(Config{Address: "0001ABCD", IseID: 100})
	// Register channels out of address order; none of their own ise_ids
	// appear in the probe text, so only the device-wide fallback can match.
	ch3 := d.AddChannel("0001ABCD:3", 3, "SWITCH", hmenum.ParamsetKeyValues)
	ch3.SetIseID(777)
	ch1 := d.AddChannel("0001ABCD:1", 1, "SWITCH", hmenum.ParamsetKeyValues)
	ch1.SetIseID(555)
	ch2 := d.AddChannel("0001ABCD:2", 2, "SWITCH", hmenum.ParamsetKeyValues)
	ch2.SetIseID(666)

	for i := range 20 {
		got := d.IdentifyChannel("sv 100")
		if got == nil {
			t.Fatalf("run %d: IdentifyChannel(): expected a match, got nil", i)
		}
		if got.Address != "0001ABCD:1" {
			t.Fatalf("run %d: IdentifyChannel(): matched channel %q, want deterministic first-sorted %q", i, got.Address, "0001ABCD:1")
		}
	}
}

// ─── containsWord ───────────────────────────────────────────────────────

func TestContainsWord(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		text string
		word string
		want bool
	}{
		{name: "empty word never matches", text: "anything", word: "", want: false},
		{name: "empty text never matches", text: "", word: "foo", want: false},
		{name: "exact full-string match", text: "123", word: "123", want: true},
		{name: "letters on both sides", text: "a123b", word: "123", want: false},
		// '_' counts as a word character (isWordChar), so it does NOT act as a
		// token boundary — "123" stays glued to its neighbours and must not match.
		{name: "underscore does not act as a boundary", text: "a_123_b", word: "123", want: false},
		{name: "hyphen boundaries", text: "a-123-b", word: "123", want: true},
		{name: "colon boundaries", text: "a:123:b", word: "123", want: true},
		{name: "space boundaries", text: "a 123 b", word: "123", want: true},
		{name: "letter suffix at string end blocks match", text: "123abc", word: "123", want: false},
		{name: "letter prefix at string start blocks match", text: "prefix123", word: "123", want: false},
		{name: "must keep scanning past a non-boundary occurrence", text: "a123b 123 c", word: "123", want: true},
		{name: "unicode letters on both sides block match", text: "über12ung", word: "12", want: false},
		{name: "hyphen boundaries around short word", text: "x-12-y", word: "12", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := containsWord(tt.text, tt.word); got != tt.want {
				t.Fatalf("containsWord(%q, %q) = %v, want %v", tt.text, tt.word, got, tt.want)
			}
		})
	}
}

// ─── isWordChar ──────────────────────────────────────────────────────────

func TestIsWordChar(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		r    rune
		want bool
	}{
		{name: "ascii letter", r: 'a', want: true},
		{name: "digit", r: '5', want: true},
		{name: "underscore", r: '_', want: true},
		{name: "hyphen", r: '-', want: false},
		{name: "colon", r: ':', want: false},
		{name: "space", r: ' ', want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isWordChar(tt.r); got != tt.want {
				t.Fatalf("isWordChar(%q) = %v, want %v", tt.r, got, tt.want)
			}
		})
	}
}
