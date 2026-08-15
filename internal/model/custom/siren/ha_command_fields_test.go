// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package siren

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestSirenTurnOnAcceptsTheFieldNamesHomeAssistantSends covers the gap
// between what the discovery payload advertises and what the command
// handler reads.
//
// The siren advertises `available_tones`, so Home Assistant renders a
// tone selector and publishes the choice as `{"state":"ON","tone":"…"}`
// on the command topic. The handler read `acoustic_selection` and
// nothing mapped one onto the other, so the tone was dropped without a
// trace and the siren fired with whatever it defaults to. The same holds
// for `volume_level`, which HA sends whenever `support_volume_set` is
// advertised.
//
// The original names keep working: they are what the REST and WebSocket
// surfaces send, and an automation written against them must not break.
func TestSirenTurnOnAcceptsTheFieldNamesHomeAssistantSends(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		params map[string]any
		want   string
	}{
		{
			name:   "HA sends tone",
			params: map[string]any{"tone": "FREQUENCY_RISING"},
			want:   "FREQUENCY_RISING",
		},
		{
			name:   "the domain name still works",
			params: map[string]any{"acoustic_selection": "FREQUENCY_FALLING"},
			want:   "FREQUENCY_FALLING",
		},
		{
			name: "the domain name wins when both arrive",
			params: map[string]any{
				"acoustic_selection": "FREQUENCY_FALLING",
				"tone":               "FREQUENCY_RISING",
			},
			want: "FREQUENCY_FALLING",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := &stubWriter{}
			s, _ := newWireRig(t, rec, custom.SirenCapabilities{SupportsAcoustic: true})
			if err := s.Invoke(context.Background(), "turn_on", tc.params, hmenum.CommandPriorityHigh); err != nil {
				t.Fatalf("turn_on: %v", err)
			}
			got := lastStringWrite(rec, hmenum.ParameterAcousticAlarmSelection)
			if got != tc.want {
				t.Errorf("acoustic selection on the wire = %q, want %q — a tone the operator picked "+
					"that never reaches the device makes the selector decoration", got, tc.want)
			}
		})
	}
}

// TestSoundPlayerTurnOnAcceptsTheFieldNamesHomeAssistantSends is the
// same gap on the MP3 player: its `available_tones` are soundfile
// labels, HA returns the chosen one as `tone`, and the handler read only
// a numeric `soundfile_index`.
func TestSoundPlayerTurnOnAcceptsTheFieldNamesHomeAssistantSends(t *testing.T) {
	t.Parallel()

	sp, rec := newSoundPlayerRig(t)
	if err := sp.Invoke(context.Background(), "turn_on",
		map[string]any{"tone": "SOUNDFILE_002"}, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("turn_on: %v", err)
	}
	if got := lastStringWrite(rec, hmenum.ParameterSoundfile); got != "SOUNDFILE_002" {
		t.Errorf("soundfile on the wire = %q, want %q", got, "SOUNDFILE_002")
	}
}

// lastStringWrite returns the most recent string written to parameter,
// or "" when it was never written.
func lastStringWrite(w *stubWriter, parameter hmenum.Parameter) string {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := ""
	for _, c := range w.calls {
		if c.param != parameter {
			continue
		}
		if s, ok := c.value.(string); ok {
			out = s
		}
	}
	return out
}

// TestSoundfileLabelResolvesArithmetically pins the inverse of
// ConvertSoundfileIndex against the mistake that passes on a tidy
// fixture: reading the index off the VALUE_LIST position.
//
// The label is built as SOUNDFILE_%03d, so the number in the label is
// the index. A list with a gap — or one that does not start at 001 —
// makes the positional reading play a different file than the operator
// picked, and no test built on a gapless fixture would notice.
func TestSoundfileLabelResolvesArithmetically(t *testing.T) {
	t.Parallel()

	sp, _ := newSoundPlayerRig(t)
	sp.availableSF = []string{"SOUNDFILE_007", "SOUNDFILE_042"}

	if idx, ok := sp.soundfileIndexFor("SOUNDFILE_042"); !ok || idx != 42 {
		t.Errorf("SOUNDFILE_042 → (%d, %v), want (42, true) — the second entry of the list, but "+
			"file 42 on the device", idx, ok)
	}
	if idx, ok := sp.soundfileIndexFor("SOUNDFILE_001"); ok {
		t.Errorf("a label the device does not offer resolved to %d; it must be refused", idx)
	}
	if idx, ok := sp.soundfileIndexFor("BADGER"); ok {
		t.Errorf("a non-soundfile label resolved to %d", idx)
	}
}
