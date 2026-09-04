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

// TestTextSetPassesEveryFirmwareMaxShapeThrough pins that MAX is not read
// as a length on a STRING parameter, because on no CCU is it one.
//
// The MAX values below are the shapes the firmware actually publishes, not
// invented ones. BidCos and wired publish the empty string and check only
// the value's type (libhsscomm HSSLogicalTypeString::GetDescription sets
// MIN and MAX to "", ::EnforceConstraints returns
// `val->getType()==TypeString`). HmIP publishes a character class or a
// maximum lexical value, forwarded verbatim into the description
// (HMIPServer de.eq3.cbcs.legacy.communicator.DeviceUtil#addDesriptionOfParameter).
//
// The four tests this replaces drove MAX values of 15, 64 and "128" — a
// length, which is the reading under test. A fixture that supplies the
// convention it is meant to confirm cannot refute it.
func TestTextSetPassesEveryFirmwareMaxShapeThrough(t *testing.T) {
	t.Parallel()

	// Far longer than any budget any of these shapes could imply.
	const long = "Wohnzimmer Deckenlampe Sued mit einem sehr langen Namen 0123456789"

	for _, max := range []string{
		`""`,                 // BidCos / wired, unconditionally
		`"[0x20-0x7E]{16}"`,  // HmIP text
		`"[U+00-U+FF]{16}"`,  // HmIP unicode text
		`"[0x00-0x7E]{127}"`, // HmIP update-server URL
		`"[0-9]{8}"`,         // HmIP numeric user PIN
		`"2255_12_31 23:55"`, // HmIP maximum lexical value
	} {
		t.Run(max, func(t *testing.T) {
			t.Parallel()
			cfg := baseCfg(hmenum.ParameterDisplayDataString, hmenum.ParameterTypeString,
				hmenum.OperationsWrite|hmenum.OperationsRead)
			cfg.Descriptor.Max = json.RawMessage(max)
			w := &stubWriter{}
			cfg.Writer = w
			tx := NewText(cfg)

			if err := tx.Set(context.Background(), long, hmenum.CommandPriorityHigh); err != nil {
				t.Fatalf("Set: %v", err)
			}
			call, ok := w.last()
			if !ok {
				t.Fatal("nothing reached the writer")
			}
			if got, _ := call.value.(string); got != long {
				t.Errorf("the writer received %q, want the value unchanged", got)
			}
		})
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
