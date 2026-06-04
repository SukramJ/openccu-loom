// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package textdisplay

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// --- TextDisplay deep tests ---

// TestTextDisplayAddressReturnsChannelAddress verifies that Address is set
// from the address supplied at construction time.
func TestTextDisplayAddressReturnsChannelAddress(t *testing.T) {
	t.Parallel()

	const addr = "HmIP-SDV:3"
	d := New(addr, &stubWriter{})
	if d.Address != addr {
		t.Errorf("Address = %q, want %q", d.Address, addr)
	}
}

// TestTextDisplaySetTextWritesTextAndCommits verifies that Write with a
// text field causes both DISPLAY_DATA_STRING and DISPLAY_DATA_COMMIT to be
// written (sequential path via plain Writer).
func TestTextDisplaySetTextWritesTextAndCommits(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	d := New("HmIP-SDV:3", w)
	if err := d.Write(context.Background(), Row{ID: 1, Text: "hello"}, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	params := w.params()
	if len(params) == 0 {
		t.Fatal("no parameters written")
	}
	// Last write must be COMMIT.
	if params[len(params)-1] != hmenum.ParameterDisplayDataCommit {
		t.Errorf("last param = %q, want DISPLAY_DATA_COMMIT", params[len(params)-1])
	}
	// String must be present somewhere before commit.
	found := false
	for _, p := range params {
		if p == hmenum.ParameterDisplayDataString {
			found = true
		}
	}
	if !found {
		t.Error("DISPLAY_DATA_STRING must be written when Text is non-empty")
	}
}

// TestTextDisplayClearWritesEmptyAndCommits verifies that Clear() writes
// DISPLAY_DATA_ID, DISPLAY_DATA_STRING (empty), and DISPLAY_DATA_COMMIT.
// STRING is always written — even empty — so the CCU actually clears the row.
func TestTextDisplayClearWritesEmptyAndCommits(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	d := New("HmIP-SDV:3", w)
	if err := d.Clear(context.Background(), 2, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	params := w.params()
	// Must contain at least ID, STRING, and COMMIT.
	if len(params) < 3 {
		t.Fatalf("expected at least 3 params (ID+STRING+COMMIT), got %d: %v", len(params), params)
	}
	if params[0] != hmenum.ParameterDisplayDataID {
		t.Errorf("first param = %q, want DISPLAY_DATA_ID", params[0])
	}
	if params[len(params)-1] != hmenum.ParameterDisplayDataCommit {
		t.Errorf("last param = %q, want DISPLAY_DATA_COMMIT", params[len(params)-1])
	}
	// STRING must appear (empty string clears the row on the CCU).
	found := slices.Contains(params, hmenum.ParameterDisplayDataString)
	if !found {
		t.Error("DISPLAY_DATA_STRING must be written by Clear() to clear the row on the CCU")
	}
}

// TestTextDisplayInvalidRowIDRejected verifies that a row ID < 1 returns
// ErrInvalidRow.
func TestTextDisplayInvalidRowIDRejected(t *testing.T) {
	t.Parallel()

	d := New("x", &stubWriter{})
	for _, id := range []int32{0, -1, -100} {
		err := d.Write(context.Background(), Row{ID: id, Text: "bad"}, hmenum.CommandPriorityHigh)
		if !errors.Is(err, ErrInvalidRow) {
			t.Errorf("Write(Row{ID=%d}) = %v, want ErrInvalidRow", id, err)
		}
	}
}

// TestTextDisplayWriteRowsMultipleRows verifies that WriteRows commits only
// once after writing all rows.
func TestTextDisplayWriteRowsMultipleRows(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	d := New("HmIP-SDV:3", w)
	rows := []Row{
		{ID: 1, Text: "row1"},
		{ID: 2, Text: "row2"},
		{ID: 3, Text: "row3"},
	}
	if err := d.WriteRows(context.Background(), rows, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	params := w.params()
	// Exactly one COMMIT at the end.
	commitCount := 0
	for _, p := range params {
		if p == hmenum.ParameterDisplayDataCommit {
			commitCount++
		}
	}
	if commitCount != 1 {
		t.Errorf("WriteRows(%d rows) committed %d times, want 1", len(rows), commitCount)
	}
	if params[len(params)-1] != hmenum.ParameterDisplayDataCommit {
		t.Errorf("last param = %q, want DISPLAY_DATA_COMMIT", params[len(params)-1])
	}
}

// TestTextDisplayWriteWithSoundIncludesSoundParams verifies that
// WriteWithSound forwards sound/repetitions/interval in the sequential
// fallback path (plain Writer without PutParamset).
func TestTextDisplayWriteWithSoundIncludesSoundParams(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	d := New("HmIP-SDV:3", w)
	err := d.WriteWithSound(
		context.Background(),
		Row{ID: 1, Text: "Alert"},
		SoundOptions{Sound: "SOUND_SHORT", Repetitions: "REPETITIONS_3", Interval: "500MS"},
		hmenum.CommandPriorityHigh,
	)
	if err != nil {
		t.Fatal(err)
	}
	params := w.params()
	seen := make(map[hmenum.Parameter]bool, len(params))
	for _, p := range params {
		seen[p] = true
	}
	for _, want := range []hmenum.Parameter{
		hmenum.ParameterAcousticNotificationSelection,
		hmenum.ParameterRepetitions,
		hmenum.ParameterInterval,
		hmenum.ParameterDisplayDataCommit,
	} {
		if !seen[want] {
			t.Errorf("WriteWithSound: param %q missing from sequential write", want)
		}
	}
}

// TestTextDisplayCommitAloneWritesCommit verifies that Commit() on its own
// writes only DISPLAY_DATA_COMMIT.
func TestTextDisplayCommitAloneWritesCommit(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	d := New("HmIP-SDV:3", w)
	if err := d.Commit(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	params := w.params()
	if len(params) != 1 || params[0] != hmenum.ParameterDisplayDataCommit {
		t.Errorf("Commit() params = %v, want [DISPLAY_DATA_COMMIT]", params)
	}
}

// --- Topology ---

func TestTextDisplayHAComponent(t *testing.T) {
	t.Parallel()

	d := New("HmIP-WRCD:3", &stubWriter{})
	if got := d.HAComponent(); got != "text" {
		t.Errorf("TextDisplay.HAComponent() = %q, want text", got)
	}
}

func TestTextDisplayTopicSlotWithChannelAddress(t *testing.T) {
	t.Parallel()

	d := New("HmIP-WRCD:3", &stubWriter{})
	slot := d.TopicSlot()
	if slot.Parameter != "text_display" {
		t.Errorf("TopicSlot.Parameter = %q, want text_display", slot.Parameter)
	}
	if slot.Channel != 3 {
		t.Errorf("TopicSlot.Channel = %d, want 3", slot.Channel)
	}
}

func TestTextDisplayTopicSlotFallbackOnInvalidAddress(t *testing.T) {
	t.Parallel()

	d := New("NOCORON", &stubWriter{})
	slot := d.TopicSlot()
	if slot.Address != "NOCORON" {
		t.Errorf("TopicSlot fallback address = %q, want NOCORON", slot.Address)
	}
}
