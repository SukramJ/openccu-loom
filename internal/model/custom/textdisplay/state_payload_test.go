// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package textdisplay

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestTextDisplayStatePayloadHasAvailableIcons verifies that StatePayload
// includes a non-empty available_icons list containing a well-known marker
// value.
func TestTextDisplayStatePayloadHasAvailableIcons(t *testing.T) {
	t.Parallel()

	td := New("VCU3756007:3", &stubWriter{})
	state, ok := td.State().(*payload.TextDisplayState)
	if !ok || state == nil {
		t.Fatal("StatePayload returned nil")
	}

	if len(state.AvailableIcons) == 0 {
		t.Fatal("StatePayload missing key available_icons")
	}

	// "OK" is present in the static HmIP-WRCD list (defaultIcons).
	found := false
	for _, icon := range state.AvailableIcons {
		if icon == "OK" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("available_icons does not contain expected marker \"OK\"; got %v", state.AvailableIcons)
	}
}

// TestTextDisplayStatePayloadHasAvailableSounds verifies that StatePayload
// includes a non-empty available_sounds list containing a well-known marker
// value.
func TestTextDisplayStatePayloadHasAvailableSounds(t *testing.T) {
	t.Parallel()

	td := New("VCU3756007:3", &stubWriter{})
	state, ok := td.State().(*payload.TextDisplayState)
	if !ok || state == nil {
		t.Fatal("StatePayload returned nil")
	}

	if len(state.AvailableSounds) == 0 {
		t.Fatal("StatePayload missing key available_sounds")
	}

	// "SHORT" is present in the static HmIP-WRCD list (defaultSounds).
	found := false
	for _, snd := range state.AvailableSounds {
		if snd == "SHORT" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("available_sounds does not contain expected marker \"SHORT\"; got %v", state.AvailableSounds)
	}
}

// TestTextDisplaySetAvailableIconsOverridesDefault verifies that
// SetAvailableIcons replaces the static fallback with runtime values.
func TestTextDisplaySetAvailableIconsOverridesDefault(t *testing.T) {
	t.Parallel()

	td := New("VCU3756007:3", &stubWriter{})
	custom := []string{"ICON_A", "ICON_B"}
	td.SetAvailableIcons(custom)

	got := td.AvailableIcons()
	if len(got) != len(custom) {
		t.Fatalf("len(AvailableIcons) = %d, want %d", len(got), len(custom))
	}
	for i, want := range custom {
		if got[i] != want {
			t.Errorf("AvailableIcons[%d] = %q, want %q", i, got[i], want)
		}
	}
}

// TestTextDisplaySetAvailableSoundsOverridesDefault verifies that
// SetAvailableSounds replaces the static fallback with runtime values.
func TestTextDisplaySetAvailableSoundsOverridesDefault(t *testing.T) {
	t.Parallel()

	td := New("VCU3756007:3", &stubWriter{})
	custom := []string{"SND_A", "SND_B", "SND_C"}
	td.SetAvailableSounds(custom)

	got := td.AvailableSounds()
	if len(got) != len(custom) {
		t.Fatalf("len(AvailableSounds) = %d, want %d", len(got), len(custom))
	}
	for i, want := range custom {
		if got[i] != want {
			t.Errorf("AvailableSounds[%d] = %q, want %q", i, got[i], want)
		}
	}
}

// TestTextDisplayWriteAcceptsColor verifies that the "write" service method
// forwards a "color" param as the TextColor on the Row, causing
// DISPLAY_DATA_TEXT_COLOR to be written to the device.
func TestTextDisplayWriteAcceptsColor(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	td := New("VCU3756007:3", w)

	params := map[string]any{
		"id":    int32(1),
		"text":  "Hello",
		"color": "RED",
	}
	if err := td.Invoke(context.Background(), "write", params, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("write service returned error: %v", err)
	}

	// DISPLAY_DATA_TEXT_COLOR must appear in the emitted parameters.
	found := slices.Contains(w.params(), hmenum.ParameterDisplayDataTextColor)
	if !found {
		t.Errorf("write with color: DISPLAY_DATA_TEXT_COLOR not emitted; params=%v", w.params())
	}
}

// --- Info / Config payload ---

func TestTextDisplayInfoPayload(t *testing.T) {
	t.Parallel()

	d := New("HmIP-WRCD:3", &stubWriter{})
	p, ok := d.Info().(*payload.TextDisplayInfo)
	if !ok || p == nil {
		t.Fatal("InfoPayload must return a non-nil *payload.TextDisplayInfo")
	}
	if p.Category != "text_display" {
		t.Errorf("InfoPayload category = %v, want text_display", p.Category)
	}
}

func TestTextDisplayInfoPayloadNilReceiverReturnsNil(t *testing.T) {
	t.Parallel()

	var d *TextDisplay
	if p := d.Info(); p != nil {
		t.Errorf("nil TextDisplay.Info() = %v, want nil", p)
	}
}

func TestTextDisplayConfigPayload(t *testing.T) {
	t.Parallel()

	d := New("x", &stubWriter{})
	p, _ := d.Config().(*payload.TextDisplayConfig)
	if p == nil {
		t.Fatal("ConfigPayload must not be nil")
	}
	if !p.WriteOnly {
		t.Errorf("ConfigPayload write_only = %v, want true", p.WriteOnly)
	}
}

func TestTextDisplayConfigPayloadNilReceiverReturnsNil(t *testing.T) {
	t.Parallel()

	var d *TextDisplay
	if p := d.Config(); p != nil {
		t.Errorf("nil TextDisplay.Config() = %v, want nil", p)
	}
}

func TestTextDisplayStatePayloadContainsDefaultIcons(t *testing.T) {
	t.Parallel()

	d := New("x", &stubWriter{})
	p, ok := d.State().(*payload.TextDisplayState)
	if !ok || p == nil {
		t.Fatal("StatePayload must not be nil")
	}
	if len(p.AvailableIcons) == 0 {
		t.Fatal("StatePayload must include available_icons")
	}
}

func TestTextDisplayStatePayloadContainsDefaultSounds(t *testing.T) {
	t.Parallel()

	d := New("x", &stubWriter{})
	p, ok := d.State().(*payload.TextDisplayState)
	if !ok || p == nil {
		t.Fatal("StatePayload must not be nil")
	}
	if len(p.AvailableSounds) == 0 {
		t.Fatal("StatePayload must include available_sounds")
	}
}

func TestTextDisplayStatePayloadNilReceiverReturnsNil(t *testing.T) {
	t.Parallel()

	var d *TextDisplay
	if p := d.State(); p != nil {
		t.Errorf("nil TextDisplay.State() = %v, want nil", p)
	}
}

// TestTextDisplayStatePayloadNewOptionLists verifies that State() includes
// the five new option-list fields when set via their respective setters.
func TestTextDisplayStatePayloadNewOptionLists(t *testing.T) {
	t.Parallel()

	d := New("x", &stubWriter{})
	d.SetAvailableBackgroundColors([]string{"BLACK", "WHITE"})
	d.SetAvailableTextColors([]string{"RED", "GREEN", "BLUE"})
	d.SetAvailableAlignments([]string{"LEFT", "CENTER", "RIGHT"})
	d.SetAvailableRepetitions([]string{"REPETITIONS_001", "REPETITIONS_003"})
	d.SetAvailableIntervals([]string{"INTERVAL_SHORT", "INTERVAL_LONG"})

	p, ok := d.State().(*payload.TextDisplayState)
	if !ok || p == nil {
		t.Fatal("StatePayload must not be nil")
	}

	checkList := func(name string, got []any, want []string) {
		t.Helper()
		if len(got) != len(want) {
			t.Errorf("%s: len=%d, want %d", name, len(got), len(want))
			return
		}
		for i, w := range want {
			if got[i] != w {
				t.Errorf("%s[%d] = %v, want %q", name, i, got[i], w)
			}
		}
	}

	checkList("available_background_colors", p.AvailableBackgroundColors, []string{"BLACK", "WHITE"})
	checkList("available_text_colors", p.AvailableTextColors, []string{"RED", "GREEN", "BLUE"})
	checkList("available_alignments", p.AvailableAlignments, []string{"LEFT", "CENTER", "RIGHT"})
	checkList("available_repetitions", p.AvailableRepetitions, []string{"REPETITIONS_001", "REPETITIONS_003"})
	checkList("available_intervals", p.AvailableIntervals, []string{"INTERVAL_SHORT", "INTERVAL_LONG"})
}

// TestTextDisplayStatePayloadNewOptionListsOmitWhenEmpty verifies that the
// five new option-list JSON keys are absent when the lists are not set (omitempty).
func TestTextDisplayStatePayloadNewOptionListsOmitWhenEmpty(t *testing.T) {
	t.Parallel()

	d := New("x", &stubWriter{})
	p, ok := d.State().(*payload.TextDisplayState)
	if !ok || p == nil {
		t.Fatal("StatePayload must not be nil")
	}

	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	js := string(raw)

	for _, key := range []string{
		"available_background_colors",
		"available_text_colors",
		"available_alignments",
		"available_repetitions",
		"available_intervals",
	} {
		if strings.Contains(js, `"`+key+`"`) {
			t.Errorf("JSON must not contain %q when list is empty; got: %s", key, js)
		}
	}
}

// TestStringsToAnyNilForEmpty verifies the stringsToAny helper returns nil
// for empty and nil inputs so omitempty suppresses the JSON key.
func TestStringsToAnyNilForEmpty(t *testing.T) {
	t.Parallel()

	if got := stringsToAny(nil); got != nil {
		t.Errorf("stringsToAny(nil) = %v, want nil", got)
	}
	if got := stringsToAny([]string{}); got != nil {
		t.Errorf("stringsToAny([]) = %v, want nil", got)
	}

	got := stringsToAny([]string{"A", "B"})
	if len(got) != 2 || got[0] != "A" || got[1] != "B" {
		t.Errorf("stringsToAny([A,B]) = %v, want [A B]", got)
	}
}
