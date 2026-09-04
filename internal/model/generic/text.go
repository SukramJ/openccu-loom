// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package generic

import (
	"context"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// Text is a writable string data point. Unlike [ActionString] it
// tracks the CCU-confirmed value via events and uses the same
// optimistic-update bookkeeping as [Switch] / [Float].
type Text struct {
	*DataPoint[string]
}

// NewText constructs a Text.
func NewText(cfg Spec) *Text {
	t := &Text{DataPoint: NewDataPoint[string](cfg)}
	t.RegisterServiceWithArg("set_text", "text", func(ctx context.Context, params map[string]any, priority hmenum.CommandPriority) error {
		v, err := paramString(params, "text")
		if err != nil {
			return err
		}
		return t.Set(ctx, v, priority)
	})
	return t
}

// Set sends v after validating writability.
//
// The descriptor's MAX is deliberately not consulted: on a STRING
// parameter it is not a length. BidCos and wired publish it as the empty
// string and check only that the value is a string at all
// (libhsscomm HSSLogicalTypeString::GetDescription sets MIN and MAX to
// "", and ::EnforceConstraints returns `val->getType()==TypeString`).
// HmIP publishes a character class instead — the parameter factories
// pass shapes like "[0x20-0x7E]{16}" or a maximum lexical value
// ("2255_12_31 23:55") straight into the description
// (HMIPServer de.eq3.cbcs.legacy.communicator.DeviceUtil#addDesriptionOfParameter).
//
// So the code that read MAX as a length could only ever parse one of
// those to zero, which is why the branch it guarded had never fired. The
// real byte budget is the converter's own constructor argument and is
// never published; an over-long value is truncated device-side rather
// than refused. Capping here would need a number no descriptor carries.
func (t *Text) Set(ctx context.Context, v string, priority hmenum.CommandPriority) error {
	if !t.IsWritable() {
		return ErrNotWritable
	}
	return t.sendAndObserve(ctx, v, v, priority)
}
