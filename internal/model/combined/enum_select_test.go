// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package combined_test

import (
	"context"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/combined"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// garageModes mirrors the door drive the EnumSelect was built for: three
// selectable states, only one of which shares its name with the command
// that reaches it.
func garageModes() []combined.EnumSelectMode {
	return []combined.EnumSelectMode{
		{State: "CLOSED", Command: "CLOSE"},
		{State: "VENTILATION_POSITION", Command: "PARTIAL_OPEN"},
		{State: "OPEN", Command: "OPEN"},
	}
}

type recordingWriter struct {
	mu     sync.Mutex
	writes []struct {
		parameter hmenum.Parameter
		value     any
	}
	err error
}

func (w *recordingWriter) SetValue(
	_ context.Context, _ string, parameter hmenum.Parameter, value any, _ hmenum.CommandPriority,
) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return w.err
	}
	w.writes = append(w.writes, struct {
		parameter hmenum.Parameter
		value     any
	}{parameter, value})
	return nil
}

func (w *recordingWriter) last() (hmenum.Parameter, any, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.writes) == 0 {
		return "", nil, false
	}
	last := w.writes[len(w.writes)-1]
	return last.parameter, last.value, true
}

func newGarageSelect(w combined.Writer) *combined.EnumSelect {
	return combined.NewEnumSelect(combined.EnumSelectConfig{
		Address:           "VCU0000001:1",
		CentralName:       "ccu-prod",
		Writer:            w,
		Kind:              "door_mode",
		LabelKey:          "discovery.garage_door_mode",
		CombinedParameter: "DOOR_MODE",
		StateParameter:    "DOOR_STATE",
		CommandParameter:  "DOOR_COMMAND",
		Modes:             garageModes(),
	})
}

// TestEnumSelectRejectsAnEmptyModeList pins that a select with nothing to
// select is never constructed. An HA select entity with an empty options
// list renders as a control an operator can click and that can do
// nothing — worse than no entity at all.
func TestEnumSelectRejectsAnEmptyModeList(t *testing.T) {
	t.Parallel()
	got := combined.NewEnumSelect(combined.EnumSelectConfig{
		Address: "VCU0000001:1", Kind: "door_mode", CombinedParameter: "DOOR_MODE",
	})
	if got != nil {
		t.Fatal("NewEnumSelect with no modes must return nil")
	}
}

// TestEnumSelectHasNoValueBeforeAnythingIsObserved pins that the data
// point reports "no value" rather than defaulting to the first mode. A
// select defaulting to CLOSED before the door has said anything is a
// reading, not a placeholder, and an operator cannot tell the two apart.
func TestEnumSelectHasNoValueBeforeAnythingIsObserved(t *testing.T) {
	t.Parallel()
	e := newGarageSelect(&recordingWriter{})
	if v, ok := e.Value(); ok {
		t.Fatalf("Value() = (%q, true) before any observation, want (\"\", false)", v)
	}
	if _, observed := e.CombinedStatePayload(); observed {
		t.Fatal("CombinedStatePayload() must not report an observation before one happened")
	}
}

// TestEnumSelectMapsModeToItsCommand pins the write half of the pair. The
// ventilation mode is the case that matters: it is the only one whose
// state token and command differ, so a mapping that quietly used the
// state token would still pass for the other two.
func TestEnumSelectMapsModeToItsCommand(t *testing.T) {
	t.Parallel()
	cases := []struct {
		mode        string
		wantCommand string
	}{
		{"CLOSED", "CLOSE"},
		{"VENTILATION_POSITION", "PARTIAL_OPEN"},
		{"OPEN", "OPEN"},
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			t.Parallel()
			w := &recordingWriter{}
			e := newGarageSelect(w)
			if err := e.SetMode(context.Background(), tc.mode, hmenum.CommandPriorityHigh); err != nil {
				t.Fatalf("SetMode(%q): %v", tc.mode, err)
			}
			param, value, ok := w.last()
			if !ok {
				t.Fatal("SetMode wrote nothing")
			}
			if param != "DOOR_COMMAND" {
				t.Errorf("wrote parameter %q, want DOOR_COMMAND", param)
			}
			if value != tc.wantCommand {
				t.Errorf("wrote %v, want %q", value, tc.wantCommand)
			}
		})
	}
}

// TestEnumSelectRejectsAModeItDoesNotOffer pins that an unknown mode is
// refused rather than written through to the device.
func TestEnumSelectRejectsAModeItDoesNotOffer(t *testing.T) {
	t.Parallel()
	w := &recordingWriter{}
	e := newGarageSelect(w)
	if err := e.SetMode(context.Background(), "POSITION_UNKNOWN", hmenum.CommandPriorityHigh); err == nil {
		t.Fatal("SetMode with a non-selectable mode must fail")
	}
	if _, _, wrote := w.last(); wrote {
		t.Error("a rejected mode must not reach the device")
	}
}

// TestEnumSelectHoldsTheCommandedModeWhileTravelling pins the optimistic
// hold.
//
// While the door travels, DOOR_STATE reports a token that is not a
// selectable mode. Without the hold the select would drop to "no value"
// on every movement and flicker for the whole travel; with it, the mode
// just commanded stays visible until the door reports a state of its own.
func TestEnumSelectHoldsTheCommandedModeWhileTravelling(t *testing.T) {
	t.Parallel()
	e := newGarageSelect(&recordingWriter{})

	e.NoteCommand("PARTIAL_OPEN")
	if v, ok := e.Value(); !ok || v != "VENTILATION_POSITION" {
		t.Fatalf("Value() = (%q, %v) after commanding vent, want VENTILATION_POSITION", v, ok)
	}

	// The device confirms. The observation replaces the hold.
	e.OnState("VENTILATION_POSITION")
	if v, ok := e.Value(); !ok || v != "VENTILATION_POSITION" {
		t.Fatalf("Value() = (%q, %v) after the device confirmed, want VENTILATION_POSITION", v, ok)
	}
}

// TestEnumSelectClearsTheHoldOnACommandWithNoResultingMode pins the case
// the hold gets wrong if nobody thinks about it.
//
// STOP reaches no mode. The device is left at a non-mode state with no
// further event coming, so a hold left in place would report a mode the
// door is not in — and would do so indefinitely, because nothing arrives
// to correct it.
func TestEnumSelectClearsTheHoldOnACommandWithNoResultingMode(t *testing.T) {
	t.Parallel()
	e := newGarageSelect(&recordingWriter{})

	e.NoteCommand("OPEN")
	if v, _ := e.Value(); v != "OPEN" {
		t.Fatalf("Value() = %q after commanding open, want OPEN", v)
	}

	e.NoteCommand("STOP")
	if v, ok := e.Value(); ok || v != "" {
		t.Fatalf("Value() = (%q, %v) after STOP, want no value — a stop reaches no mode", v, ok)
	}
}

// TestEnumSelectKeepsTheObservedStateOverAStaleHold pins precedence: what
// the device reports wins over what was commanded.
func TestEnumSelectKeepsTheObservedStateOverAStaleHold(t *testing.T) {
	t.Parallel()
	e := newGarageSelect(&recordingWriter{})

	// The door is open, and someone commands it closed. Until it reports
	// otherwise it is still open — reporting CLOSED here would be a lie
	// the operator has no way to check.
	e.OnState("OPEN")
	e.NoteCommand("CLOSE")
	if v, _ := e.Value(); v != "OPEN" {
		t.Fatalf("Value() = %q while the door still reports OPEN, want OPEN", v)
	}

	// It starts moving: the state is no longer a mode, so the hold shows.
	e.OnState("POSITION_UNKNOWN")
	if v, _ := e.Value(); v != "CLOSED" {
		t.Fatalf("Value() = %q while travelling, want the commanded CLOSED", v)
	}

	// It arrives.
	e.OnState("CLOSED")
	if v, _ := e.Value(); v != "CLOSED" {
		t.Fatalf("Value() = %q after arrival, want CLOSED", v)
	}
}

// TestEnumSelectNotifiesSubscribersOnChange pins that the projection's
// live subscription actually fires — the event bridge re-reads the state
// on this callback, so a silent one leaves the retained topic stale.
func TestEnumSelectNotifiesSubscribersOnChange(t *testing.T) {
	t.Parallel()
	e := newGarageSelect(&recordingWriter{})

	var mu sync.Mutex
	var seen []string
	unsub := e.OnCombinedChange(func() {
		state, _ := e.CombinedStatePayload()
		mu.Lock()
		seen = append(seen, state)
		mu.Unlock()
	})
	defer unsub()

	e.OnState("OPEN")
	e.OnState("CLOSED")

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 2 || seen[0] != "OPEN" || seen[1] != "CLOSED" {
		t.Fatalf("subscriber saw %v, want [OPEN CLOSED]", seen)
	}
}

// TestEnumSelectDoesNotRepublishAnUnchangedState pins that a repeated
// identical report is not an event. The CCU re-sends values on
// reconnects, and turning each one into a publish would put avoidable
// traffic on every subscriber.
func TestEnumSelectDoesNotRepublishAnUnchangedState(t *testing.T) {
	t.Parallel()
	e := newGarageSelect(&recordingWriter{})

	var mu sync.Mutex
	calls := 0
	unsub := e.OnCombinedChange(func() {
		mu.Lock()
		calls++
		mu.Unlock()
	})
	defer unsub()

	e.OnState("OPEN")
	e.OnState("OPEN")
	e.OnState("OPEN")

	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("subscriber fired %d times for three identical reports, want 1", calls)
	}
}

// TestEnumSelectProjectsAsASelectCarryingItsModes pins the discovery half:
// the component and the option list HA renders.
func TestEnumSelectProjectsAsASelectCarryingItsModes(t *testing.T) {
	t.Parallel()
	e := newGarageSelect(&recordingWriter{})
	component, body := e.HACombinedDiscovery(stubCombinedContext{})
	if component != "select" {
		t.Fatalf("component = %q, want select", component)
	}
	options, ok := body["options"].([]string)
	if !ok {
		t.Fatalf("options = %T, want []string", body["options"])
	}
	// Presentation order follows the door's travel, closed to open.
	want := []string{"CLOSED", "VENTILATION_POSITION", "OPEN"}
	if len(options) != len(want) {
		t.Fatalf("options = %v, want %v", options, want)
	}
	for i := range want {
		if options[i] != want[i] {
			t.Fatalf("options = %v, want %v", options, want)
		}
	}
	if body["command_topic"] == nil || body["command_topic"] == "" {
		t.Error("a writable select must declare a command topic")
	}
}

// TestEnumSelectWriteCombinedRoutesToSetMode pins the MQTT write path: the
// payload HA publishes on the command topic is a mode token.
func TestEnumSelectWriteCombinedRoutesToSetMode(t *testing.T) {
	t.Parallel()
	w := &recordingWriter{}
	e := newGarageSelect(w)
	if err := e.WriteCombined(context.Background(), "VENTILATION_POSITION", hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("WriteCombined: %v", err)
	}
	_, value, ok := w.last()
	if !ok || value != "PARTIAL_OPEN" {
		t.Fatalf("WriteCombined wrote %v, want PARTIAL_OPEN", value)
	}
}

// stubCombinedContext is a discovery context with fixed topics; the label
// lookups echo their input so an assertion can tell which path produced a
// string.
type stubCombinedContext struct{}

func (stubCombinedContext) CombinedStateTopic() string   { return "base/combined/door_mode" }
func (stubCombinedContext) CombinedCommandTopic() string { return "base/combined/door_mode/set" }
func (stubCombinedContext) Translate(key string) string  { return key }
func (stubCombinedContext) ParameterLabel(hmenum.Parameter) (string, bool) {
	return "", false
}
