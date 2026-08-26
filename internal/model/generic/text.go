// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package generic

import (
	"context"
	"encoding/json"
	"log/slog"

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

// Set sends v after validating writability and the descriptor's MAX length
// when present.
func (t *Text) Set(ctx context.Context, v string, priority hmenum.CommandPriority) error {
	if !t.IsWritable() {
		return ErrNotWritable
	}
	if maxLen := textMaxLen(t.Descriptor.Max); maxLen > 0 && len(v) > maxLen {
		slog.Warn(
			"generic.Text: value exceeds descriptor MAX length",
			"address", t.DataPointKey().ChannelAddress,
			"parameter", t.DataPointKey().Parameter,
			"max_len", maxLen,
			"actual_len", len(v),
		)
	}
	return t.sendAndObserve(ctx, v, v, priority)
}

// textMaxLen extracts the MAX length from a Text-descriptor's Max
// JSON-RawMessage. Returns 0 when the field is empty or unparseable.
func textMaxLen(raw json.RawMessage) int {
	if len(raw) == 0 {
		return 0
	}
	var n int
	if err := json.Unmarshal(raw, &n); err == nil && n > 0 {
		return n
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		var parsed int
		// best-effort numeric parse
		for _, r := range s {
			if r < '0' || r > '9' {
				return 0
			}
			parsed = parsed*10 + int(r-'0')
		}
		return parsed
	}
	return 0
}
