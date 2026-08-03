// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package alarm

import (
	"context"
	"log/slog"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// --- resolveActive ------------------------------------------------------

// TestResolveActiveEmptyRuleMatchesHistoricalBehaviour is the
// compatibility guarantee that lets active_values ship without migrating
// any existing enrollment: with no configured labels, resolveActive must
// reproduce normalizeActive exactly, value for value.
func TestResolveActiveEmptyRuleMatchesHistoricalBehaviour(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		raw        any
		wantActive bool
		wantKnown  bool
	}{
		{"bool true passes through", true, true, true},
		{"bool false passes through", false, false, true},
		{"int zero is inactive", 0, false, true},
		{"int non-zero is active", 7, true, true},
		{"int32 non-zero is active", int32(3), true, true},
		{"int64 zero is inactive", int64(0), false, true},
		{"float64 zero is inactive", 0.0, false, true},
		{"float64 non-zero is active", 1.5, true, true},
		{"nil is unknown", nil, false, false},
		{"string is unknown", "IDLE_OFF", false, false},
		{"unsupported struct type is unknown", struct{}{}, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			gotActive, gotKnown, resolved := resolveActive(activationRule{}, tc.raw, nil)
			if !resolved {
				t.Fatalf("resolved = false, want true for an unconfigured rule")
			}
			if gotActive != tc.wantActive || gotKnown != tc.wantKnown {
				t.Errorf("resolveActive(empty rule, %#v) = (%v, %v), want (%v, %v)",
					tc.raw, gotActive, gotKnown, tc.wantActive, tc.wantKnown)
			}

			// The compatibility guarantee itself: a future divergence
			// between the two functions must fail here, not surface as a
			// silent behavior change for existing enrollments.
			wantActive, wantKnown := normalizeActive(tc.raw)
			if gotActive != wantActive || gotKnown != wantKnown {
				t.Errorf("resolveActive diverges from normalizeActive for %#v: got (%v,%v), normalizeActive gives (%v,%v)",
					tc.raw, gotActive, gotKnown, wantActive, wantKnown)
			}
		})
	}
}

// TestResolveActiveSmokeDetectorAlarmStatusIndexes is the load-bearing
// case active_values exists for: SMOKE_DETECTOR_ALARM_STATUS's value list
// is [IDLE_OFF, PRIMARY_ALARM, INTRUSION_ALARM, SECONDARY_ALARM], and a
// naive "index != 0" rule would misclassify index 2.
func TestResolveActiveSmokeDetectorAlarmStatusIndexes(t *testing.T) {
	t.Parallel()

	rule := activationRule{labels: []string{"PRIMARY_ALARM", "SECONDARY_ALARM"}}
	valueList := []string{"IDLE_OFF", "PRIMARY_ALARM", "INTRUSION_ALARM", "SECONDARY_ALARM"}

	cases := []struct {
		idx  int
		want bool
	}{
		{0, false}, // IDLE_OFF: no detection.
		{1, true},  // PRIMARY_ALARM: a real smoke detection.
		// INTRUSION_ALARM is the alarm system's own siren command during
		// a burglary, not a fire — the whole reason active_values exists
		// is so this index reports inactive instead of the naive
		// "index != 0" verdict, which would count it as a smoke
		// detection and let the domain read back its own output.
		{2, false},
		{3, true}, // SECONDARY_ALARM: a real smoke detection.
	}
	for _, tc := range cases {
		t.Run(valueList[tc.idx], func(t *testing.T) {
			t.Parallel()

			active, known, resolved := resolveActive(rule, tc.idx, valueList)
			if !known || !resolved {
				t.Fatalf("known=%v resolved=%v, want true/true", known, resolved)
			}
			if active != tc.want {
				t.Errorf("index %d (%s): active = %v, want %v", tc.idx, valueList[tc.idx], active, tc.want)
			}
		})
	}
}

// TestResolveActiveStringLabelMatchesIndexVerdict verifies a value
// arriving as its string label (rather than the numeric index) reaches
// the same verdicts as TestResolveActiveSmokeDetectorAlarmStatusIndexes,
// without needing a value list at all.
func TestResolveActiveStringLabelMatchesIndexVerdict(t *testing.T) {
	t.Parallel()

	rule := activationRule{labels: []string{"PRIMARY_ALARM", "SECONDARY_ALARM"}}
	labels := []string{"IDLE_OFF", "PRIMARY_ALARM", "INTRUSION_ALARM", "SECONDARY_ALARM"}
	want := []bool{false, true, false, true}

	for i, label := range labels {
		t.Run(label, func(t *testing.T) {
			t.Parallel()

			active, known, resolved := resolveActive(rule, label, nil)
			if !known || !resolved {
				t.Fatalf("known=%v resolved=%v, want true/true", known, resolved)
			}
			if active != want[i] {
				t.Errorf("label %q: active = %v, want %v", label, active, want[i])
			}
		})
	}
}

// TestResolveActiveFallsBackWhenRuleCannotApply verifies that a
// configured rule which cannot be applied — no value list to resolve an
// index against, or a value of the wrong shape — falls back to the
// historical rule and reports resolved=false, so the caller can warn.
func TestResolveActiveFallsBackWhenRuleCannotApply(t *testing.T) {
	t.Parallel()

	rule := activationRule{labels: []string{"PRIMARY_ALARM", "SECONDARY_ALARM"}}

	t.Run("missing value list", func(t *testing.T) {
		t.Parallel()

		active, known, resolved := resolveActive(rule, 2, nil)
		if resolved {
			t.Error("resolved = true, want false (no value list to resolve the index against)")
		}
		wantActive, wantKnown := normalizeActive(2)
		if active != wantActive || known != wantKnown {
			t.Errorf("fallback verdict = (%v,%v), want historical rule (%v,%v)", active, known, wantActive, wantKnown)
		}
	})

	t.Run("empty value list", func(t *testing.T) {
		t.Parallel()

		active, known, resolved := resolveActive(rule, 0, []string{})
		if resolved {
			t.Error("resolved = true, want false (an empty value list cannot resolve an index)")
		}
		wantActive, wantKnown := normalizeActive(0)
		if active != wantActive || known != wantKnown {
			t.Errorf("fallback verdict = (%v,%v), want historical rule (%v,%v)", active, known, wantActive, wantKnown)
		}
	})

	t.Run("bool value on a configured rule", func(t *testing.T) {
		t.Parallel()

		valueList := []string{"IDLE_OFF", "PRIMARY_ALARM", "INTRUSION_ALARM", "SECONDARY_ALARM"}
		active, known, resolved := resolveActive(rule, true, valueList)
		if resolved {
			t.Error("resolved = true, want false (a bool value against an enumerated rule is a misconfiguration)")
		}
		wantActive, wantKnown := normalizeActive(true)
		if active != wantActive || known != wantKnown {
			t.Errorf("fallback verdict = (%v,%v), want historical rule (%v,%v)", active, known, wantActive, wantKnown)
		}
	})
}

// TestResolveActiveIndexOutsideValueListIsInactive verifies an index the
// declared value list does not cover — including a negative one —
// reports inactive rather than falling back, because the list is
// exhaustive by construction.
func TestResolveActiveIndexOutsideValueListIsInactive(t *testing.T) {
	t.Parallel()

	rule := activationRule{labels: []string{"PRIMARY_ALARM"}}
	valueList := []string{"IDLE_OFF", "PRIMARY_ALARM"}

	active, known, resolved := resolveActive(rule, 5, valueList)
	if active || !known || !resolved {
		t.Errorf("resolveActive(rule, 5, list) = (%v,%v,%v), want (false,true,true)", active, known, resolved)
	}

	active, known, resolved = resolveActive(rule, -1, valueList)
	if active || !known || !resolved {
		t.Errorf("resolveActive(rule, -1, list) = (%v,%v,%v), want (false,true,true)", active, known, resolved)
	}
}

// TestResolveActiveExactMatchIsCaseSensitive verifies a label differing
// only in case does not match — a value list is a fixed vocabulary, and
// a case-insensitive match would silently accept a label the device
// never emits.
func TestResolveActiveExactMatchIsCaseSensitive(t *testing.T) {
	t.Parallel()

	rule := activationRule{labels: []string{"PRIMARY_ALARM"}}
	active, known, resolved := resolveActive(rule, "primary_alarm", nil)
	if active || !known || !resolved {
		t.Errorf("resolveActive(rule, %q, nil) = (%v,%v,%v), want (false,true,true)", "primary_alarm", active, known, resolved)
	}
}

// --- activationRule ------------------------------------------------------

func TestActivationRuleConfigured(t *testing.T) {
	t.Parallel()

	if (activationRule{}).configured() {
		t.Error("configured() = true for an empty rule, want false")
	}
	if !(activationRule{labels: []string{"X"}}).configured() {
		t.Error("configured() = false for a rule carrying labels, want true")
	}
}

func TestActivationRuleMatches(t *testing.T) {
	t.Parallel()

	rule := activationRule{labels: []string{"PRIMARY_ALARM", "SECONDARY_ALARM"}}
	cases := []struct {
		label string
		want  bool
	}{
		{"PRIMARY_ALARM", true},
		{"SECONDARY_ALARM", true},
		{"INTRUSION_ALARM", false},
		{"primary_alarm", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := rule.matches(tc.label); got != tc.want {
			t.Errorf("matches(%q) = %v, want %v", tc.label, got, tc.want)
		}
	}
	if (activationRule{}).matches("anything") {
		t.Error("matches on an empty rule = true, want false (no labels to match against)")
	}
}

// --- Service.active -------------------------------------------------------

// countingHandler is a minimal slog.Handler that only counts records, so
// a test can assert a warning fired exactly once without depending on
// message formatting.
type countingHandler struct {
	mu    sync.Mutex
	count int
}

func (h *countingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *countingHandler) Handle(context.Context, slog.Record) error {
	h.mu.Lock()
	h.count++
	h.mu.Unlock()
	return nil
}

func (h *countingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }

func (h *countingHandler) WithGroup(string) slog.Handler { return h }

func (h *countingHandler) Count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.count
}

// TestServiceActiveFallsBackAndWarnsOnceForUnresolvableValueList builds a
// Service as a bare struct literal (the established pattern in this
// package's tests — see candidates_test.go) around a binding whose
// channel the registry does not carry. The rule is configured, so
// resolution needs the parameter's value list; since the channel never
// resolves, active falls back to the historical rule and must warn
// exactly once, not on every event of the same binding.
func TestServiceActiveFallsBackAndWarnsOnceForUnresolvableValueList(t *testing.T) {
	t.Parallel()

	reg := central.NewRegistry()
	u, err := central.New(central.Config{Name: "my-ccu"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	if err := reg.Register(u); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	// "my-ccu" is registered but carries no device/channel matching the
	// binding below, so the registry genuinely has no such channel.

	handler := &countingHandler{}
	s := &Service{
		reg:   reg,
		enums: newEnumResolver(reg),
		log:   slog.New(handler),
	}
	binding := sensorBinding{
		id:             "sensor-1",
		rule:           activationRule{labels: []string{"PRIMARY_ALARM", "SECONDARY_ALARM"}},
		centralName:    "my-ccu",
		interfaceID:    "HmIP-RF",
		channelAddress: "SWSD0001:1",
		parameter:      "SMOKE_DETECTOR_ALARM_STATUS",
	}
	value := hmtypes.IntValue(1)

	active, known := s.active(binding, value)
	if !known {
		t.Fatal("known = false, want true (a fallback verdict is still a known one)")
	}
	if !active {
		t.Error("active = false, want true (normalizeActive(1) is active)")
	}
	if got := handler.Count(); got != 1 {
		t.Fatalf("log count after first event = %d, want 1", got)
	}

	// A second event on the same binding must not log again — the
	// chattering-sensor guard in enumResolver.shouldWarn.
	s.active(binding, value)
	if got := handler.Count(); got != 1 {
		t.Errorf("log count after second event = %d, want still 1 (no repeat warning)", got)
	}
}
