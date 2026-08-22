// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package combined

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/datapoint"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// EnumSelectMode pairs one selectable state with the command that
// reaches it.
//
// The two halves are separate because on the CCU they are separate
// parameters: a garage drive reports CLOSED / OPEN / VENTILATION_POSITION
// on DOOR_STATE but accepts CLOSE / OPEN / PARTIAL_OPEN on DOOR_COMMAND,
// and only one of the three pairs shares a name. Declaring the pair in
// one place is what keeps the read and write halves from drifting.
type EnumSelectMode struct {
	// State is the VALUE_LIST token the read parameter reports.
	State string
	// Command is the value written to the command parameter to reach
	// State.
	Command string
}

// EnumSelectConfig is the constructor record for [EnumSelect].
type EnumSelectConfig struct {
	// Address is the channel address both parameters live on.
	Address string
	// CentralName scopes the data point's identity (ADR 0002).
	CentralName string
	// Writer dispatches the command parameter write.
	Writer Writer
	// Kind is the projection kind — the retained topic segment and the
	// object_id suffix ("door_mode").
	Kind string
	// LabelKey names the daemon catalogue entry for the entity label.
	LabelKey string
	// CombinedParameter is the synthetic parameter name forming the data
	// point's identity. It must not collide with either wire parameter,
	// so the combined DP can attach to the same channel.
	CombinedParameter hmenum.Parameter
	// StateParameter is the read-only ENUM reporting the current state.
	StateParameter hmenum.Parameter
	// CommandParameter is the parameter a mode change is written to.
	CommandParameter hmenum.Parameter
	// Modes lists the selectable modes in presentation order.
	Modes []EnumSelectMode
	// InterfaceID completes the DataPointKey. Optional.
	InterfaceID string
}

// EnumSelect projects a state parameter plus a command parameter onto one
// selectable mode.
//
// It exists because some devices report a discrete state on one parameter
// and accept the matching command on another, with no single parameter an
// operator can both read and set. A garage drive is the case that drove
// it: its ventilation position was reachable only by writing a cover
// position between two thresholds, which no north-bound surface can
// discover or label. As one combined data point it is a first-class
// three-way control.
//
// While the device is travelling the state parameter typically reports a
// token that is not a selectable mode (POSITION_UNKNOWN on a garage
// drive). Dropping to "unknown" on every movement would make the control
// flicker, so the last commanded mode is held until the device reports a
// state again. [EnumSelect.NoteCommand] is how a parent custom DP keeps
// that hold honest when it writes the command parameter itself.
type EnumSelect struct {
	datapoint.BaseDataPointFields

	Address string
	Writer  Writer

	kind              string
	labelKey          string
	combinedParameter hmenum.Parameter
	stateParameter    hmenum.Parameter
	commandParameter  hmenum.Parameter
	interfaceID       string

	// modes preserves presentation order; stateToCommand and
	// commandToState are the two lookup directions over the same pairs.
	modes          []EnumSelectMode
	stateToCommand map[string]string
	commandToState map[string]string

	mu sync.RWMutex
	// observed is the last state token the device reported that is also a
	// selectable mode; held is the optimistic value carried while the
	// device reports a non-mode token.
	observed string
	held     string
	current  string
	hasValue bool

	callbacks []func(old, next string)
}

// NewEnumSelect constructs an EnumSelect. It returns nil when the config
// declares no modes — a select with nothing to select is a control that
// renders and does nothing, which is worse than an absent one.
func NewEnumSelect(cfg EnumSelectConfig) *EnumSelect {
	if len(cfg.Modes) == 0 || cfg.Kind == "" || cfg.CombinedParameter == "" {
		return nil
	}
	e := &EnumSelect{
		BaseDataPointFields: datapoint.NewBaseDataPointFields(
			cfg.CentralName, cfg.Address, "COMBINED/"+string(cfg.CombinedParameter),
		),
		Address:           cfg.Address,
		Writer:            cfg.Writer,
		kind:              cfg.Kind,
		labelKey:          cfg.LabelKey,
		combinedParameter: cfg.CombinedParameter,
		stateParameter:    cfg.StateParameter,
		commandParameter:  cfg.CommandParameter,
		interfaceID:       cfg.InterfaceID,
		modes:             slices.Clone(cfg.Modes),
		stateToCommand:    make(map[string]string, len(cfg.Modes)),
		commandToState:    make(map[string]string, len(cfg.Modes)),
	}
	for _, m := range cfg.Modes {
		e.stateToCommand[m.State] = m.Command
		e.commandToState[m.Command] = m.State
	}
	// Deliberately not forced to NoCreate, unlike the combined data
	// points that exist only to feed their parent custom DP. This one is
	// the operator's control: NoCreate would drop it from the Matter
	// candidate set (ADR 0049 admits data_point / ce_primary /
	// ce_visible only) and mark it internal on every other surface.
	return e
}

// IsCombined satisfies the [device.CombinedDataPoint] marker so
// Channel.CombinedDataPoints surfaces this data point.
func (e *EnumSelect) IsCombined() bool { return true }

// DataPointKey returns the combined DP's identity, satisfying
// [device.AttachableDataPoint].
func (e *EnumSelect) DataPointKey() hmtypes.DataPointKey {
	return hmtypes.DataPointKey{
		InterfaceID:    e.interfaceID,
		ChannelAddress: e.Address,
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Parameter:      string(e.combinedParameter),
	}
}

// Modes returns the selectable state tokens in presentation order.
func (e *EnumSelect) Modes() []string {
	out := make([]string, 0, len(e.modes))
	for _, m := range e.modes {
		out = append(out, m.State)
	}
	return out
}

// StateParameter reports the read parameter, so a caller localising the
// mode list knows whose VALUE_LIST the tokens come from.
func (e *EnumSelect) StateParameter() hmenum.Parameter { return e.stateParameter }

// Value returns the current mode and whether one has been established —
// either observed from the device or held from a command this data point
// dispatched.
func (e *EnumSelect) Value() (string, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.current, e.hasValue
}

// enumWireDataPoint is the narrow contract [EnumSelect.Subscribe] needs
// from the channel's read parameter. Every generic.DataPoint[T] satisfies
// it.
type enumWireDataPoint interface {
	RawValue() (any, bool)
	OnAnyUpdate(fn func(old, next any)) func()
	ParameterData() hmproto.ParameterData
}

// Subscribe wires the read parameter's updates into the mode value.
//
// Satisfies [device.SubscribingDataPoint]; channels invoke it from
// AttachCalculatedDataPoint. Returns nil when the read parameter is
// absent, which leaves the data point valueless rather than reporting a
// mode it cannot observe.
func (e *EnumSelect) Subscribe(ch *device.Channel) func() {
	if ch == nil {
		return nil
	}
	stateDP, _ := any(ch.Parameter(e.stateParameter)).(enumWireDataPoint)
	if stateDP == nil {
		return nil
	}
	push := func() {
		raw, ok := stateDP.RawValue()
		if !ok {
			return
		}
		e.OnState(resolveEnumToken(raw, stateDP.ParameterData().ValueList))
	}
	unsub := stateDP.OnAnyUpdate(func(_, _ any) { push() })
	// Seed immediately: after a cache hydration the value is already
	// there and no further update is coming.
	push()
	return unsub
}

// resolveEnumToken maps a raw wire value onto its VALUE_LIST token. A
// read-only ENUM arrives as a 0-based index projected onto an integer
// sensor, so the index has to be resolved against the list; a device that
// already reports the token passes through.
func resolveEnumToken(raw any, valueList []string) string {
	switch v := raw.(type) {
	case string:
		return v
	case int32:
		return enumTokenAt(valueList, int(v))
	case int64:
		return enumTokenAt(valueList, int(v))
	case int:
		return enumTokenAt(valueList, v)
	case float64:
		return enumTokenAt(valueList, int(v))
	default:
		return ""
	}
}

func enumTokenAt(valueList []string, idx int) string {
	if idx < 0 || idx >= len(valueList) {
		return ""
	}
	return valueList[idx]
}

// OnState records a device-reported state token. A token that is not a
// selectable mode (the travelling state) leaves the held value in place.
func (e *EnumSelect) OnState(token string) {
	e.mu.Lock()
	if _, isMode := e.stateToCommand[token]; isMode {
		e.observed = token
		e.held = ""
	} else {
		e.observed = ""
	}
	old, next, changed := e.recomputeLocked()
	e.mu.Unlock()
	if changed {
		e.fire(old, next)
	}
}

// NoteCommand records a command written to the command parameter outside
// this data point.
//
// A parent custom DP writes that parameter for its own operations (a
// garage cover's open / close / stop). Without this hook the held mode
// would keep reporting a mode the device is no longer heading for — and
// would do so indefinitely after a stop, because the device then sits at
// a non-mode state with no further event to correct it. A command with no
// resulting mode clears the hold instead of leaving a stale one.
func (e *EnumSelect) NoteCommand(command string) {
	e.mu.Lock()
	e.held = e.commandToState[command]
	old, next, changed := e.recomputeLocked()
	e.mu.Unlock()
	if changed {
		e.fire(old, next)
	}
}

// recomputeLocked derives the visible value from the observed and held
// halves. Caller holds the write lock.
func (e *EnumSelect) recomputeLocked() (old, next string, changed bool) {
	old = e.current
	hadValue := e.hasValue
	switch {
	case e.observed != "":
		next = e.observed
	default:
		next = e.held
	}
	e.current = next
	e.hasValue = next != ""
	return old, next, old != next || hadValue != e.hasValue
}

func (e *EnumSelect) fire(old, next string) {
	e.MarkModified(time.Now())
	e.mu.RLock()
	cbs := slices.Clone(e.callbacks)
	e.mu.RUnlock()
	for _, cb := range cbs {
		cb(old, next)
	}
}

// OnUpdate registers fn for every mode change and returns the
// unsubscribe.
func (e *EnumSelect) OnUpdate(fn func(old, next string)) func() {
	if fn == nil {
		return func() {}
	}
	e.mu.Lock()
	idx := len(e.callbacks)
	e.callbacks = append(e.callbacks, fn)
	e.mu.Unlock()
	return func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		if idx < len(e.callbacks) {
			e.callbacks[idx] = func(string, string) {}
		}
	}
}

// SetMode writes the command that reaches mode and holds mode optimistically
// until the device reports a state of its own.
func (e *EnumSelect) SetMode(ctx context.Context, mode string, priority hmenum.CommandPriority) error {
	command, ok := e.stateToCommand[mode]
	if !ok {
		return fmt.Errorf("combined %s: %q is not a selectable mode", e.kind, mode)
	}
	if e.Writer == nil {
		return fmt.Errorf("combined %s: writer required", e.kind)
	}
	if err := e.Writer.SetValue(ctx, e.Address, e.commandParameter, command, priority); err != nil {
		return fmt.Errorf("combined %s: %s=%s: %w", e.kind, e.commandParameter, command, err)
	}
	e.NoteCommand(command)
	return nil
}

// --- payload.CombinedProjection ------------------------------------

// CombinedKind implements [payload.CombinedProjection].
func (e *EnumSelect) CombinedKind() string { return e.kind }

// HACombinedDiscovery implements [payload.CombinedProjection]. The mode
// projects as an HA `select` carrying one option per mode.
func (e *EnumSelect) HACombinedDiscovery(ctx payload.CombinedDiscoveryContext) (component string, body map[string]any) {
	if ctx == nil {
		return "", nil
	}
	return "select", map[string]any{
		"name":          ctx.Translate(e.labelKey),
		"command_topic": ctx.CombinedCommandTopic(),
		"options":       e.Modes(),
		"optimistic":    false,
	}
}

// CombinedStatePayload implements [payload.CombinedProjection].
func (e *EnumSelect) CombinedStatePayload() (string, bool) {
	return e.Value()
}

// OnCombinedChange implements [payload.CombinedProjection].
func (e *EnumSelect) OnCombinedChange(fn func()) func() {
	return e.OnUpdate(func(_, _ string) { fn() })
}

// WriteCombined implements [payload.CombinedWritable].
func (e *EnumSelect) WriteCombined(ctx context.Context, raw string, priority hmenum.CommandPriority) error {
	return e.SetMode(ctx, raw, priority)
}
