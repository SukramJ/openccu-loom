// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package textdisplay

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

type stubWriter struct {
	mu    sync.Mutex
	calls []call
}

type call struct {
	param hmenum.Parameter
	value any
}

func (s *stubWriter) SetValue(_ context.Context, _ string, p hmenum.Parameter, v any, _ hmenum.CommandPriority) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, call{p, v})
	return nil
}

func (s *stubWriter) params() []hmenum.Parameter {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]hmenum.Parameter, len(s.calls))
	for i, c := range s.calls {
		out[i] = c.param
	}
	return out
}

type putWriter struct {
	stubWriter
	puts []map[string]any
}

func (p *putWriter) PutParamset(_ context.Context, _ string, _ hmenum.ParamsetKey, values map[string]any, _ hmenum.CommandPriority) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	cp := make(map[string]any, len(values))
	for k, v := range values {
		cp[k] = v
	}
	p.puts = append(p.puts, cp)
	return nil
}

func TestTextDisplayWriteAtomicPutParamset(t *testing.T) {
	w := &putWriter{}
	d := New("VCU3756007:3", w)
	align := AlignCenter
	textColor := "BLACK"
	bgColor := "WHITE"
	if err := d.Write(context.Background(), Row{
		ID:              1,
		Text:            "Hello World",
		Icon:            "NO_ICON",
		Alignment:       &align,
		TextColor:       &textColor,
		BackgroundColor: &bgColor,
	}, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if len(w.puts) != 1 {
		t.Fatalf("expected 1 put_paramset, got %d (sets=%d)", len(w.puts), len(w.calls))
	}
	got := w.puts[0]
	required := []hmenum.Parameter{
		hmenum.ParameterDisplayDataID,
		hmenum.ParameterDisplayDataString,
		hmenum.ParameterDisplayDataIcon,
		hmenum.ParameterDisplayDataAlignment,
		hmenum.ParameterDisplayDataTextColor,
		hmenum.ParameterDisplayDataBackgroundColor,
		hmenum.ParameterDisplayDataCommit,
	}
	for _, p := range required {
		if _, ok := got[string(p)]; !ok {
			t.Errorf("missing %s in atomic batch", p)
		}
	}
	if got[string(hmenum.ParameterDisplayDataCommit)] != true {
		t.Errorf("DISPLAY_DATA_COMMIT=%v want true", got[string(hmenum.ParameterDisplayDataCommit)])
	}
}

func TestTextDisplayWriteWithSoundAtomicPutParamset(t *testing.T) {
	w := &putWriter{}
	d := New("VCU3756007:3", w)
	if err := d.WriteWithSound(
		context.Background(),
		Row{ID: 1, Text: "Alarm"},
		SoundOptions{Sound: "SOUND_SHORT", Repetitions: "REPETITIONS_5", Interval: "1S"},
		hmenum.CommandPriorityHigh,
	); err != nil {
		t.Fatal(err)
	}
	if len(w.puts) != 1 {
		t.Fatalf("expected 1 put_paramset, got %d", len(w.puts))
	}
	got := w.puts[0]
	if got[string(hmenum.ParameterAcousticNotificationSelection)] != "SOUND_SHORT" {
		t.Errorf("SOUND=%v", got[string(hmenum.ParameterAcousticNotificationSelection)])
	}
	if got[string(hmenum.ParameterRepetitions)] != "REPETITIONS_5" {
		t.Errorf("REPETITIONS=%v", got[string(hmenum.ParameterRepetitions)])
	}
	if got[string(hmenum.ParameterInterval)] != "1S" {
		t.Errorf("INTERVAL=%v", got[string(hmenum.ParameterInterval)])
	}
}

func TestWriteEmitsAllFieldsThenCommits(t *testing.T) {
	w := &stubWriter{}
	d := New("HmIP-SDV:1", w)
	align := AlignCenter
	fg := "RED"
	err := d.Write(context.Background(), Row{
		ID:        1,
		Text:      "Hello",
		Icon:      "clock",
		Alignment: &align,
		TextColor: &fg,
	}, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatal(err)
	}
	// Final call must be COMMIT.
	got := w.params()
	if got[len(got)-1] != hmenum.ParameterDisplayDataCommit {
		t.Fatalf("last call=%s", got[len(got)-1])
	}
	// Parameters sent (any order between; commit last).
	seen := make(map[hmenum.Parameter]bool, len(got))
	for _, p := range got {
		seen[p] = true
	}
	for _, want := range []hmenum.Parameter{
		hmenum.ParameterDisplayDataID,
		hmenum.ParameterDisplayDataString,
		hmenum.ParameterDisplayDataIcon,
		hmenum.ParameterDisplayDataAlignment,
		hmenum.ParameterDisplayDataTextColor,
	} {
		if !seen[want] {
			t.Errorf("missing parameter %s", want)
		}
	}
}

func TestWriteRejectsInvalidRow(t *testing.T) {
	d := New("x", &stubWriter{})
	err := d.Write(context.Background(), Row{ID: 0}, hmenum.CommandPriorityHigh)
	if !errors.Is(err, ErrInvalidRow) {
		t.Fatalf("got %v, want ErrInvalidRow", err)
	}
}

func TestClearCommitsEmptyRow(t *testing.T) {
	w := &stubWriter{}
	d := New("x", w)
	if err := d.Clear(context.Background(), 2, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	// Expect ID=2 and a final commit. Empty string skips STRING write.
	params := w.params()
	if params[0] != hmenum.ParameterDisplayDataID || params[len(params)-1] != hmenum.ParameterDisplayDataCommit {
		t.Fatalf("params=%v", params)
	}
}

// display_id max=5 validation.
func TestWriteRejectsDisplayIDAboveMax(t *testing.T) {
	d := New("x", &stubWriter{})
	// ID=6 is above maxDisplayID=5 — must be rejected.
	err := d.Write(context.Background(), Row{ID: 6}, hmenum.CommandPriorityHigh)
	if !errors.Is(err, ErrInvalidRow) {
		t.Fatalf("got %v, want ErrInvalidRow for ID=6", err)
	}
}

func TestWriteRowsRejectsDisplayIDAboveMax(t *testing.T) {
	d := New("x", &stubWriter{})
	err := d.WriteRows(context.Background(), []Row{{ID: 1}, {ID: 6}}, hmenum.CommandPriorityHigh)
	if !errors.Is(err, ErrInvalidRow) {
		t.Fatalf("got %v, want ErrInvalidRow for ID=6", err)
	}
}

func TestWriteWithSoundRejectsDisplayIDAboveMax(t *testing.T) {
	d := New("x", &putWriter{})
	err := d.WriteWithSound(context.Background(), Row{ID: 6}, SoundOptions{}, hmenum.CommandPriorityHigh)
	if !errors.Is(err, ErrInvalidRow) {
		t.Fatalf("got %v, want ErrInvalidRow for ID=6", err)
	}
}

// repetitions validation against available list.
func TestWriteWithSoundRejectsUnknownRepetitions(t *testing.T) {
	w := &putWriter{}
	d := New("x", w)
	d.SetAvailableRepetitions([]string{"NO_REPETITION", "REPETITIONS_001", "INFINITE_REPETITIONS"})
	err := d.WriteWithSound(
		context.Background(),
		Row{ID: 1, Text: "test"},
		SoundOptions{Sound: "SOUND_SHORT", Repetitions: "INVALID_VALUE"},
		hmenum.CommandPriorityHigh,
	)
	if !errors.Is(err, ErrInvalidRepetitions) {
		t.Fatalf("got %v, want ErrInvalidRepetitions", err)
	}
}

func TestWriteWithSoundAcceptsValidRepetitions(t *testing.T) {
	w := &putWriter{}
	d := New("x", w)
	d.SetAvailableRepetitions([]string{"NO_REPETITION", "REPETITIONS_001", "INFINITE_REPETITIONS"})
	if err := d.WriteWithSound(
		context.Background(),
		Row{ID: 1, Text: "test"},
		SoundOptions{Sound: "SOUND_SHORT", Repetitions: "REPETITIONS_001"},
		hmenum.CommandPriorityHigh,
	); err != nil {
		t.Fatalf("valid repetitions rejected: %v", err)
	}
}

func TestWriteWithSoundSkipsRepetitionsValidationWhenListEmpty(t *testing.T) {
	w := &putWriter{}
	d := New("x", w) // availableRepetitions not set — validation skipped
	if err := d.WriteWithSound(
		context.Background(),
		Row{ID: 1, Text: "test"},
		SoundOptions{Sound: "SOUND_SHORT", Repetitions: "ANYTHING_GOES"},
		hmenum.CommandPriorityHigh,
	); err != nil {
		t.Fatalf("should accept any repetitions when list not set: %v", err)
	}
}

// ─── L11: IsRefreshed / SubDataPointKeys ─────────────────────────────────────

// TestTextDisplayIsRefreshedFalseWithoutBurstLimitWarning verifies that
// IsRefreshed returns false for a write-only TextDisplay with no observable
// sub-DPs.
func TestTextDisplayIsRefreshedFalseWithoutBurstLimitWarning(t *testing.T) {
	t.Parallel()
	d := New("VCU0001:3", nil)
	if d.IsRefreshed() {
		t.Fatal("IsRefreshed() must be false when no sub-DPs are present")
	}
}

// TestTextDisplaySubDataPointKeysEmptyWithoutBurstLimitWarning verifies that
// SubDataPointKeys returns an empty (not nil) slice when no sensor DPs exist.
func TestTextDisplaySubDataPointKeysEmptyWithoutBurstLimitWarning(t *testing.T) {
	t.Parallel()
	d := New("VCU0001:3", nil)
	keys := d.SubDataPointKeys()
	if len(keys) != 0 {
		t.Fatalf("SubDataPointKeys() = %v, want empty", keys)
	}
}

// ─── L12: HADiscoveryPayload max=24 ──────────────────────────────────────────

// TestTextDisplayHADiscoveryPayloadMaxIs24 verifies that the HA Discovery payload
// advertises max=24 (matching MaxRowLength), not the old value of 64.
func TestTextDisplayHADiscoveryPayloadMaxIs24(t *testing.T) {
	t.Parallel()
	d := New("VCU0001:3", nil)
	_, body := d.HADiscoveryPayload(discoveryCtx{})
	maxVal, ok := body["max"]
	if !ok {
		t.Fatal("HA Discovery payload must contain 'max'")
	}
	if maxVal != MaxRowLength {
		t.Fatalf("HA Discovery payload max=%v, want %d", maxVal, MaxRowLength)
	}
}

// --- AvailableAlignments / AvailableBackgroundColors / AvailableTextColors ---

func TestAvailableAlignmentsRoundTrip(t *testing.T) {
	t.Parallel()
	d := New("addr", &stubWriter{})
	want := []string{"LEFT", "CENTER", "RIGHT"}
	d.SetAvailableAlignments(want)
	got := d.AvailableAlignments()
	if len(got) != len(want) {
		t.Fatalf("AvailableAlignments len=%d, want %d", len(got), len(want))
	}
	for i, v := range got {
		if v != want[i] {
			t.Errorf("AvailableAlignments[%d]=%q, want %q", i, v, want[i])
		}
	}
}

func TestAvailableBackgroundColorsRoundTrip(t *testing.T) {
	t.Parallel()
	d := New("addr", &stubWriter{})
	want := []string{"BLACK", "WHITE", "RED"}
	d.SetAvailableBackgroundColors(want)
	got := d.AvailableBackgroundColors()
	if len(got) != len(want) {
		t.Fatalf("AvailableBackgroundColors len=%d, want %d", len(got), len(want))
	}
}

func TestAvailableTextColorsRoundTrip(t *testing.T) {
	t.Parallel()
	d := New("addr", &stubWriter{})
	want := []string{"RED", "GREEN", "BLUE"}
	d.SetAvailableTextColors(want)
	got := d.AvailableTextColors()
	if len(got) != len(want) {
		t.Fatalf("AvailableTextColors len=%d, want %d", len(got), len(want))
	}
}

func TestAvailableColorsNilSafety(t *testing.T) {
	t.Parallel()
	var d *TextDisplay
	if d.AvailableAlignments() != nil {
		t.Fatal("nil TextDisplay.AvailableAlignments must return nil")
	}
	if d.AvailableBackgroundColors() != nil {
		t.Fatal("nil TextDisplay.AvailableBackgroundColors must return nil")
	}
	if d.AvailableTextColors() != nil {
		t.Fatal("nil TextDisplay.AvailableTextColors must return nil")
	}
}

// TestAvailableAlignmentsEmptySliceIsNoOp verifies SetAvailableAlignments is a
// no-op when passed an empty slice.
func TestAvailableAlignmentsEmptySliceIsNoOp(t *testing.T) {
	t.Parallel()
	d := New("addr", &stubWriter{})
	d.SetAvailableAlignments([]string{})
	if d.AvailableAlignments() != nil {
		t.Fatal("SetAvailableAlignments with empty slice must not change nil internal slice")
	}
}

// --- Icon / Sound validation ---

// TestWriteRejectsUnknownIcon verifies that Write returns ErrInvalidIcon when
// the icon is not in the available list.
func TestWriteRejectsUnknownIcon(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	d := New("addr", w)
	d.SetAvailableIcons([]string{"OFF", "ON"})

	err := d.Write(context.Background(), Row{ID: 1, Icon: "GHOST"}, 0)
	if !errors.Is(err, ErrInvalidIcon) {
		t.Fatalf("Write with unknown icon: got %v, want ErrInvalidIcon", err)
	}
}

// TestWriteAcceptsKnownIcon verifies that Write succeeds when the icon is
// in the available list.
func TestWriteAcceptsKnownIcon(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	d := New("addr", w)
	d.SetAvailableIcons([]string{"OFF", "ON", "OK"})

	if err := d.Write(context.Background(), Row{ID: 1, Icon: "OK"}, 0); err != nil {
		t.Fatalf("Write with known icon: %v", err)
	}
}

// TestWriteAcceptsEmptyIcon verifies that an empty Icon is always accepted
// (means "leave unchanged").
func TestWriteAcceptsEmptyIcon(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	d := New("addr", w)
	d.SetAvailableIcons([]string{"OFF"})

	if err := d.Write(context.Background(), Row{ID: 1, Icon: ""}, 0); err != nil {
		t.Fatalf("Write with empty icon: %v", err)
	}
}

// TestWriteWithSoundRejectsUnknownSound verifies that WriteWithSound returns
// ErrInvalidSound when the sound is not in the available list.
func TestWriteWithSoundRejectsUnknownSound(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	d := New("addr", w)
	d.SetAvailableSounds([]string{"SOUND_OFF", "LONG"})

	err := d.WriteWithSound(context.Background(), Row{ID: 1}, SoundOptions{Sound: "UNKNOWN_SOUND"}, 0)
	if !errors.Is(err, ErrInvalidSound) {
		t.Fatalf("WriteWithSound with unknown sound: got %v, want ErrInvalidSound", err)
	}
}

// TestWriteWithSoundAcceptsKnownSound verifies that WriteWithSound succeeds
// when the sound label is in the available list.
func TestWriteWithSoundAcceptsKnownSound(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	d := New("addr", w)
	d.SetAvailableSounds([]string{"SOUND_OFF", "LONG"})

	if err := d.WriteWithSound(context.Background(), Row{ID: 1}, SoundOptions{Sound: "LONG"}, 0); err != nil {
		t.Fatalf("WriteWithSound with known sound: %v", err)
	}
}

// TestWriteWithSoundAcceptsEmptySound verifies that an empty Sound is always
// accepted.
func TestWriteWithSoundAcceptsEmptySound(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	d := New("addr", w)
	d.SetAvailableSounds([]string{"SOUND_OFF"})

	if err := d.WriteWithSound(context.Background(), Row{ID: 1}, SoundOptions{Sound: ""}, 0); err != nil {
		t.Fatalf("WriteWithSound with empty sound: %v", err)
	}
}

// TestWriteIconValidationSkippedWhenNilList verifies that icon validation is
// skipped when the available icons list has not been set (nil).
func TestWriteIconValidationSkippedWhenNilList(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	// New() populates the default icons list, so create manually.
	d := &TextDisplay{
		Address:        "addr",
		Writer:         w,
		availableIcons: nil, // no list → skip validation
	}
	if err := d.Write(context.Background(), Row{ID: 1, Icon: "ANY_ICON"}, 0); err != nil {
		t.Fatalf("Write must not validate icon when list is nil: %v", err)
	}
}

// --- Available list accessors ---

func TestTextDisplayAvailableRepetitionsEmptyByDefault(t *testing.T) {
	t.Parallel()

	d := New("x", &stubWriter{})
	if reps := d.AvailableRepetitions(); reps != nil {
		t.Errorf("AvailableRepetitions() before set = %v, want nil", reps)
	}
}

func TestTextDisplayAvailableRepetitionsAfterSet(t *testing.T) {
	t.Parallel()

	d := New("x", &stubWriter{})
	reps := []string{"NO_REPETITION", "INFINITE_REPETITIONS"}
	d.SetAvailableRepetitions(reps)
	got := d.AvailableRepetitions()
	if len(got) != len(reps) {
		t.Fatalf("AvailableRepetitions() = %v, want %v", got, reps)
	}
	for i, v := range got {
		if v != reps[i] {
			t.Errorf("AvailableRepetitions()[%d] = %q, want %q", i, v, reps[i])
		}
	}
}

func TestTextDisplayAvailableRepetitionsNilReceiverReturnsNil(t *testing.T) {
	t.Parallel()

	var d *TextDisplay
	if reps := d.AvailableRepetitions(); reps != nil {
		t.Errorf("nil TextDisplay.AvailableRepetitions() = %v, want nil", reps)
	}
}

func TestTextDisplaySetAvailableIconsEmptySliceIsNoOp(t *testing.T) {
	t.Parallel()

	d := New("x", &stubWriter{})
	origIcons := d.AvailableIcons()
	d.SetAvailableIcons(nil)
	after := d.AvailableIcons()
	if len(after) != len(origIcons) {
		t.Errorf("SetAvailableIcons(nil) changed icon count from %d to %d", len(origIcons), len(after))
	}
}

func TestTextDisplaySetAvailableSoundsEmptySliceIsNoOp(t *testing.T) {
	t.Parallel()

	d := New("x", &stubWriter{})
	origSounds := d.AvailableSounds()
	d.SetAvailableSounds(nil)
	after := d.AvailableSounds()
	if len(after) != len(origSounds) {
		t.Errorf("SetAvailableSounds(nil) changed sound count from %d to %d", len(origSounds), len(after))
	}
}

func TestTextDisplayAvailableIconsNilReceiverReturnsNil(t *testing.T) {
	t.Parallel()

	var d *TextDisplay
	if icons := d.AvailableIcons(); icons != nil {
		t.Errorf("nil TextDisplay.AvailableIcons() = %v, want nil", icons)
	}
}

func TestTextDisplayAvailableSoundsNilReceiverReturnsNil(t *testing.T) {
	t.Parallel()

	var d *TextDisplay
	if sounds := d.AvailableSounds(); sounds != nil {
		t.Errorf("nil TextDisplay.AvailableSounds() = %v, want nil", sounds)
	}
}

// --- Service registration ---

func TestTextDisplayServiceWrite(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	d := New("HmIP-WRCD:3", w)
	params := map[string]any{
		"id":   int32(2),
		"text": "Hello",
		"icon": "INFO",
	}
	if err := d.Invoke(context.Background(), "write", params, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("write service: %v", err)
	}
	ps := w.params()
	if len(ps) == 0 {
		t.Fatal("write service must send at least one parameter")
	}
	seen := make(map[hmenum.Parameter]bool)
	for _, p := range ps {
		seen[p] = true
	}
	if !seen[hmenum.ParameterDisplayDataID] {
		t.Error("write service must write DISPLAY_DATA_ID")
	}
	if !seen[hmenum.ParameterDisplayDataCommit] {
		t.Error("write service must write DISPLAY_DATA_COMMIT")
	}
}

func TestTextDisplayServiceWriteMissingIDReturnsError(t *testing.T) {
	t.Parallel()

	d := New("x", &stubWriter{})
	if err := d.Invoke(context.Background(), "write", map[string]any{"text": "hi"}, hmenum.CommandPriorityHigh); err == nil {
		t.Error("write service with missing id must return error")
	}
}

func TestTextDisplayServiceWriteWithColor(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	d := New("HmIP-WRCD:3", w)
	params := map[string]any{
		"id":    int32(1),
		"text":  "Colored",
		"color": "RED",
	}
	if err := d.Invoke(context.Background(), "write", params, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("write with color service: %v", err)
	}
	ps := w.params()
	seen := make(map[hmenum.Parameter]bool)
	for _, p := range ps {
		seen[p] = true
	}
	if !seen[hmenum.ParameterDisplayDataTextColor] {
		t.Error("write service must write TEXT_COLOR when color param present")
	}
}

func TestTextDisplayServiceWriteWithSound(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	d := New("HmIP-WRCD:3", w)
	params := map[string]any{
		"id":          int32(1),
		"text":        "Sound",
		"sound":       "SOUND_SHORT",
		"repetitions": "REPETITIONS_3",
		"interval":    "500MS",
	}
	if err := d.Invoke(context.Background(), "write_with_sound", params, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("write_with_sound service: %v", err)
	}
	ps := w.params()
	seen := make(map[hmenum.Parameter]bool)
	for _, p := range ps {
		seen[p] = true
	}
	for _, want := range []hmenum.Parameter{
		hmenum.ParameterAcousticNotificationSelection,
		hmenum.ParameterRepetitions,
		hmenum.ParameterInterval,
		hmenum.ParameterDisplayDataCommit,
	} {
		if !seen[want] {
			t.Errorf("write_with_sound service: param %q missing", want)
		}
	}
}

func TestTextDisplayServiceWriteWithSoundMissingIDReturnsError(t *testing.T) {
	t.Parallel()

	d := New("x", &stubWriter{})
	if err := d.Invoke(context.Background(), "write_with_sound", map[string]any{}, hmenum.CommandPriorityHigh); err == nil {
		t.Error("write_with_sound with missing id must return error")
	}
}

// --- WriteRows edge cases ---

func TestTextDisplayWriteRowsEmptyNoOp(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	d := New("x", w)
	if err := d.WriteRows(context.Background(), nil, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("WriteRows(nil) must succeed: %v", err)
	}
	if len(w.calls) != 0 {
		t.Errorf("WriteRows(nil) must write nothing, got %d calls", len(w.calls))
	}
}

func TestTextDisplayWriteRowsZeroIDReturnsError(t *testing.T) {
	t.Parallel()

	d := New("x", &stubWriter{})
	err := d.WriteRows(context.Background(), []Row{{ID: 0}}, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Error("WriteRows with ID=0 must return ErrInvalidRow")
	}
}
