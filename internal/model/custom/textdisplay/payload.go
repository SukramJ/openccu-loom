// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package textdisplay

import (
	"context"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// Compile-time guarantee that *TextDisplay satisfies the universal
// Source contract. ADR-0007 step 5.
//
// It does NOT implement [payload.HADiscoveryEntityBuilder], and it is the
// only custom data point that does not. A text display has no Home
// Assistant entity of its own to describe: the reference stack's notify.py
// spawns one HmipTextDisplayNotifyEntity per CustomDpTextDisplay and
// registers no `text` entity, so the channel aggregate suppresses this
// source and the bridge publishes the notify companion instead
// (DefaultDiscoveryBuilder.BuildTextDisplayNotify). There is nothing for a
// self-description to reach: [TextDisplay.State] publishes the device's
// static capability lists and no current text at all — the display is
// write-only — so a `text` entity rendered from this source would read a
// key no topic carries and stand permanently blank.
var _ payload.Source = (*TextDisplay)(nil)

// Info returns identity-level fields for a TextDisplay.
func (t *TextDisplay) Info() payload.InfoPayload {
	if t == nil {
		return nil
	}
	return &payload.TextDisplayInfo{
		Address:  t.Address,
		Key:      t.key.String(),
		Category: "text_display",
	}
}

// Config returns the text display static configuration.
// The display is write-only — there are no runtime-variable capabilities.
func (t *TextDisplay) Config() payload.ConfigPayload {
	if t == nil {
		return nil
	}
	return &payload.TextDisplayConfig{
		WriteOnly: true,
	}
}

// State returns the live text display state. The display's
// DISPLAY_DATA_* parameters are write-only (OPERATIONS 2), so no state is
// readable back from the device.
// Per-device availability rides on its own MQTT topic
// (eventbridge.markAvailability), not on the state JSON.
//
// The available_* lists are static device-capability VALUE_LISTs included
// so that consumers (Home Assistant automations, the notify entity's
// per-option pickers) know which values the device accepts without a
// separate capability query. Mirrors the `_hm_payload_state` property.
// (platforms/text_display.py: `state["available_icons"]` /
// `state["available_sounds"]`).
func (t *TextDisplay) State() payload.StatePayload {
	if t == nil {
		return nil
	}
	return &payload.TextDisplayState{
		AvailableIcons:            stringsToAny(t.AvailableIcons()),
		AvailableSounds:           stringsToAny(t.AvailableSounds()),
		AvailableBackgroundColors: stringsToAny(t.AvailableBackgroundColors()),
		AvailableTextColors:       stringsToAny(t.AvailableTextColors()),
		AvailableAlignments:       stringsToAny(t.AvailableAlignments()),
		AvailableRepetitions:      stringsToAny(t.AvailableRepetitions()),
		AvailableIntervals:        stringsToAny(t.AvailableIntervals()),
	}
}

// stringsToAny converts a label list into the []any the state payload
// carries, returning nil for an empty list so the `omitempty` field is
// dropped from the wire shape.
func stringsToAny(in []string) []any {
	if len(in) == 0 {
		return nil
	}
	out := make([]any, len(in))
	for i, v := range in {
		out[i] = v
	}
	return out
}

// registerTextDisplayServices wires the text display write operations
// onto the embedded ServiceRegistry. Service-method names mirror
// (write, write_with_sound).
func (t *TextDisplay) registerTextDisplayServices() {
	t.RegisterService("write", func(ctx context.Context, params map[string]any, priority hmenum.CommandPriority) error {
		id, err := payload.ParamInt32(params, "id")
		if err != nil {
			return err
		}
		r := Row{ID: id}
		if s, err := payload.ParamString(params, "text"); err == nil {
			r.Text = s
		}
		if s, err := payload.ParamString(params, "icon"); err == nil {
			r.Icon = s
		}
		// "color" accepts a string label (e.g. "RED") forwarded as
		// text_color.
		if s, err := payload.ParamString(params, "color"); err == nil {
			r.TextColor = &s
		}
		if s, err := payload.ParamString(params, "text_color"); err == nil {
			r.TextColor = &s
		}
		if s, err := payload.ParamString(params, "background_color"); err == nil {
			r.BackgroundColor = &s
		}
		if s, err := payload.ParamString(params, "alignment"); err == nil {
			r.Alignment = &s
		}
		return t.Write(ctx, r, priority)
	})
	t.RegisterService("write_with_sound", func(ctx context.Context, params map[string]any, priority hmenum.CommandPriority) error {
		id, err := payload.ParamInt32(params, "id")
		if err != nil {
			return err
		}
		r := Row{ID: id}
		if s, err := payload.ParamString(params, "text"); err == nil {
			r.Text = s
		}
		if s, err := payload.ParamString(params, "icon"); err == nil {
			r.Icon = s
		}
		opts := SoundOptions{}
		if s, err := payload.ParamString(params, "sound"); err == nil {
			opts.Sound = s
		}
		// "repetitions" accepts a string label directly (e.g.
		// "REPETITIONS_003") or a numeric count via "repeat" which is
		// converted to the CCU label string.
		if s, err := payload.ParamString(params, "repetitions"); err == nil {
			opts.Repetitions = s
		} else if n, err := payload.ParamInt32(params, "repeat"); err == nil {
			label, lerr := custom.RepetitionsLabel(int(n))
			if lerr != nil {
				return fmt.Errorf("textdisplay: %w", lerr)
			}
			opts.Repetitions = label
		}
		if s, err := payload.ParamString(params, "interval"); err == nil {
			opts.Interval = s
		}
		return t.WriteWithSound(ctx, r, opts, priority)
	})
}
