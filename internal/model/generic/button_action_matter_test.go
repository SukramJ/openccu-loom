// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package generic

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
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
	// A lone PRESS_LONG button has no PRESS_LONG_RELEASE sibling, so
	// every long press must complete the full Matter §1.13 cycle
	// immediately — including the closing LongRelease.
	b := NewButton(baseCfg(hmenum.ParameterPressLong, hmenum.ParameterTypeBool,
		hmenum.OperationsRead|hmenum.OperationsWrite|hmenum.OperationsEvent))
	e := &matterSwitchEmitter4{}
	_ = b.WireMatterSwitchHandler(e)
	b.OnEvent(true)
	if e.initPresses != 1 || e.longPresses != 1 || e.longReleases != 1 {
		t.Errorf("PressLong: got initPresses=%d longPresses=%d longReleases=%d, want 1,1,1",
			e.initPresses, e.longPresses, e.longReleases)
	}
}

func TestButton_WireMatterSwitchHandler_PressLongRelease(t *testing.T) {
	t.Parallel()
	// A release without a tracked hold synthesizes the missing
	// InitialPress + LongPress so the LongRelease never arrives
	// unpaired at the controller.
	b := NewButton(baseCfg(hmenum.ParameterPressLongRelease, hmenum.ParameterTypeBool,
		hmenum.OperationsRead|hmenum.OperationsWrite|hmenum.OperationsEvent))
	e := &matterSwitchEmitter4{}
	_ = b.WireMatterSwitchHandler(e)
	b.OnEvent(true)
	if e.initPresses != 1 || e.longPresses != 1 || e.longReleases != 1 {
		t.Errorf("PressLongRelease: got initPresses=%d longPresses=%d longReleases=%d, want 1,1,1",
			e.initPresses, e.longPresses, e.longReleases)
	}
}

func TestButton_WireMatterSwitchHandler_PressCont_RepeatsSuppressed(t *testing.T) {
	t.Parallel()
	// A lone PRESS_CONT DP is a group of one: no PRESS_LONG_RELEASE
	// member exists, so the hold end can never be signalled and each
	// continuation frame must close its own gesture. Repeat
	// suppression applies only where a release member can reopen the
	// cycle — see
	// TestButtonGroup_PressContWithReleaseMember_RepeatsStaySuppressed.
	b := NewButton(baseCfg(hmenum.ParameterPressCont, hmenum.ParameterTypeBool,
		hmenum.OperationsRead|hmenum.OperationsWrite|hmenum.OperationsEvent))
	e := &matterSwitchEmitter4{}
	_ = b.WireMatterSwitchHandler(e)
	for range 5 {
		b.OnEvent(true)
	}
	if e.initPresses != 5 {
		t.Errorf("PressCont x5: got initPresses=%d, want 5 (one gesture per frame)", e.initPresses)
	}
	if e.longPresses != 5 {
		t.Errorf("PressCont x5: got longPresses=%d, want 5 (no release member)", e.longPresses)
	}
	if e.longReleases != 5 {
		t.Errorf("PressCont x5: got longReleases=%d, want 5 (each gesture closed)", e.longReleases)
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
// button.go — ButtonGroup press-cycle state machine
// ---------------------------------------------------------------------------

// matterSwitchSeqRecorder records the ORDER of the emitted press-cycle
// events, not just their counts — the Matter §1.13 sequence contract
// (InitialPress → LongPress → LongRelease per matter.js
// packages/node/src/behaviors/switch/SwitchServer.ts) is exactly what
// the ButtonGroup state machine guards.
type matterSwitchSeqRecorder struct {
	seq []string
}

func (r *matterSwitchSeqRecorder) FireInitialPress(_ uint8) { r.seq = append(r.seq, "IP") }
func (r *matterSwitchSeqRecorder) FireShortRelease(_ uint8) { r.seq = append(r.seq, "SR") }
func (r *matterSwitchSeqRecorder) FireLongPress(_ uint8)    { r.seq = append(r.seq, "LP") }
func (r *matterSwitchSeqRecorder) FireLongRelease(_ uint8)  { r.seq = append(r.seq, "LR") }

func (r *matterSwitchSeqRecorder) assertSeq(t *testing.T, want ...string) {
	t.Helper()
	if len(r.seq) != len(want) {
		t.Fatalf("event sequence = %v, want %v", r.seq, want)
	}
	for i := range want {
		if r.seq[i] != want[i] {
			t.Fatalf("event sequence = %v, want %v", r.seq, want)
		}
	}
}

// pressButton builds an event-only press DP the way the resolver does
// for KEY / KEY_TRANSCEIVER channels.
func pressButton(p hmenum.Parameter) *Button {
	return NewButton(baseCfg(p, hmenum.ParameterTypeAction, hmenum.OperationsEvent))
}

func TestButtonGroup_ShortPress_EmitsInitialPressThenShortRelease(t *testing.T) {
	t.Parallel()
	short := pressButton(hmenum.ParameterPressShort)
	long := pressButton(hmenum.ParameterPressLong)
	g := NewButtonGroup(short, long)
	r := &matterSwitchSeqRecorder{}
	unsub := g.WireMatterSwitchHandler(r)
	defer unsub()

	short.OnEvent(true)
	r.assertSeq(t, "IP", "SR")
}

func TestButtonGroup_BidCosHold_OneGesturePerHold(t *testing.T) {
	t.Parallel()
	// Full BidCos KEY channel: PRESS_SHORT + PRESS_LONG + PRESS_CONT +
	// PRESS_LONG_RELEASE. A hold delivers PRESS_LONG once, PRESS_CONT
	// repeats (~300 ms), then PRESS_LONG_RELEASE — the group must
	// narrate that as ONE InitialPress → LongPress → LongRelease.
	short := pressButton(hmenum.ParameterPressShort)
	long := pressButton(hmenum.ParameterPressLong)
	cont := pressButton(hmenum.ParameterPressCont)
	release := pressButton(hmenum.ParameterPressLongRelease)
	g := NewButtonGroup(short, long, cont, release)
	r := &matterSwitchSeqRecorder{}
	unsub := g.WireMatterSwitchHandler(r)
	defer unsub()

	long.OnEvent(true)
	for range 4 {
		cont.OnEvent(true) // device-side repeats while held → suppressed
	}
	release.OnEvent(true)
	r.assertSeq(t, "IP", "LP", "LR")

	// A second hold starts a fresh, complete cycle.
	long.OnEvent(true)
	cont.OnEvent(true)
	release.OnEvent(true)
	r.assertSeq(t, "IP", "LP", "LR", "IP", "LP", "LR")
}

func TestButtonGroup_RepeatedPressLongWithinHold_Suppressed(t *testing.T) {
	t.Parallel()
	long := pressButton(hmenum.ParameterPressLong)
	release := pressButton(hmenum.ParameterPressLongRelease)
	g := NewButtonGroup(long, release)
	r := &matterSwitchSeqRecorder{}
	defer g.WireMatterSwitchHandler(r)()

	long.OnEvent(true)
	long.OnEvent(true) // repeat within the same hold
	long.OnEvent(true)
	release.OnEvent(true)
	r.assertSeq(t, "IP", "LP", "LR")
}

func TestButtonGroup_ContStartedHold_SynthesizesLongPressAtRelease(t *testing.T) {
	t.Parallel()
	// Hold opened by PRESS_CONT (no explicit PRESS_LONG frame): the
	// release still owes the LongPress so the controller sees the
	// mandatory InitialPress → LongPress → LongRelease order.
	cont := pressButton(hmenum.ParameterPressCont)
	release := pressButton(hmenum.ParameterPressLongRelease)
	g := NewButtonGroup(cont, release)
	r := &matterSwitchSeqRecorder{}
	defer g.WireMatterSwitchHandler(r)()

	cont.OnEvent(true)
	cont.OnEvent(true) // repeat → suppressed
	release.OnEvent(true)
	r.assertSeq(t, "IP", "LP", "LR")
}

func TestButtonGroup_OrphanLongRelease_SynthesizesFullSequence(t *testing.T) {
	t.Parallel()
	// A release with no tracked hold (press-start frames lost, daemon
	// restarted mid-hold) synthesizes the whole gesture so LongRelease
	// never arrives unpaired.
	long := pressButton(hmenum.ParameterPressLong)
	release := pressButton(hmenum.ParameterPressLongRelease)
	g := NewButtonGroup(long, release)
	r := &matterSwitchSeqRecorder{}
	defer g.WireMatterSwitchHandler(r)()

	release.OnEvent(true)
	r.assertSeq(t, "IP", "LP", "LR")
}

func TestButtonGroup_LongPressWithoutReleaseMember_CompletesCycle(t *testing.T) {
	t.Parallel()
	// HmIP KEY channels carry PRESS_SHORT + PRESS_LONG only. Without a
	// release parameter the device cannot signal the hold end, so each
	// PRESS_LONG is a complete InitialPress → LongPress → LongRelease
	// gesture.
	short := pressButton(hmenum.ParameterPressShort)
	long := pressButton(hmenum.ParameterPressLong)
	g := NewButtonGroup(short, long)
	r := &matterSwitchSeqRecorder{}
	defer g.WireMatterSwitchHandler(r)()

	long.OnEvent(true)
	r.assertSeq(t, "IP", "LP", "LR")
	long.OnEvent(true)
	r.assertSeq(t, "IP", "LP", "LR", "IP", "LP", "LR")
}

func TestButtonGroup_ShortPressClosesStaleHold(t *testing.T) {
	t.Parallel()
	// A short press while a hold is still open means the release frame
	// was lost — the stale long cycle is closed first so every
	// InitialPress stays paired with exactly one release.
	short := pressButton(hmenum.ParameterPressShort)
	long := pressButton(hmenum.ParameterPressLong)
	release := pressButton(hmenum.ParameterPressLongRelease)
	g := NewButtonGroup(short, long, release)
	r := &matterSwitchSeqRecorder{}
	defer g.WireMatterSwitchHandler(r)()

	long.OnEvent(true)  // IP LP — hold open
	short.OnEvent(true) // stale close LR, then IP SR
	r.assertSeq(t, "IP", "LP", "LR", "IP", "SR")
}

func TestButtonGroup_ShortPressClosesContStartedHoldAsShort(t *testing.T) {
	t.Parallel()
	// Stale CONT-opened hold (no LongPress emitted yet) closes as a
	// short release: matter.js emits ShortRelease when no LongPress
	// was generated since the previous InitialPress.
	short := pressButton(hmenum.ParameterPressShort)
	cont := pressButton(hmenum.ParameterPressCont)
	release := pressButton(hmenum.ParameterPressLongRelease)
	g := NewButtonGroup(short, cont, release)
	r := &matterSwitchSeqRecorder{}
	defer g.WireMatterSwitchHandler(r)()

	cont.OnEvent(true)  // IP — hold open, no LongPress yet
	short.OnEvent(true) // stale close SR, then IP SR
	r.assertSeq(t, "IP", "SR", "IP", "SR")
}

func TestButtonGroup_CurrentPositionTracksHold(t *testing.T) {
	t.Parallel()
	long := pressButton(hmenum.ParameterPressLong)
	release := pressButton(hmenum.ParameterPressLongRelease)
	g := NewButtonGroup(long, release)
	r := &matterSwitchSeqRecorder{}
	defer g.WireMatterSwitchHandler(r)()

	if got := g.MatterSwitchCurrentPosition(); got != 0 {
		t.Fatalf("idle CurrentPosition = %d, want 0", got)
	}
	long.OnEvent(true)
	if got := g.MatterSwitchCurrentPosition(); got != 1 {
		t.Fatalf("held CurrentPosition = %d, want 1", got)
	}
	release.OnEvent(true)
	if got := g.MatterSwitchCurrentPosition(); got != 0 {
		t.Fatalf("released CurrentPosition = %d, want 0", got)
	}
}

func TestButtonGroup_Construction(t *testing.T) {
	t.Parallel()
	short := pressButton(hmenum.ParameterPressShort)
	long := pressButton(hmenum.ParameterPressLong)
	state := NewButton(baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead))

	if g := NewButtonGroup(state); g != nil {
		t.Error("group of non-press members must be nil")
	}
	if g := NewButtonGroup(); g != nil {
		t.Error("empty group must be nil")
	}

	shortOnly := NewButtonGroup(short, state, nil)
	if shortOnly == nil {
		t.Fatal("group with one press member must construct")
	}
	if shortOnly.MatterSwitchSupportsLongPress() {
		t.Error("short-press-only group must not advertise long press")
	}
	if shortOnly.MatterSwitchPositions() != 2 {
		t.Errorf("MatterSwitchPositions = %d, want 2", shortOnly.MatterSwitchPositions())
	}
	if shortOnly.MatterMeasurementClass() != interfaces.MatterMeasurementMomentarySwitch {
		t.Error("group must classify as MomentarySwitch")
	}

	withLong := NewButtonGroup(short, long)
	if !withLong.MatterSwitchSupportsLongPress() {
		t.Error("group with a long member must advertise long press")
	}

	var nilGroup *ButtonGroup
	if nilGroup.MatterMeasurementClass() != interfaces.MatterMeasurementNone {
		t.Error("nil group must classify as None")
	}
	if nilGroup.MatterSwitchSupportsLongPress() {
		t.Error("nil group must not advertise long press")
	}
	if nilGroup.MatterSwitchCurrentPosition() != 0 {
		t.Error("nil group position must be 0")
	}
	nilGroup.WireMatterSwitchHandler(&matterSwitchSeqRecorder{})() // must not panic
}

func TestButtonGroup_UnsubscribeStopsDispatch(t *testing.T) {
	t.Parallel()
	short := pressButton(hmenum.ParameterPressShort)
	g := NewButtonGroup(short)
	r := &matterSwitchSeqRecorder{}
	unsub := g.WireMatterSwitchHandler(r)

	short.OnEvent(true)
	unsub()
	short.OnEvent(true)
	r.assertSeq(t, "IP", "SR") // only the pre-unsubscribe press
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

func TestAction_WireMatterSwitchHandler_PressCont_RepeatsSuppressed(t *testing.T) {
	t.Parallel()
	// Same press-cycle semantics as the Button wrapper: a lone
	// PRESS_CONT DP has no PRESS_LONG_RELEASE member, so every
	// continuation frame is a complete gesture rather than a
	// suppressed repeat of a cycle nothing could ever close.
	a := NewAction(baseCfg(hmenum.ParameterPressCont, hmenum.ParameterTypeAction,
		hmenum.OperationsWrite|hmenum.OperationsEvent))
	e := &matterSwitchEmitter4{}
	_ = a.WireMatterSwitchHandler(e)
	for range 5 {
		a.OnEvent(true)
	}
	if e.initPresses != 5 {
		t.Errorf("PressCont x5: got initPresses=%d, want 5 (one gesture per frame)", e.initPresses)
	}
	if e.longPresses != 5 {
		t.Errorf("PressCont x5: got longPresses=%d, want 5 (no release member)", e.longPresses)
	}
	if e.longReleases != 5 {
		t.Errorf("PressCont x5: got longReleases=%d, want 5 (each gesture closed)", e.longReleases)
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

func TestButtonGroup_PressContWithoutReleaseMember_CompletesCycle(t *testing.T) {
	t.Parallel()
	// HM-Sen-DB-PCB channel 1 declares PRESS_SHORT + PRESS_CONT and
	// nothing else. Without a PRESS_LONG_RELEASE member the device can
	// never signal the hold end, so a CONT-opened hold would latch
	// forever: no LongPress, no LongRelease, every later CONT
	// suppressed. Each PRESS_CONT must therefore be a complete
	// InitialPress -> LongPress -> LongRelease gesture, mirroring the
	// PRESS_LONG branch.
	short := pressButton(hmenum.ParameterPressShort)
	cont := pressButton(hmenum.ParameterPressCont)
	g := NewButtonGroup(short, cont)
	r := &matterSwitchSeqRecorder{}
	defer g.WireMatterSwitchHandler(r)()

	cont.OnEvent(true)
	r.assertSeq(t, "IP", "LP", "LR")
	if got := g.MatterSwitchCurrentPosition(); got != switchNeutralPosition {
		t.Fatalf("CurrentPosition after first CONT = %d, want %d", got, switchNeutralPosition)
	}

	cont.OnEvent(true)
	r.assertSeq(t, "IP", "LP", "LR", "IP", "LP", "LR")
	if got := g.MatterSwitchCurrentPosition(); got != switchNeutralPosition {
		t.Fatalf("CurrentPosition after second CONT = %d, want %d", got, switchNeutralPosition)
	}

	// No hold is open, so the following short press must not emit a
	// stray stale-close release before its own gesture.
	short.OnEvent(true)
	r.assertSeq(t, "IP", "LP", "LR", "IP", "LP", "LR", "IP", "SR")
}

func TestButtonGroup_PressContWithReleaseMember_RepeatsStaySuppressed(t *testing.T) {
	t.Parallel()
	// Counterpart: with PRESS_LONG_RELEASE present the device DOES
	// signal the hold end, so the ~300 ms BidCos CONT repeats stay
	// collapsed into one gesture and the position reads pressed while
	// the hold is open.
	cont := pressButton(hmenum.ParameterPressCont)
	release := pressButton(hmenum.ParameterPressLongRelease)
	g := NewButtonGroup(cont, release)
	r := &matterSwitchSeqRecorder{}
	defer g.WireMatterSwitchHandler(r)()

	cont.OnEvent(true)
	cont.OnEvent(true)
	cont.OnEvent(true)
	r.assertSeq(t, "IP")
	if got := g.MatterSwitchCurrentPosition(); got != switchPressedPosition {
		t.Fatalf("CurrentPosition while held = %d, want %d", got, switchPressedPosition)
	}
	release.OnEvent(true)
	r.assertSeq(t, "IP", "LP", "LR")
	if got := g.MatterSwitchCurrentPosition(); got != switchNeutralPosition {
		t.Fatalf("CurrentPosition after release = %d, want %d", got, switchNeutralPosition)
	}
}
