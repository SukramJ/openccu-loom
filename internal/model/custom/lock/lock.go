// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package lock implements the lock custom data point.
//
// The CCU exposes two distinct lock families: HmIP locks driven
// through LOCK_TARGET_LEVEL (ENUM) plus LOCK_STATE / DIRECTION
// sensors, and RF / Button locks driven through STATE / OPEN
// booleans. A single [Lock] type covers both by carrying a [Kind]
// tag. Read-side observations point at the channel's existing
// generic data points (LOCK_STATE, DIRECTION, ERROR_JAMMED) — Lock
// holds typed references rather than duplicate instances. The write
// path dispatches to the kind-specific wire parameter through the
// configured Writer.
package lock

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// unlockEventCapacity is the maximum number of unlock events retained
// in the ring buffer exposed by [Lock.UnlockEvents].
const unlockEventCapacity = 10

// UnlockEvent records a single successful unlock command with a
// wall-clock timestamp. The list is ordered from oldest to newest.
type UnlockEvent struct {
	At time.Time
}

// Writer is an alias for [generic.Writer].
type Writer = generic.Writer

// State enumerates the high-level lock state. Values.
// MQTT-Discovery `state_locked: "LOCKED"` etc. mappings match the
// `value_template`-extracted JSON values 1:1. HA's MQTT-Lock
// platform compares strings case-sensitively; emitting lowercase
// here would render every lock entity as `unknown` in HA.
type State string

// State values.
const (
	StateUnknown  State = "UNKNOWN"
	StateLocked   State = "LOCKED"
	StateUnlocked State = "UNLOCKED"
)

// Direction reports whether the motor is currently moving. Values
type Direction string

// Direction values. The CCU DIRECTION parameter uses "DOWN" for a
// locking movement and "UP" for an unlocking movement.
const (
	DirectionNone    Direction = ""
	DirectionLocking Direction = "DOWN"
	DirectionUnlock  Direction = "UP"
)

// LockError enumerates the error states the CCU reports on the ERROR_JAMMED /
// ERROR parameter for locks. The type is exported (unlike Python's private
// underscore prefix) because Go consumers need it for per-error
// classification.
type LockError string //nolint:revive // "Error" would shadow the built-in error interface in this package

// LockError values.
const (
	LockErrorNoError      LockError = "NO_ERROR"
	LockErrorClutchFail   LockError = "CLUTCH_FAILURE"
	LockErrorMotorAborted LockError = "MOTOR_ABORTED"
)

// Kind discriminates between HmIP and RF locks.
type Kind int

// Kind values.
const (
	KindIP Kind = iota
	KindRF
	KindButton
)

// ErrNotSupported is returned when an operation is unavailable for
// the current Kind + Capabilities combination.
var ErrNotSupported = errors.New("lock: operation not supported")

// Lock is a lockable device.
type Lock struct {
	custom.BaseDP

	Address      string
	Capabilities custom.LockCapabilities
	Kind         Kind

	// ServiceRegistry implements the write-half of [payload.Source].
	// Service methods are registered in [New].
	payload.ServiceRegistry

	// dataVersion tracks the per-cluster monotonic counter (Matter
	// §10.6.5). Bumped on every CCU-confirmed wire-DP change so
	// DataVersionFilter evaluation correctly detects cluster changes
	// from physical operation at the device.
	dataVersion hmtypes.DataVersionTracker

	// key is the composite data-point key used by [DataPointKey] to
	// satisfy [device.AttachableDataPoint]. Keyed on the primary write
	// parameter for the kind (LOCK_TARGET_LEVEL for IP, STATE for RF/Button).
	key hmtypes.DataPointKey

	writer Writer

	// unlockMu guards unlockHistory.
	unlockMu      sync.Mutex
	unlockHistory []UnlockEvent

	// IP-locks expose LOCK_STATE and DIRECTION as read-only ENUM
	// parameters, which the resolver projects onto raw-index Sensor[int32]
	// slots; the string label is resolved on read via
	// [custom.EnumLabelValue]. RF / Button locks expose only a boolean
	// STATE plus the optional jam flag — the CCU does not report a
	// separate direction parameter.
	stateDp     *generic.Sensor[int32]
	directionDp *generic.Sensor[int32]
	jammedDp    *generic.BinarySensor
	// rfErrorDp carries the HM-Sec-Key (RF lock) ERROR parameter (a
	// read-only ENUM, so also an index sensor). RF locks report jammed
	// state as ERROR != "NO_ERROR" rather than through a binary
	// ERROR_JAMMED flag.
	rfErrorDp *generic.Sensor[int32]
	// boolStateDp carries the RF / Button bool wire value. Semantics
	// differ per kind: RF STATE reads false="locked" / true="unlocked",
	// while the button-lock parameter (GLOBAL_BUTTON_LOCK) reads
	// true="locked" (keys disabled) / false="unlocked".
	boolStateDp *generic.Switch
	// buttonParam is the resolved wire parameter for KindButton —
	// GLOBAL_BUTTON_LOCK on every shipping device, BUTTON_LOCK kept as
	// fallback. The DataPointKey deliberately stays "BUTTON_LOCK" so the
	// CDP name (REST/MQTT identity, HA postfix matching) is stable.
	buttonParam hmenum.Parameter
}

// Config is the constructor record. Channel must already carry the
// LOCK_STATE / DIRECTION / ERROR_JAMMED data points; absent fields
// degrade to "(zero, false)" on the corresponding accessors.
type Config struct {
	Channel      *device.Channel
	Writer       Writer
	Capabilities custom.LockCapabilities
	Kind         Kind
}

// New constructs a Lock.
func New(cfg Config) *Lock {
	address := ""
	var key hmtypes.DataPointKey
	if cfg.Channel != nil {
		address = cfg.Channel.Address
		// Choose the primary write parameter for the key so the
		// materializer can attach this DP unambiguously.
		var keyParam hmenum.Parameter
		switch cfg.Kind {
		case KindIP:
			keyParam = hmenum.ParameterLockTargetLevel
		case KindButton:
			keyParam = hmenum.ParameterButtonLock
		default: // KindRF
			keyParam = hmenum.ParameterState
		}
		key = hmtypes.DataPointKey{
			ChannelAddress: address,
			Parameter:      string(keyParam),
		}
	}
	l := &Lock{
		Address:      address,
		Capabilities: cfg.Capabilities,
		Kind:         cfg.Kind,
		key:          key,
		writer:       cfg.Writer,
		jammedDp:     custom.BinarySensorField(cfg.Channel, hmenum.ParameterErrorJammed),
	}
	// RF locks (HM-Sec-Key family) carry a string ERROR parameter instead of
	// the binary ERROR_JAMMED that IP/Button locks use.
	if cfg.Kind == KindRF {
		l.rfErrorDp = custom.EnumSensorField(cfg.Channel, hmenum.ParameterError)
	}
	// DIRECTION is a read-only ENUM reporting which way the motor last
	// turned. The CCU exposes it on the HM key-matic family (channel 1),
	// not on the HmIP door locks — the opposite of what the branches
	// below assumed, which left the field nil on every device. Resolving
	// it for every kind lets the accessor decide from the wire.
	l.directionDp = custom.EnumSensorField(cfg.Channel, hmenum.ParameterDirection)
	switch cfg.Kind {
	case KindIP:
		// HmIP locks: LOCK_STATE (read-only ENUM).
		l.stateDp = custom.EnumSensorField(cfg.Channel, hmenum.ParameterLockState)
	case KindRF:
		// RF locks: bool STATE only — the CCU exposes no LOCK_STATE here.
		l.boolStateDp = custom.SwitchField(cfg.Channel, hmenum.ParameterState)
	case KindButton:
		// Button locks: the wire parameter is GLOBAL_BUTTON_LOCK (a
		// MASTER-paramset bool on ch0 that both the IP and RF button-lock
		// profiles resolve their BUTTON_LOCK field to). BUTTON_LOCK is
		// kept as a fallback for paramsets that carry it literally.
		l.buttonParam = hmenum.ParameterGlobalButtonLock
		l.boolStateDp = custom.SwitchField(cfg.Channel, hmenum.ParameterGlobalButtonLock)
		if l.boolStateDp == nil {
			l.buttonParam = hmenum.ParameterButtonLock
			l.boolStateDp = custom.SwitchField(cfg.Channel, hmenum.ParameterButtonLock)
		}
	}
	// Matter §10.6.5: DataVersion advances on every CCU-confirmed wire-DP
	// change. Physical operation at the device updates LOCK_STATE / STATE /
	// BUTTON_LOCK without going through LockInvoke, so each wire slot needs
	// its own hook to guarantee DataVersionFilter sees the change.
	if l.stateDp != nil {
		_ = l.stateDp.OnConfirmedUpdate(func(_, _ int32) { l.dataVersion.Bump() })
	}
	if l.directionDp != nil {
		_ = l.directionDp.OnConfirmedUpdate(func(_, _ int32) { l.dataVersion.Bump() })
	}
	if l.boolStateDp != nil {
		_ = l.boolStateDp.OnConfirmedUpdate(func(_, _ bool) { l.dataVersion.Bump() })
	}
	if l.jammedDp != nil {
		_ = l.jammedDp.OnConfirmedUpdate(func(_, _ bool) { l.dataVersion.Bump() })
	}
	if l.rfErrorDp != nil {
		_ = l.rfErrorDp.OnConfirmedUpdate(func(_, _ int32) { l.dataVersion.Bump() })
	}
	l.registerServices()
	return l
}

// MatterDataVersion returns the current Matter cluster DataVersion counter.
// Implements [interfaces.MatterClusterDataVersion] via [matter.go].
func (l *Lock) MatterDataVersion() uint32 { return l.dataVersion.Current() }

// DataPointKey returns the composite identifier used by the materializer
// to attach this custom DP to its primary channel. Satisfies
// [device.AttachableDataPoint].
func (l *Lock) DataPointKey() hmtypes.DataPointKey { return l.key }

// Category reports the HA data-point category — clients spawn the
// entity off this value (lock platform).
func (l *Lock) Category() hmenum.DataPointCategory { return hmenum.DataPointCategoryLock }

// IgnoreMultipleChannelsForName opts the Lock custom DP out of the
// multi-primary `ch<N>` naming suffix. Multi-channel locks render as
// "<Lock>" / "<Lock>" instead of "<Lock> ch1" / "<Lock> ch2".
func (*Lock) IgnoreMultipleChannelsForName() bool { return true }

// aggregate exposes Lock's wire-backed slots to the
// [custom.AggregateView] helper. IP locks contribute LOCK_STATE +
// DIRECTION; RF / Button locks contribute the bool STATE. The
// jam indicator (ERROR_JAMMED for IP/Button, ERROR for RF) is
// included for all families.
func (l *Lock) aggregate() custom.AggregateView {
	slots := make([]custom.AggregateSlot, 0, 4)
	if l.stateDp != nil {
		slots = append(slots, l.stateDp)
	}
	if l.directionDp != nil {
		slots = append(slots, l.directionDp)
	}
	if l.boolStateDp != nil {
		slots = append(slots, l.boolStateDp)
	}
	if l.jammedDp != nil {
		slots = append(slots, l.jammedDp)
	}
	if l.rfErrorDp != nil {
		slots = append(slots, l.rfErrorDp)
	}
	return custom.AggregateStatus(slots...)
}

// IsRefreshed reports whether at least one of the lock's wire slots
// has been observed.
func (l *Lock) IsRefreshed() bool { return l.aggregate().IsRefreshed() }

// StateUncertain reports whether any of the lock's wire slots is in
// the optimistic-update window.
func (l *Lock) StateUncertain() bool { return l.aggregate().StateUncertain() }

// IsStatusValid reports whether all wire-level slots have a valid STATUS
// parameter state (no OVERFLOW / ERROR).
func (l *Lock) IsStatusValid() bool { return l.aggregate().IsStatusValid() }

// SubDataPointKeys returns the wire identifiers of every wire-level
// slot.
func (l *Lock) SubDataPointKeys() []hmtypes.DataPointKey {
	return l.aggregate().SubDataPointKeys()
}

// --- state accessors ---

// LockState returns the current lock state. IP locks read LOCK_STATE
// (string enum); RF / Button locks invert the bool STATE wire
// Value (false → locked, true → unlocked) — mirrors
// `CustomDpRfLock` semantics.
func (l *Lock) LockState() (State, bool) {
	if l.boolStateDp != nil {
		v, ok := l.boolStateDp.Value()
		if !ok {
			return StateUnknown, false
		}
		// Button locks invert the RF STATE semantics:
		// GLOBAL_BUTTON_LOCK=true means the keys are locked,
		// while RF STATE reads true as "unlocked".
		if l.Kind == KindButton {
			if v {
				return StateLocked, true
			}
			return StateUnlocked, true
		}
		if v {
			return StateUnlocked, true
		}
		return StateLocked, true
	}
	s, ok := custom.EnumLabelValue(l.stateDp)
	if !ok {
		return StateUnknown, false
	}
	switch s {
	case "":
		return StateUnknown, true
	case string(StateLocked):
		return StateLocked, true
	case string(StateUnlocked):
		return StateUnlocked, true
	}
	return State(s), true
}

// IsLocked reports whether the lock is currently locked.
func (l *Lock) IsLocked() (locked, observed bool) {
	s, ok := l.LockState()
	return s == StateLocked, ok
}

// Direction returns the current motor direction.
func (l *Lock) Direction() (Direction, bool) {
	d, ok := custom.EnumLabelValue(l.directionDp)
	if !ok {
		return DirectionNone, false
	}
	return Direction(d), true
}

// IsLocking reports whether the motor is currently moving toward the
// locked position.
func (l *Lock) IsLocking() bool {
	d, ok := l.Direction()
	if !ok {
		return false
	}
	return d == DirectionLocking
}

// IsUnlocking reports whether the motor is currently moving toward the
// unlocked position.
func (l *Lock) IsUnlocking() bool {
	d, ok := l.Direction()
	if !ok {
		return false
	}
	return d == DirectionUnlock
}

// NamePostfix returns the suffix appended to a lock's data-point name.
// The default Lock has no postfix; ButtonLock (Kind=KindButton) returns
// "BUTTON_LOCK" (uppercase, matching the wire parameter name). HA's
// unique_id hash is case-sensitive, so a lowercase postfix would produce
// a different entity slug than the Python reference and re-register the
// entity under a new ID across stacks.
func (l *Lock) NamePostfix() string {
	if l.Kind == KindButton {
		return "BUTTON_LOCK"
	}
	return ""
}

// IsJammed reports whether the lock is in a jammed state.
//
// RF locks (KindRF) use the ERROR string parameter: any non-empty value
// other than "NO_ERROR" is treated as jammed. All other lock kinds use the
// binary ERROR_JAMMED parameter.
func (l *Lock) IsJammed() bool {
	if l.Kind == KindRF && l.rfErrorDp != nil {
		v, ok := custom.EnumLabelValue(l.rfErrorDp)
		if !ok {
			return false
		}
		return v != "" && v != string(LockErrorNoError)
	}
	if l.jammedDp == nil {
		return false
	}
	v, _ := l.jammedDp.IsOn()
	return v
}

// Subscribe wires the channel's lock-state parameters into the Lock
// so the device.SubscribingDataPoint contract is satisfied. Lock has
// no hot-path aggregate cache — State() / Direction() / IsJammed()
// read directly from the wire DPs — so the OnAnyUpdate hooks are
// no-ops. They only need to exist so the channel records an
// OnAnyUpdate registration for the EventBridge's
// publishCustomDPState path to re-fire on every wire-side change.
// Implements [device.SubscribingDataPoint].
func (l *Lock) Subscribe(ch *device.Channel) func() {
	if ch == nil {
		return func() {}
	}
	var unsubs []func()
	// Each branch checks the concrete pointer (not an interface
	// wrapper) — a typed-nil *generic.Sensor[string] / *generic.Switch
	// would slip past an `interface{} != nil` test and panic on dispatch.
	if l.stateDp != nil {
		unsubs = append(unsubs, l.stateDp.OnAnyUpdate(func(_, _ any) {}))
	}
	if l.directionDp != nil {
		unsubs = append(unsubs, l.directionDp.OnAnyUpdate(func(_, _ any) {}))
	}
	if l.boolStateDp != nil {
		unsubs = append(unsubs, l.boolStateDp.OnAnyUpdate(func(_, _ any) {}))
	}
	if l.jammedDp != nil {
		unsubs = append(unsubs, l.jammedDp.OnAnyUpdate(func(_, _ any) {}))
	}
	if l.rfErrorDp != nil {
		unsubs = append(unsubs, l.rfErrorDp.OnAnyUpdate(func(_, _ any) {}))
	}
	return func() {
		for _, u := range unsubs {
			if u != nil {
				u()
			}
		}
	}
}

// --- commands ---

// Lock locks the lock.
func (l *Lock) Lock(ctx context.Context, priority hmenum.CommandPriority) error {
	return l.send(ctx, commandLock, priority)
}

// Unlock unlocks the lock.
func (l *Lock) Unlock(ctx context.Context, priority hmenum.CommandPriority) error {
	return l.send(ctx, commandUnlock, priority)
}

// Open opens the lock (releases the latch). Requires
// [custom.LockCapabilities.SupportsOpen].
func (l *Lock) Open(ctx context.Context, priority hmenum.CommandPriority) error {
	if !l.Capabilities.SupportsOpen {
		return ErrNotSupported
	}
	return l.send(ctx, commandOpen, priority)
}

type command int

const (
	commandLock command = iota
	commandUnlock
	commandOpen
)

func (l *Lock) send(ctx context.Context, cmd command, priority hmenum.CommandPriority) error {
	ctx = custom.EnsureContext(ctx)
	switch l.Kind {
	case KindIP:
		return l.sendIP(ctx, cmd, priority)
	case KindRF:
		return l.sendRF(ctx, cmd, priority)
	case KindButton:
		return l.sendButton(ctx, cmd, priority)
	}
	return ErrNotSupported
}

// IP locks use LOCK_TARGET_LEVEL with the ENUM string labels below.
// The CCU XML-RPC interface accepts both integer indices and string labels
// for ENUM parameters. String labels are used here for parity with the
// reference implementation and for clarity in CCU logs.
const (
	ipTargetLocked   = "LOCKED"
	ipTargetUnlocked = "UNLOCKED"
	ipTargetOpen     = "OPEN"
)

func (l *Lock) sendIP(ctx context.Context, cmd command, priority hmenum.CommandPriority) error {
	var label string
	switch cmd {
	case commandLock:
		label = ipTargetLocked
	case commandUnlock:
		label = ipTargetUnlocked
	case commandOpen:
		label = ipTargetOpen
	}
	if err := l.writer.SetValue(ctx, l.Address, hmenum.ParameterLockTargetLevel, label, priority); err != nil {
		return fmt.Errorf("lock: IP command %d: %w", cmd, err)
	}
	l.observeCommand(cmd)
	if cmd == commandUnlock || cmd == commandOpen {
		l.recordUnlock()
	}
	return nil
}

func (l *Lock) sendRF(ctx context.Context, cmd command, priority hmenum.CommandPriority) error {
	// KindRF uses the bool STATE parameter: false locks, true unlocks.
	switch cmd {
	case commandLock:
		if err := l.writer.SetValue(ctx, l.Address, hmenum.ParameterState, false, priority); err != nil {
			return fmt.Errorf("lock: send lock: %w", err)
		}
	case commandUnlock:
		if err := l.writer.SetValue(ctx, l.Address, hmenum.ParameterState, true, priority); err != nil {
			return fmt.Errorf("lock: send unlock: %w", err)
		}
	case commandOpen:
		if err := l.writer.SetValue(ctx, l.Address, hmenum.ParameterOpen, true, priority); err != nil {
			return fmt.Errorf("lock: send open: %w", err)
		}
	}
	l.observeCommand(cmd)
	if cmd == commandUnlock || cmd == commandOpen {
		l.recordUnlock()
	}
	return nil
}

func (l *Lock) sendButton(ctx context.Context, cmd command, priority hmenum.CommandPriority) error {
	// Button locks write the resolved button parameter
	// (GLOBAL_BUTTON_LOCK): lock → true (keys disabled),
	// unlock → false. The parameter lives in the
	// MASTER paramset on every shipping device, so the write must go
	// through put_paramset — setValue on a MASTER parameter faults
	// with XML-RPC -5 "Invalid parameter or value".
	var value bool
	switch cmd {
	case commandLock:
		value = true
	case commandUnlock:
		value = false
	case commandOpen:
		return ErrNotSupported
	}
	if err := l.writeButtonParam(ctx, value, priority); err != nil {
		verb := "lock"
		if cmd == commandUnlock {
			verb = "unlock"
		}
		return fmt.Errorf("lock: send %s: %w", verb, err)
	}
	l.observeCommand(cmd)
	return nil
}

// writeButtonParam routes the button-lock write through the paramset
// the resolved DP lives in: MASTER values go through put_paramset,
// VALUES parameters through the regular setValue path.
func (l *Lock) writeButtonParam(ctx context.Context, value bool, priority hmenum.CommandPriority) error {
	isMaster := l.boolStateDp != nil &&
		l.boolStateDp.DataPointKey().ParamsetKey == hmenum.ParamsetKeyMaster
	if isMaster {
		if pw, ok := l.writer.(generic.ParamsetWriter); ok {
			return pw.PutParamset(
				ctx,
				l.Address,
				hmenum.ParamsetKeyMaster,
				map[string]any{string(l.buttonParam): value},
				priority,
			)
		}
	}
	return l.writer.SetValue(ctx, l.Address, l.buttonParam, value, priority)
}

func (l *Lock) observeCommand(cmd command) {
	// Optimistic echo: synthesise LOCK_STATE / STATE immediately after a
	// write so [State] / [IsLocked] reflect the desired post-write value
	// before the CCU push-callback arrives. The Python reference does not do
	// this — it relies solely on the CCU push for state updates. The
	// optimistic path here improves responsiveness for callers (e.g. the
	// MQTT bridge) that poll State right after a write command; it is
	// harmless because the next CCU push overwrites the synthesised value.
	// Documented as a deliberate divergence in notes/parity/by_design.md.
	if l.stateDp != nil {
		var label string
		switch cmd {
		case commandLock:
			label = string(StateLocked)
		case commandUnlock, commandOpen:
			label = string(StateUnlocked)
		}
		if idx, ok := custom.EnumLabelIndex(l.stateDp, label); ok {
			l.stateDp.OnEvent(idx)
		}
		return
	}
	if l.boolStateDp != nil {
		// Button locks invert the RF STATE wire semantics: true means
		// "locked" (GLOBAL_BUTTON_LOCK active).
		locked, unlocked := false, true
		if l.Kind == KindButton {
			locked, unlocked = true, false
		}
		switch cmd {
		case commandLock:
			l.boolStateDp.OnEvent(locked)
		case commandUnlock, commandOpen:
			l.boolStateDp.OnEvent(unlocked)
		}
	}
}

// recordUnlock appends a new entry to the unlock-event ring buffer.
// Older entries beyond unlockEventCapacity are evicted.
func (l *Lock) recordUnlock() {
	l.unlockMu.Lock()
	defer l.unlockMu.Unlock()
	l.unlockHistory = append(l.unlockHistory, UnlockEvent{At: time.Now()})
	if len(l.unlockHistory) > unlockEventCapacity {
		l.unlockHistory = l.unlockHistory[len(l.unlockHistory)-unlockEventCapacity:]
	}
}

// UnlockEvents returns a copy of the unlock-event ring buffer ordered from
// oldest to newest. The slice contains at most [unlockEventCapacity] entries
// and is empty until the first successful Unlock command.
func (l *Lock) UnlockEvents() []UnlockEvent {
	l.unlockMu.Lock()
	defer l.unlockMu.Unlock()
	if len(l.unlockHistory) == 0 {
		return nil
	}
	out := make([]UnlockEvent, len(l.unlockHistory))
	copy(out, l.unlockHistory)
	return out
}
