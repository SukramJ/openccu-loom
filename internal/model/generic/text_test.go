// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package generic

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ---------------------------------------------------------------------------
// text.go — NewText, Set, textMaxLen
// ---------------------------------------------------------------------------

func TestText_Set_NotWritable(t *testing.T) {
	t.Parallel()
	t1 := NewText(baseCfg(hmenum.ParameterDisplayDataString, hmenum.ParameterTypeString, hmenum.OperationsRead))
	if err := t1.Set(context.Background(), "hello", hmenum.CommandPriorityHigh); !errors.Is(err, ErrNotWritable) {
		t.Errorf("expected ErrNotWritable, got %v", err)
	}
}

func TestTextMaxLen_String(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`"15"`)
	if got := textMaxLen(raw); got != 15 {
		t.Errorf("expected 15, got %d", got)
	}
}

func TestTextMaxLen_NonNumericString(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`"abc"`)
	if got := textMaxLen(raw); got != 0 {
		t.Errorf("expected 0 for non-numeric string, got %d", got)
	}
}

func TestTextMaxLen(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   json.RawMessage
		want int
	}{
		{nil, 0},
		{json.RawMessage(`""`), 0},
		{json.RawMessage(`0`), 0},
		{json.RawMessage(`64`), 64},
		{json.RawMessage(`"128"`), 128},
		{json.RawMessage(`"bad"`), 0},
		{json.RawMessage(`"12bad"`), 0},
	}
	for _, c := range cases {
		got := textMaxLen(c.in)
		if got != c.want {
			t.Errorf("textMaxLen(%s) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestTextSetExceedsMaxLen(t *testing.T) {
	t.Parallel()
	cfg := baseCfg(hmenum.ParameterDisplayDataString, hmenum.ParameterTypeString, hmenum.OperationsWrite|hmenum.OperationsRead)
	cfg.Descriptor.Max = json.RawMessage(`5`)
	cfg.Writer = &stubWriter{}
	tx := NewText(cfg)
	// Exceeds max — logs a warning but does NOT error.
	if err := tx.Set(context.Background(), "toolong", hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("Set with overlong value must not return error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// text.go — Invoke (set_text registered service)
// ---------------------------------------------------------------------------

func TestText_Invoke_SetText(t *testing.T) {
	t.Parallel()
	cfg := baseCfg(hmenum.ParameterDisplayDataString, hmenum.ParameterTypeString,
		hmenum.OperationsRead|hmenum.OperationsWrite)
	t1 := NewText(cfg)
	w := &stubWriter{}
	t1.Writer = w
	if err := t1.Invoke(context.Background(), "set_text", map[string]any{"text": "hello"}, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("set_text: %v", err)
	}
}

func TestText_Invoke_SetText_MissingParam(t *testing.T) {
	t.Parallel()
	cfg := baseCfg(hmenum.ParameterDisplayDataString, hmenum.ParameterTypeString,
		hmenum.OperationsRead|hmenum.OperationsWrite)
	t1 := NewText(cfg)
	w := &stubWriter{}
	t1.Writer = w
	if err := t1.Invoke(context.Background(), "set_text", map[string]any{}, hmenum.CommandPriorityHigh); err == nil {
		t.Error("missing 'text' param: expected error")
	}
}
