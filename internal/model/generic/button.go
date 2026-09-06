// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package generic

import (
	"context"
	"sync"

	"github.com/SukramJ/go-fabric/contract"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// Button represents a CCU button channel — a stateless trigger that
// always "sends true" when the user presses it. Unlike [Action], the
// Button parameter is a specific PRESS_* variant, and no type
// conversion occurs.
type Button struct {
	*DataPoint[bool]
}

// NewButton constructs a Button. Optimistic tracking is force-disabled
// because button presses are stateless triggers — the CCU never echoes a
// confirmation, so a tracker would always run into the rollback timeout.
func NewButton(cfg Spec) *Button {
	cfg.OptimisticDisabled = true
	b := &Button{DataPoint: NewDataPoint[bool](cfg)}
	b.RegisterService("press", func(ctx context.Context, _ map[string]any, priority hmenum.CommandPriority) error {
		return b.Press(ctx, priority)
	})
	return b
}

// Press fires the button.
func (b *Button) Press(ctx context.Context, priority hmenum.CommandPriority) error {
	if !b.IsWritable() && b.Descriptor.Type != hmenum.ParameterTypeAction {
		return ErrNotWritable
	}
	return b.sendAndObserve(ctx, true, true, priority)
}

// FireAction implements [ActionTrigger]. A button press is exactly the
// value-less trigger the interface describes.
func (b *Button) FireAction(ctx context.Context, priority hmenum.CommandPriority) error {
	return b.Press(ctx, priority)
}

// MatterMeasurementClass implements [interfaces.MatterMeasurementSource].
// Press-event parameters surface as MomentarySwitch (Matter §1.13
// GenericSwitch); other parameter shapes opt out by returning None.
func (b *Button) MatterMeasurementClass() interfaces.MatterMeasurementClass {
	if isPressParameter(hmenum.Parameter(b.Key.Parameter)) {
		return interfaces.MatterMeasurementMomentarySwitch
	}
	return interfaces.MatterMeasurementNone
}

// MatterSwitchPositions implements
// [cluster/wire.GenericSwitchSource]. HM buttons advertise two
// positions: idle + pressed. Multi-tap counters are tracked via the
// MultiPress* events, not the position attribute.
func (b *Button) MatterSwitchPositions() uint8 { return 2 }

// MatterSwitchSupportsLongPress implements
// [cluster/wire.GenericSwitchSource]. True when the source parameter
// is one of the long-press variants (PRESS_LONG, PRESS_LONG_START,
// PRESS_LONG_RELEASE, PRESS_CONT).
func (b *Button) MatterSwitchSupportsLongPress() bool {
	return isLongPressParameter(hmenum.Parameter(b.Key.Parameter))
}

// MatterSwitchEventEmitter is the receiver-side surface
// [ButtonGroup.WireMatterSwitchHandler] (and the single-DP
// Button / Action convenience wrappers) drive. The wire-side
// `cluster/wire.GenericSwitch` satisfies this contract by
// implementing the four Fire* methods directly.
//
// The contract itself lives in the port package so the model and the
// bridge name one identical type — an alias, not a copy, because Go
// interface satisfaction compares parameter types by identity and a
// second declaration of the same method set would not match.
type MatterSwitchEventEmitter = contract.SwitchEventEmitter

// WireMatterSwitchHandler subscribes the receiver to this Button's
// value-change stream and dispatches the Matter §1.13 GenericSwitch
// press-cycle events. Single-DP convenience wrapper: it wires a
// [ButtonGroup] of one, so a lone Button follows the same press-cycle
// state machine as a fully populated channel group. The Matter
// endpoint assembler does not use this path — it consolidates every
// press DP of a channel into one shared ButtonGroup instead.
func (b *Button) WireMatterSwitchHandler(h MatterSwitchEventEmitter) func() {
	return NewButtonGroup(b).WireMatterSwitchHandler(h)
}

// PressEventSource is the member surface a [ButtonGroup] aggregates:
// any press-parameter DP identified by its DataPointKey and observable
// through the type-erased update stream. *Button and *Action satisfy
// it, as does the device layer's ParameterDataPoint view, so the
// endpoint assembler can hand channel DPs over without knowing their
// concrete generic type.
type PressEventSource interface {
	DataPointKey() hmtypes.DataPointKey
	OnAnyUpdate(fn func(old, next any)) func()
}

// ButtonGroup consolidates every press-event parameter of ONE physical
// button (one device channel) behind a single Matter GenericSwitch
// (0x003B) cluster. A CCU splits one momentary button into several
// PRESS_* event parameters (PRESS_SHORT, PRESS_LONG, PRESS_CONT,
// PRESS_LONG_RELEASE, …) while Matter models the same button as ONE
// switch whose gesture is narrated as an ordered event sequence:
// InitialPress → ShortRelease for a short press, InitialPress →
// LongPress → LongRelease for a hold. Mirrors matter.js
// packages/node/src/behaviors/switch/SwitchServer.ts
// (#handleSwitchPositionChange + #handleLongPress), which derives that
// sequence from ONE currentPosition stream per switch. Materialising
// one cluster per PRESS_* parameter would split a single gesture
// across unrelated endpoints — every LongRelease orphaned by
// construction, no cluster ever emitting the full sequence — so the
// group runs one press-cycle state machine across all members.
//
// The CCU already applies the long-press threshold on the device (a
// PRESS_LONG only fires once the hold is long), so the matter.js
// longPressDelay timer collapses into direct event mapping here; the
// held/longEmitted flags replace its longPressTimer/currentIsLongPress
// pair.
type ButtonGroup struct {
	members      []pressMember
	supportsLong bool
	// hasRelease reports whether a PRESS_LONG_RELEASE member exists.
	// Only with a release parameter can the group hold a long-press
	// cycle open; without one every PRESS_LONG and every PRESS_CONT
	// must complete the cycle immediately (see transitionLocked).
	hasRelease bool

	// mu guards the press-cycle state below. Events are dispatched
	// outside the lock so an emitter can never dead-lock back into
	// the group.
	mu sync.Mutex
	// held is true between the press-start event of a long gesture
	// and its release.
	held bool
	// longEmitted is true once LongPress fired for the current hold —
	// device-side repeats (PRESS_CONT every ~300 ms, repeated
	// PRESS_LONG) are suppressed until the release closes the cycle.
	longEmitted bool
}

// pressMember pairs one press DP with its pre-resolved parameter.
type pressMember struct {
	param hmenum.Parameter
	src   PressEventSource
}

// NewButtonGroup builds the consolidated press source from the given
// member DPs. Members whose parameter is outside the press family are
// dropped; returns nil when no press member remains, so callers can
// pass an unfiltered candidate set.
func NewButtonGroup(members ...PressEventSource) *ButtonGroup {
	g := &ButtonGroup{}
	for _, m := range members {
		if m == nil {
			continue
		}
		p := hmenum.Parameter(m.DataPointKey().Parameter)
		if !isPressParameter(p) {
			continue
		}
		g.members = append(g.members, pressMember{param: p, src: m})
		if isLongPressParameter(p) {
			g.supportsLong = true
		}
		if p == hmenum.ParameterPressLongRelease {
			g.hasRelease = true
		}
	}
	if len(g.members) == 0 {
		return nil
	}
	return g
}

// MatterMeasurementClass implements [interfaces.MatterMeasurementSource]:
// the consolidated button projects as a MomentarySwitch endpoint.
func (g *ButtonGroup) MatterMeasurementClass() interfaces.MatterMeasurementClass {
	if g == nil {
		return interfaces.MatterMeasurementNone
	}
	return interfaces.MatterMeasurementMomentarySwitch
}

// MatterSwitchPositions implements [cluster/wire.GenericSwitchSource].
// One physical button: idle + pressed. Matches the NumberOfPositions
// "min 2" constraint and matter.js SwitchServer.ts's
// `numberOfPositions = 2` default.
func (g *ButtonGroup) MatterSwitchPositions() uint8 { return 2 }

// MatterSwitchSupportsLongPress implements
// [cluster/wire.GenericSwitchSource]. True when any member carries a
// long-press variant, flipping the cluster's MSL feature on. Feature
// conformance per matter.js
// packages/model/src/standard/elements/Switch.element.ts: MSL requires
// "[MS & (MSR | AS)]" — the bridge's MS+MSR base satisfies it.
func (g *ButtonGroup) MatterSwitchSupportsLongPress() bool {
	return g != nil && g.supportsLong
}

// MatterSwitchCurrentPosition implements the wire cluster's optional
// live-position capability: 1 (pressed) while a long-press cycle is
// held open, 0 (idle) otherwise. Mirrors matter.js SwitchServer.ts,
// where `state.currentPosition` rests at `momentaryNeutralPosition`
// (default 0) and moves to the pressed position for the duration of a
// press. Short presses collapse to a single dispatch (press + release
// arrive as one CCU event), so reads only ever observe 1 during a
// held long press.
func (g *ButtonGroup) MatterSwitchCurrentPosition() uint8 {
	if g == nil {
		return switchNeutralPosition
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.held {
		return switchPressedPosition
	}
	return switchNeutralPosition
}

// WireMatterSwitchHandler subscribes the receiver to every member's
// update stream and dispatches the Matter §1.13 press-cycle events
// through one shared state machine. Returns an idempotent unsubscribe
// closure covering all members.
//
// Parameter → event mapping (press-cycle state machine):
//
//	PRESS, PRESS_SHORT            → InitialPress + ShortRelease
//	PRESS_LONG, PRESS_LONG_START  → first of a hold: InitialPress +
//	                                LongPress; repeats while held are
//	                                suppressed. Without a
//	                                PRESS_LONG_RELEASE member the cycle
//	                                completes immediately as
//	                                InitialPress + LongPress + LongRelease.
//	PRESS_CONT                    → first of a hold: InitialPress
//	                                (LongPress is deferred to the
//	                                release); repeats while held are
//	                                suppressed. Without a
//	                                PRESS_LONG_RELEASE member the cycle
//	                                completes immediately as
//	                                InitialPress + LongPress + LongRelease.
//	PRESS_LONG_RELEASE            → LongRelease; synthesizes the missing
//	                                InitialPress / LongPress first when
//	                                the hold started with PRESS_CONT or
//	                                the press-start events were lost.
//
// Sequence contract per matter.js SwitchServer.ts: ShortRelease fires
// only when no LongPress was generated since the previous InitialPress;
// LongRelease fires only after a LongPress (#handleSwitchPositionChange,
// the shortRelease/longRelease branches). The synthesis on release keeps
// that contract even when the CCU never delivered the press-start frame.
func (g *ButtonGroup) WireMatterSwitchHandler(h MatterSwitchEventEmitter) func() {
	if g == nil || h == nil {
		return func() {}
	}
	unsubs := make([]func(), 0, len(g.members))
	for _, m := range g.members {
		param := m.param
		unsubs = append(unsubs, m.src.OnAnyUpdate(func(_, next any) {
			if !pressSignal(next) {
				// Only the rising edge is a press — HM presses are
				// momentary and the falling edge back to false is the
				// implicit reset, never a gesture of its own.
				return
			}
			g.dispatchPress(param, h)
		}))
	}
	return func() {
		for _, unsub := range unsubs {
			if unsub != nil {
				unsub()
			}
		}
	}
}

// Matter §1.13.5.2 CurrentPosition values for a two-position momentary
// switch. Neutral mirrors matter.js SwitchServer.ts
// `momentaryNeutralPosition` (default 0).
const (
	switchNeutralPosition uint8 = 0
	switchPressedPosition uint8 = 1
)

// switchPressEvent enumerates the Matter §1.13.6 events the press-cycle
// state machine can order into a gesture sequence.
type switchPressEvent uint8

const (
	pressEventInitial switchPressEvent = iota
	pressEventShortRelease
	pressEventLongPress
	pressEventLongRelease
)

// dispatchPress advances the press-cycle state machine for one member
// update and fires the resulting event sequence. State transitions run
// under the group mutex; the emitter is invoked after unlock so the
// event pipeline can never re-enter the group under its lock.
func (g *ButtonGroup) dispatchPress(param hmenum.Parameter, h MatterSwitchEventEmitter) {
	g.mu.Lock()
	seq := g.transitionLocked(param)
	g.mu.Unlock()
	for _, ev := range seq {
		switch ev {
		case pressEventInitial:
			h.FireInitialPress(switchPressedPosition)
		case pressEventShortRelease:
			// PreviousPosition is the position BEFORE the release —
			// the pressed position. Mirrors matter.js SwitchServer.ts
			// shortRelease/longRelease emits, which carry
			// `previousPosition: this.internal.previouslyReportedPosition`
			// (1 at release time).
			h.FireShortRelease(switchPressedPosition)
		case pressEventLongPress:
			h.FireLongPress(switchPressedPosition)
		case pressEventLongRelease:
			h.FireLongRelease(switchPressedPosition)
		}
	}
}

// transitionLocked computes the event sequence for one press update and
// mutates the hold state. Caller holds g.mu.
func (g *ButtonGroup) transitionLocked(param hmenum.Parameter) []switchPressEvent {
	switch param {
	case hmenum.ParameterPress, hmenum.ParameterPressShort:
		// A short press implies the button is up — when a hold is
		// still open its release frame was lost, so close the stale
		// cycle first to keep every InitialPress paired with exactly
		// one release.
		var seq []switchPressEvent
		if g.held {
			if g.longEmitted {
				seq = append(seq, pressEventLongRelease)
			} else {
				seq = append(seq, pressEventShortRelease)
			}
		}
		g.held, g.longEmitted = false, false
		return append(seq, pressEventInitial, pressEventShortRelease)

	case hmenum.ParameterPressLong, hmenum.ParameterPressLongStart:
		if !g.hasRelease {
			// No PRESS_LONG_RELEASE on this button: the device cannot
			// signal the hold end, so each PRESS_LONG is a complete
			// gesture. Emitting the closing LongRelease immediately
			// keeps the matter.js sequence contract (LongRelease after
			// every LongPress) for controllers that trigger on the
			// release.
			return []switchPressEvent{pressEventInitial, pressEventLongPress, pressEventLongRelease}
		}
		if !g.held {
			g.held, g.longEmitted = true, true
			return []switchPressEvent{pressEventInitial, pressEventLongPress}
		}
		if !g.longEmitted {
			// Hold was opened by PRESS_CONT; the explicit long-press
			// frame upgrades it now instead of at release time.
			g.longEmitted = true
			return []switchPressEvent{pressEventLongPress}
		}
		// Device-side repeat within one hold — suppressed; the cycle
		// closes on PRESS_LONG_RELEASE.
		return nil

	case hmenum.ParameterPressCont:
		if !g.hasRelease {
			// No PRESS_LONG_RELEASE on this button (HM-Sen-DB-PCB
			// channel 1 declares PRESS_SHORT + PRESS_CONT only): the
			// device can never signal the hold end, so holding the
			// cycle open would latch the group forever — no LongPress,
			// no LongRelease, every later continuation suppressed.
			// Each PRESS_CONT is therefore a complete gesture, exactly
			// as the PRESS_LONG branch above resolves the same gap.
			return []switchPressEvent{pressEventInitial, pressEventLongPress, pressEventLongRelease}
		}
		if g.held {
			// BidCos repeats PRESS_CONT roughly every 300 ms while the
			// button stays down. One physical hold is ONE Matter
			// gesture — repeats are suppressed until the release.
			return nil
		}
		// Hold opened by a continuation frame: report the press start
		// now, defer LongPress so the release can decide whether an
		// explicit PRESS_LONG already covered it.
		g.held, g.longEmitted = true, false
		return []switchPressEvent{pressEventInitial}

	case hmenum.ParameterPressLongRelease:
		// Close the cycle. Synthesize whatever prefix of the
		// InitialPress → LongPress → LongRelease sequence has not been
		// emitted for this hold: a hold opened by PRESS_CONT still owes
		// the LongPress, an orphaned release (press-start frames lost,
		// or the daemon restarted mid-hold) owes both.
		var seq []switchPressEvent
		if !g.held {
			seq = append(seq, pressEventInitial)
		}
		if !g.longEmitted {
			seq = append(seq, pressEventLongPress)
		}
		seq = append(seq, pressEventLongRelease)
		g.held, g.longEmitted = false, false
		return seq

	default:
		// Group members are pre-filtered to the press family; any other
		// parameter reaching this hook is ignored.
		return nil
	}
}

// isPressParameter reports whether p belongs to the press family that
// projects onto the Matter GenericSwitch cluster. PRESS_LOCK /
// PRESS_UNLOCK are click events too but carry keymatic lock semantics,
// not momentary-switch semantics, and stay outside the projection.
func isPressParameter(p hmenum.Parameter) bool {
	switch p {
	case hmenum.ParameterPress, hmenum.ParameterPressShort,
		hmenum.ParameterPressLong, hmenum.ParameterPressLongStart,
		hmenum.ParameterPressLongRelease, hmenum.ParameterPressCont:
		return true
	default:
		return false
	}
}

// isLongPressParameter reports whether p participates in the long-press
// cycle (MSL feature evidence).
func isLongPressParameter(p hmenum.Parameter) bool {
	switch p {
	case hmenum.ParameterPressLong, hmenum.ParameterPressLongStart,
		hmenum.ParameterPressLongRelease, hmenum.ParameterPressCont:
		return true
	default:
		return false
	}
}

// pressSignal reports whether a type-erased DP update announces a
// press. Button members deliver bool `true` per press; ACTION-typed
// members may carry a non-bool payload where any non-nil value counts
// as a fired press.
func pressSignal(next any) bool {
	switch v := next.(type) {
	case nil:
		return false
	case bool:
		return v
	default:
		return true
	}
}
