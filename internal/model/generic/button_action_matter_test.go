// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package generic

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ---------------------------------------------------------------------------
// Shared stubs for Matter switch event emission
// ---------------------------------------------------------------------------

// matterSwitchEmitter4 records calls to the four-method Matter switch
// event emitter interface.
type matterSwitchEmitter4 struct {
	initPresses   int
	shortReleases int
	longPresses   int
	longReleases  int
}

func (s *matterSwitchEmitter4) FireInitialPress(_ uint8) { s.initPresses++ }
func (s *matterSwitchEmitter4) FireShortRelease(_ uint8) { s.shortReleases++ }
func (s *matterSwitchEmitter4) FireLongPress(_ uint8)    { s.longPresses++ }
func (s *matterSwitchEmitter4) FireLongRelease(_ uint8)  { s.longReleases++ }

// matterSwitchEmitter6 records calls to the six-method Matter switch
// event emitter interface (including multi-press events).
type matterSwitchEmitter6 struct {
	initial, short, long, longRelease int
}

func (s *matterSwitchEmitter6) FireInitialPress(pos uint8)   { s.initial++ }
func (s *matterSwitchEmitter6) FireShortRelease(pos uint8)   { s.short++ }
func (s *matterSwitchEmitter6) FireLongPress(pos uint8)      { s.long++ }
func (s *matterSwitchEmitter6) FireLongRelease(pos uint8)    { s.longRelease++ }
func (s *matterSwitchEmitter6) FireMultiPressOngoing(uint8)  {}
func (s *matterSwitchEmitter6) FireMultiPressComplete(uint8) {}

// ---------------------------------------------------------------------------
// button.go — WireMatterSwitchHandler and Press branches
// ---------------------------------------------------------------------------

func TestButton_WireMatterSwitchHandler_NilEmitter(t *testing.T) {
	t.Parallel()
	b := NewButton(baseCfg(hmenum.ParameterPress, hmenum.ParameterTypeBool,
		hmenum.OperationsRead|hmenum.OperationsWrite|hmenum.OperationsEvent))
	unsub := b.WireMatterSwitchHandler(nil)
	unsub() // must not panic
}

func TestButton_WireMatterSwitchHandler_PressShort(t *testing.T) {
	t.Parallel()
	b := NewButton(baseCfg(hmenum.ParameterPressShort, hmenum.ParameterTypeBool,
		hmenum.OperationsRead|hmenum.OperationsWrite|hmenum.OperationsEvent))
	e := &matterSwitchEmitter4{}
	unsub := b.WireMatterSwitchHandler(e)
	defer unsub()
	b.OnEvent(true)
	if e.initPresses != 1 || e.shortReleases != 1 {
		t.Errorf("PressShort: got initPresses=%d shortReleases=%d, want 1,1",
			e.initPresses, e.shortReleases)
	}
}

func TestButton_WireMatterSwitchHandler_FallingEdge_NoEvents(t *testing.T) {
	t.Parallel()
	b := NewButton(baseCfg(hmenum.ParameterPress, hmenum.ParameterTypeBool,
		hmenum.OperationsRead|hmenum.OperationsWrite|hmenum.OperationsEvent))
	e := &matterSwitchEmitter4{}
	_ = b.WireMatterSwitchHandler(e)
	b.OnEvent(true)  // rising edge → events
	b.OnEvent(false) // falling edge → no additional events
	if e.initPresses != 1 {
		t.Errorf("expected 1 initPress after rising+falling, got %d", e.initPresses)
	}
}

func TestButton_WireMatterSwitchHandler_PressLong(t *testing.T) {
	t.Parallel()
	b := NewButton(baseCfg(hmenum.ParameterPressLong, hmenum.ParameterTypeBool,
		hmenum.OperationsRead|hmenum.OperationsWrite|hmenum.OperationsEvent))
	e := &matterSwitchEmitter4{}
	_ = b.WireMatterSwitchHandler(e)
	b.OnEvent(true)
	if e.initPresses != 1 || e.longPresses != 1 {
		t.Errorf("PressLong: got initPresses=%d longPresses=%d, want 1,1",
			e.initPresses, e.longPresses)
	}
}

func TestButton_WireMatterSwitchHandler_PressLongRelease(t *testing.T) {
	t.Parallel()
	b := NewButton(baseCfg(hmenum.ParameterPressLongRelease, hmenum.ParameterTypeBool,
		hmenum.OperationsRead|hmenum.OperationsWrite|hmenum.OperationsEvent))
	e := &matterSwitchEmitter4{}
	_ = b.WireMatterSwitchHandler(e)
	b.OnEvent(true)
	if e.longReleases != 1 {
		t.Errorf("PressLongRelease: got longReleases=%d, want 1", e.longReleases)
	}
}

func TestButton_WireMatterSwitchHandler_PressCont(t *testing.T) {
	t.Parallel()
	b := NewButton(baseCfg(hmenum.ParameterPressCont, hmenum.ParameterTypeBool,
		hmenum.OperationsRead|hmenum.OperationsWrite|hmenum.OperationsEvent))
	e := &matterSwitchEmitter4{}
	_ = b.WireMatterSwitchHandler(e)
	b.OnEvent(true)
	if e.longPresses != 1 {
		t.Errorf("PressCont: got longPresses=%d, want 1", e.longPresses)
	}
}

func TestButton_WireMatterSwitchHandler_UnknownParam_NoEvents(t *testing.T) {
	t.Parallel()
	b := NewButton(baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool,
		hmenum.OperationsRead|hmenum.OperationsWrite|hmenum.OperationsEvent))
	e := &matterSwitchEmitter4{}
	_ = b.WireMatterSwitchHandler(e)
	b.OnEvent(true)
	if e.initPresses != 0 {
		t.Errorf("Unknown param: expected no initPress events, got %d", e.initPresses)
	}
}

func TestButton_Press_NotWritableAndNotAction(t *testing.T) {
	t.Parallel()
	b := NewButton(baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool,
		hmenum.OperationsRead)) // not writable, not ACTION type
	if err := b.Press(context.Background(), hmenum.CommandPriorityHigh); !errors.Is(err, ErrNotWritable) {
		t.Errorf("expected ErrNotWritable, got %v", err)
	}
}

func TestButton_MatterMeasurementClass_Press(t *testing.T) {
	t.Parallel()
	b := NewButton(baseCfg(hmenum.ParameterPress, hmenum.ParameterTypeBool,
		hmenum.OperationsRead|hmenum.OperationsWrite))
	got := b.MatterMeasurementClass()
	if got == 0 {
		t.Error("PRESS should map to MomentarySwitch")
	}
}

func TestButton_MatterMeasurementClass_OtherParam(t *testing.T) {
	t.Parallel()
	b := NewButton(baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool,
		hmenum.OperationsRead|hmenum.OperationsWrite))
	if got := b.MatterMeasurementClass(); got != 0 {
		t.Errorf("STATE should yield MatterMeasurementNone (0), got %v", got)
	}
}

// ---------------------------------------------------------------------------
// button.go — MatterSwitchPositions / MatterSwitchSupportsLongPress
// ---------------------------------------------------------------------------

func TestButtonMatterHelpers(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	cfgLong := baseCfg(hmenum.ParameterPressLong, hmenum.ParameterTypeAction, hmenum.OperationsEvent)
	cfgLong.Writer = w
	b := NewButton(cfgLong)

	if b.MatterMeasurementClass() == 0 {
		t.Fatal("button: PRESS_LONG should be MomentarySwitch")
	}
	if b.MatterSwitchPositions() != 2 {
		t.Fatalf("MatterSwitchPositions = %d, want 2", b.MatterSwitchPositions())
	}
	if !b.MatterSwitchSupportsLongPress() {
		t.Fatal("PRESS_LONG button must support long press")
	}

	cfgShort := baseCfg(hmenum.ParameterPressShort, hmenum.ParameterTypeAction, hmenum.OperationsEvent)
	cfgShort.Writer = w
	bShort := NewButton(cfgShort)
	if bShort.MatterSwitchSupportsLongPress() {
		t.Fatal("PRESS_SHORT button must NOT support long press")
	}
}

// ---------------------------------------------------------------------------
// action.go — WireMatterSwitchHandler and Trigger branches
// ---------------------------------------------------------------------------

func TestAction_WireMatterSwitchHandler_NilEmitter(t *testing.T) {
	t.Parallel()
	a := NewAction(baseCfg(hmenum.ParameterPress, hmenum.ParameterTypeAction,
		hmenum.OperationsWrite))
	unsub := a.WireMatterSwitchHandler(nil)
	unsub() // must not panic
}

func TestAction_WireMatterSwitchHandler_PressShort(t *testing.T) {
	t.Parallel()
	a := NewAction(baseCfg(hmenum.ParameterPressShort, hmenum.ParameterTypeAction,
		hmenum.OperationsWrite|hmenum.OperationsEvent))
	e := &matterSwitchEmitter4{}
	_ = a.WireMatterSwitchHandler(e)
	a.OnEvent(true) // trigger the handler
	if e.initPresses != 1 || e.shortReleases != 1 {
		t.Errorf("PressShort Action: got initPresses=%d shortReleases=%d, want 1,1",
			e.initPresses, e.shortReleases)
	}
}

func TestAction_WireMatterSwitchHandler_FalseValue_NoEvents(t *testing.T) {
	t.Parallel()
	a := NewAction(baseCfg(hmenum.ParameterPressShort, hmenum.ParameterTypeAction,
		hmenum.OperationsWrite|hmenum.OperationsEvent))
	e := &matterSwitchEmitter4{}
	_ = a.WireMatterSwitchHandler(e)
	a.OnEvent(false) // bool false → no events
	if e.initPresses != 0 {
		t.Errorf("false value: expected 0 initPresses, got %d", e.initPresses)
	}
}

func TestAction_WireMatterSwitchHandler_NilValue_NoEvents(t *testing.T) {
	t.Parallel()
	a := NewAction(baseCfg(hmenum.ParameterPressShort, hmenum.ParameterTypeAction,
		hmenum.OperationsWrite|hmenum.OperationsEvent))
	e := &matterSwitchEmitter4{}
	_ = a.WireMatterSwitchHandler(e)
	a.OnEvent(nil) // nil → no events
	if e.initPresses != 0 {
		t.Errorf("nil value: expected 0 initPresses, got %d", e.initPresses)
	}
}

func TestAction_WireMatterSwitchHandler_PressLongStart(t *testing.T) {
	t.Parallel()
	a := NewAction(baseCfg(hmenum.ParameterPressLongStart, hmenum.ParameterTypeAction,
		hmenum.OperationsWrite|hmenum.OperationsEvent))
	e := &matterSwitchEmitter4{}
	_ = a.WireMatterSwitchHandler(e)
	a.OnEvent(true)
	if e.initPresses != 1 || e.longPresses != 1 {
		t.Errorf("PressLongStart Action: got initPresses=%d longPresses=%d, want 1,1",
			e.initPresses, e.longPresses)
	}
}

func TestAction_WireMatterSwitchHandler_PressLongRelease(t *testing.T) {
	t.Parallel()
	a := NewAction(baseCfg(hmenum.ParameterPressLongRelease, hmenum.ParameterTypeAction,
		hmenum.OperationsWrite|hmenum.OperationsEvent))
	e := &matterSwitchEmitter4{}
	_ = a.WireMatterSwitchHandler(e)
	a.OnEvent(true)
	if e.longReleases != 1 {
		t.Errorf("PressLongRelease Action: got longReleases=%d, want 1", e.longReleases)
	}
}

func TestAction_WireMatterSwitchHandler_PressCont(t *testing.T) {
	t.Parallel()
	a := NewAction(baseCfg(hmenum.ParameterPressCont, hmenum.ParameterTypeAction,
		hmenum.OperationsWrite|hmenum.OperationsEvent))
	e := &matterSwitchEmitter4{}
	_ = a.WireMatterSwitchHandler(e)
	a.OnEvent(true)
	if e.longPresses != 1 {
		t.Errorf("PressCont Action: got longPresses=%d, want 1", e.longPresses)
	}
}

func TestAction_Trigger_NotWritableAndNotAction(t *testing.T) {
	t.Parallel()
	a := NewAction(baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool,
		hmenum.OperationsRead)) // not writable, not ACTION type
	if err := a.Trigger(context.Background(), true, hmenum.CommandPriorityHigh); !errors.Is(err, ErrNotWritable) {
		t.Errorf("expected ErrNotWritable, got %v", err)
	}
}

func TestAction_Trigger_NoWriter(t *testing.T) {
	t.Parallel()
	a := NewAction(baseCfg(hmenum.ParameterPress, hmenum.ParameterTypeAction,
		hmenum.OperationsWrite)) // no writer wired
	if err := a.Trigger(context.Background(), true, hmenum.CommandPriorityHigh); !errors.Is(err, ErrNoWriter) {
		t.Errorf("expected ErrNoWriter, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// action.go — WireMatterSwitchHandler with six-method emitter, MatterMeasurementClass,
//             MatterSwitchSupportsLongPress, MatterSwitchPositions
// ---------------------------------------------------------------------------

func TestActionWireMatterSwitchHandler(t *testing.T) {
	t.Parallel()
	cfg := baseCfg(hmenum.ParameterPressShort, hmenum.ParameterTypeAction, hmenum.OperationsEvent)
	cfg.Writer = &stubWriter{}
	a := NewAction(cfg)

	h := &matterSwitchEmitter6{}
	unsub := a.WireMatterSwitchHandler(h)
	defer unsub()

	a.OnEvent(true)
	if h.initial == 0 {
		t.Fatal("FireInitialPress not called after WireMatterSwitchHandler + OnEvent")
	}

	// nil emitter → returns noop without panic.
	noop := a.WireMatterSwitchHandler(nil)
	noop()
}

func TestActionMatterHelpers(t *testing.T) {
	t.Parallel()
	makeAction := func(param hmenum.Parameter) *Action {
		cfg := baseCfg(param, hmenum.ParameterTypeAction, hmenum.OperationsEvent)
		cfg.Writer = &stubWriter{}
		return NewAction(cfg)
	}

	// Press parameters → MomentarySwitch.
	for _, p := range []hmenum.Parameter{
		hmenum.ParameterPressShort, hmenum.ParameterPressLong,
		hmenum.ParameterPressLongStart, hmenum.ParameterPressLongRelease,
		hmenum.ParameterPressCont,
	} {
		a := makeAction(p)
		if a.MatterMeasurementClass() == 0 { // MatterMeasurementNone == 0
			t.Errorf("param %s: expected MomentarySwitch, got None", p)
		}
	}

	// Non-press → None.
	a := makeAction(hmenum.ParameterResetMotion)
	if a.MatterMeasurementClass() != 0 {
		t.Errorf("RESET_MOTION should map to MatterMeasurementNone")
	}

	// LongPress support.
	longPress := makeAction(hmenum.ParameterPressLong)
	if !longPress.MatterSwitchSupportsLongPress() {
		t.Fatal("PRESS_LONG must support long press")
	}
	noLong := makeAction(hmenum.ParameterPressShort)
	if noLong.MatterSwitchSupportsLongPress() {
		t.Fatal("PRESS_SHORT must NOT support long press")
	}

	// MatterSwitchPositions is always 2.
	if longPress.MatterSwitchPositions() != 2 {
		t.Fatal("MatterSwitchPositions must be 2")
	}
}
