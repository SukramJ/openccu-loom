// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package textdisplay

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestWriteWithSoundSendsIntervalAsInteger pins the wire type of INTERVAL:
// the HmIP-WRCD declares it INTEGER 1..15 (godevccu
// paramset_descriptions/HmIP-WRCD.json VCU4243444:3) and the reference
// validates and sends it as an int (text_display.py _MIN_INTERVAL /
// _MAX_INTERVAL, send_value(value=interval)).
func TestWriteWithSoundSendsIntervalAsInteger(t *testing.T) {
	w := &putWriter{}
	d := New("VCU4243444:3", w)
	if err := d.WriteWithSound(
		context.Background(),
		Row{ID: 1, Text: "Alarm"},
		SoundOptions{Sound: "SOUND_SHORT", Repetitions: "REPETITIONS_5", Interval: "5"},
		hmenum.CommandPriorityHigh,
	); err != nil {
		t.Fatal(err)
	}
	got := w.puts[0][string(hmenum.ParameterInterval)]
	if v, ok := got.(int32); !ok || v != 5 {
		t.Fatalf("INTERVAL=%v (%T), want int32(5)", got, got)
	}
}

// TestWriteWithSoundRejectsIntervalOutsideDescriptorRange is the negative
// control on the same rule: a non-integer label and an out-of-range value
// must fail validation instead of reaching the wire.
func TestWriteWithSoundRejectsIntervalOutsideDescriptorRange(t *testing.T) {
	for _, bad := range []string{"1S", "0", "16", "abc"} {
		w := &putWriter{}
		d := New("VCU4243444:3", w)
		err := d.WriteWithSound(
			context.Background(),
			Row{ID: 1, Text: "Alarm"},
			SoundOptions{Sound: "SOUND_SHORT", Interval: bad},
			hmenum.CommandPriorityHigh,
		)
		if !errors.Is(err, ErrInvalidInterval) {
			t.Errorf("Interval %q: err=%v, want ErrInvalidInterval", bad, err)
		}
		if len(w.puts) != 0 {
			t.Errorf("Interval %q reached the wire: %v", bad, w.puts)
		}
	}
}
