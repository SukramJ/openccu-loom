// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package combined

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// The north-bound projections of the combined data points. Each type
// describes the data-point-specific half of its HA discovery body and
// renders its own state payload; the bridge supplies the shared frame
// (unique_id, availability, device, origin) and the topics.
//
// Collected in one file on purpose: these bodies have to stay identical
// to what the per-type discovery builders emitted before the projection
// seam existed, and a reviewer can only check that when they sit side by
// side. TestCombinedProjectionBodiesAreUnchanged pins it.
var (
	_ payload.CombinedProjection = (*Timer)(nil)
	_ payload.CombinedProjection = (*LevelCombined)(nil)
	_ payload.CombinedProjection = (*HSColor)(nil)
	_ payload.CombinedWritable   = (*Timer)(nil)
)

// Combined kinds. Each one is a retained-topic segment, so it is part of
// the wire contract: renaming one orphans every retained message
// published under the old segment.
const (
	KindDuration      = "duration"
	KindLevelCombined = "level_combined"
	KindHSColor       = "hs_color"
)

// timerMaxSeconds bounds the HA number input at a 24h window. The wire's
// INTEGER max (16343) reinterpreted at the hours unit is ~678 days, far
// past anything an operator sets by hand. SetDuration auto-promotes the
// unit, so a user-entered 100 s still writes 100 at the seconds unit
// rather than overflowing.
const timerMaxSeconds = 24 * 60 * 60

// --- Timer ---------------------------------------------------------

// CombinedKind implements [payload.CombinedProjection].
func (t *Timer) CombinedKind() string { return KindDuration }

// HACombinedDiscovery implements [payload.CombinedProjection]. The timer
// projects as an HA `number` the operator types a duration into.
func (t *Timer) HACombinedDiscovery(ctx payload.CombinedDiscoveryContext) (component string, body map[string]any) {
	if ctx == nil {
		return "", nil
	}
	return "number", map[string]any{
		"name":                t.discoveryLabel(ctx),
		"command_topic":       ctx.CombinedCommandTopic(),
		"min":                 float64(0),
		"max":                 float64(timerMaxSeconds),
		"step":                float64(1),
		"unit_of_measurement": "s",
		"entity_category":     payload.CombinedEntityCategoryConfig,
		"mode":                "box",
		"optimistic":          false,
	}
}

// discoveryLabel resolves the operator-facing entity name. The OCCU
// catalogue translates the underlying DURATION_VALUE wire parameter on
// the channel type the timer lives on ("Wert Zeitdauer" / "Duration
// Value"); the leading "Wert " is dropped so HA's entity_id derivation
// (device name + label) yields "alarmsirene_fl_zeitdauer" rather than
// the stutter "…wert_zeitdauer".
func (t *Timer) discoveryLabel(ctx payload.CombinedDiscoveryContext) string {
	if raw, ok := ctx.ParameterLabel(t.ValueParameter); ok && raw != "" {
		label := strings.TrimSpace(strings.TrimPrefix(raw, "Wert "))
		label = strings.TrimSpace(strings.TrimPrefix(label, "Value "))
		if label != "" {
			return label
		}
	}
	return ctx.Translate("discovery.duration")
}

// CombinedStatePayload implements [payload.CombinedProjection].
func (t *Timer) CombinedStatePayload() (string, bool) {
	seconds, observed := t.ValueSeconds()
	if !observed {
		return "", false
	}
	if seconds < 0 {
		seconds = 0
	}
	return formatSeconds(seconds), true
}

// OnCombinedChange implements [payload.CombinedProjection].
func (t *Timer) OnCombinedChange(fn func()) func() {
	return t.OnUpdate(func(_, _ float64) { fn() })
}

// WriteCombined implements [payload.CombinedWritable]. The payload is a
// duration in seconds; fractional values are honoured because the wire
// unit promotion works on the seconds total, not on integers.
func (t *Timer) WriteCombined(ctx context.Context, raw string, priority hmenum.CommandPriority) error {
	seconds, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return fmt.Errorf("combined timer: %q is not a duration in seconds: %w", raw, err)
	}
	if seconds < 0 {
		return fmt.Errorf("combined timer: negative duration %g", seconds)
	}
	return t.SetDuration(ctx, time.Duration(seconds*float64(time.Second)), priority)
}

// formatSeconds renders a seconds value with no trailing ".0" when the
// value is integral.
func formatSeconds(s float64) string {
	if s == float64(int64(s)) {
		return strconv.FormatInt(int64(s), 10)
	}
	return fmt.Sprintf("%g", s)
}

// --- LevelCombined -------------------------------------------------

// CombinedKind implements [payload.CombinedProjection].
func (l *LevelCombined) CombinedKind() string { return KindLevelCombined }

// HACombinedDiscovery implements [payload.CombinedProjection]. The
// blind's level+slats pair projects as a diagnostic sensor showing the
// level; the slats travel in the same JSON body for template access.
func (l *LevelCombined) HACombinedDiscovery(ctx payload.CombinedDiscoveryContext) (component string, body map[string]any) {
	if ctx == nil {
		return "", nil
	}
	return "sensor", map[string]any{
		"name":            ctx.Translate("discovery.level_combined"),
		"value_template":  "{{ value_json.level }}",
		"entity_category": payload.CombinedEntityCategoryDiagnostic,
	}
}

// CombinedStatePayload implements [payload.CombinedProjection].
func (l *LevelCombined) CombinedStatePayload() (string, bool) {
	composite, observed := l.Value()
	if !observed {
		return "", false
	}
	return EncodeLevelCompositeJSON(composite), true
}

// OnCombinedChange implements [payload.CombinedProjection].
func (l *LevelCombined) OnCombinedChange(fn func()) func() {
	return l.OnUpdate(func(_, _ LevelComposite) { fn() })
}

// EncodeLevelCompositeJSON renders a LevelComposite as the JSON body
// published to the combined state topic.
func EncodeLevelCompositeJSON(c LevelComposite) string {
	return fmt.Sprintf(`{"level":%g,"slats":%g}`, c.Level.Level(), c.SlatsLevel.Level())
}

// --- HSColor -------------------------------------------------------

// CombinedKind implements [payload.CombinedProjection].
func (c *HSColor) CombinedKind() string { return KindHSColor }

// HACombinedDiscovery implements [payload.CombinedProjection]. The
// hue/saturation pair projects as a diagnostic sensor showing the hue;
// saturation travels in the same JSON body for template access.
func (c *HSColor) HACombinedDiscovery(ctx payload.CombinedDiscoveryContext) (component string, body map[string]any) {
	if ctx == nil {
		return "", nil
	}
	return "sensor", map[string]any{
		"name":            ctx.Translate("discovery.hs_color"),
		"value_template":  "{{ value_json.hue }}",
		"entity_category": payload.CombinedEntityCategoryDiagnostic,
	}
}

// CombinedStatePayload implements [payload.CombinedProjection].
func (c *HSColor) CombinedStatePayload() (string, bool) {
	hs, observed := c.Value()
	if !observed {
		return "", false
	}
	return EncodeHSJSON(hs), true
}

// OnCombinedChange implements [payload.CombinedProjection].
func (c *HSColor) OnCombinedChange(fn func()) func() {
	return c.OnUpdate(func(_, _ HS) { fn() })
}

// EncodeHSJSON renders an HS pair as the JSON body published to the
// combined state topic.
func EncodeHSJSON(hs HS) string {
	return fmt.Sprintf(`{"hue":%d,"saturation":%g}`, hs.Hue, hs.Saturation)
}
