// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package cover

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/SukramJ/openccu-loom/internal/model/combined"
	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// DoorState enumerates the values the DOOR_STATE parameter reports on
// HmIP-MOD-HO / HmIP-MOD-TM-class garage drives.
//
// The labels are the parameter's VALUE_LIST verbatim — that list is what
// reaches us on the wire, and a resolved label is what
// custom.EnumLabelValue hands the comparisons below. A drive without the
// ventilation option reports a three-entry list without
// VENTILATION_POSITION, which is why the vent control stays gated on
// Capabilities.SupportsVent. Pinned by
// TestDoorEnumLabelsMatchReportedValueList.
type DoorState string

// DoorState values.
const (
	DoorStateUnknown     DoorState = "POSITION_UNKNOWN"
	DoorStateOpen        DoorState = "OPEN"
	DoorStateClosed      DoorState = "CLOSED"
	DoorStateVentilation DoorState = "VENTILATION_POSITION"
)

// DoorCommand enumerates the values DOOR_COMMAND accepts.
type DoorCommand string

// DoorCommand values.
const (
	DoorCommandOpen        DoorCommand = "OPEN"
	DoorCommandClose       DoorCommand = "CLOSE"
	DoorCommandStop        DoorCommand = "STOP"
	DoorCommandPartialOpen DoorCommand = "PARTIAL_OPEN"
	DoorCommandNop         DoorCommand = "NOP"
)

// Section codes reported on the SECTION parameter (motion phases).
const (
	sectionOpening = 2
	sectionClosing = 5
)

// GarageDoorModeKind is the combined-DP kind for the selectable door
// mode. It is a retained MQTT topic segment, so it is part of the wire
// contract and must stay stable.
const GarageDoorModeKind = "door_mode"

// parameterDoorMode is the synthetic parameter naming the door-mode
// combined data point. DOOR_MODE is not a CCU parameter: the mode is
// read from DOOR_STATE and written to DOOR_COMMAND. Naming it after
// either source would mislabel the control — "Door State" on something
// writable — and collide with that parameter's own identity on the
// channel.
const parameterDoorMode hmenum.Parameter = "DOOR_MODE"

// Garage is a garage-door drive (HmIP-MOD-HO, HmIP-MOD-TM, …).
type Garage struct {
	custom.BaseDP

	Address      string
	Capabilities custom.CoverCapabilities

	// ServiceRegistry implements the write-half of [payload.Source].
	// Service methods are registered in [NewGarage].
	payload.ServiceRegistry

	// dataVersion tracks the per-cluster monotonic counter (Matter
	// §10.6.5). Bumped on every successful MatterInvoke so
	// DataVersionFilter evaluation correctly detects cluster changes.
	dataVersion hmtypes.DataVersionTracker

	// key is the composite data-point key used by [DataPointKey] to
	// satisfy [device.AttachableDataPoint]. Keyed on DOOR_COMMAND
	// (the primary write parameter for garage doors).
	key hmtypes.DataPointKey

	writer Writer

	doorStateDp *generic.Sensor[int32]
	// doorCommandDp is DOOR_COMMAND: a write-only ENUM (OPERATIONS=2), so
	// the resolver builds an ActionSelect. It carries no readable state —
	// the door position comes from DOOR_STATE.
	doorCommandDp *generic.ActionSelect
	sectionDp     *generic.Sensor[int32]

	// doorMode is the selectable closed/ventilation/open control, present
	// only on vent-capable drives. Nil elsewhere — every use is guarded.
	doorMode *combined.EnumSelect

	// closure is the ClosureControl projection. Built on first Matter
	// read and kept: the cluster server owns the attribute state a
	// controller subscribes to, so rebuilding it would reset the reported
	// position to "nothing observed" mid-session.
	closure closureProjection

	mu       sync.RWMutex
	state    DoorState
	hasSt    bool
	sectionV int32
	hasSec   bool
}

// GarageConfig is the constructor record.
type GarageConfig struct {
	Channel      *device.Channel
	Writer       Writer
	Capabilities custom.CoverCapabilities
	// Group is the rebased channel-group schema of the profile that
	// materialised this door. Every composed field resolves through it —
	// see [custom.ResolveSlotOr].
	Group custom.RebasedChannelGroupConfig
}

// NewGarage constructs a Garage.
func NewGarage(cfg GarageConfig) *Garage {
	addr := ""
	var key hmtypes.DataPointKey
	if cfg.Channel != nil {
		addr = cfg.Channel.Address
		key = hmtypes.DataPointKey{
			ChannelAddress: addr,
			Parameter:      string(hmenum.ParameterDoorCommand),
		}
	}
	g := &Garage{
		Address:       addr,
		Capabilities:  cfg.Capabilities,
		key:           key,
		writer:        cfg.Writer,
		doorStateDp:   custom.EnumSensorField(custom.ResolveSlotOr(cfg.Channel, cfg.Group, hmenum.FieldDoorState, hmenum.ParameterDoorState)),
		doorCommandDp: custom.ActionSelectField(custom.ResolveSlotOr(cfg.Channel, cfg.Group, hmenum.FieldDoorCommand, hmenum.ParameterDoorCommand)),
		sectionDp:     custom.IntegerSensorField(custom.ResolveSlotOr(cfg.Channel, cfg.Group, hmenum.FieldSection, hmenum.ParameterSection)),
	}
	g.registerGarageServices()
	if g.doorStateDp != nil {
		_ = g.doorStateDp.OnConfirmedUpdate(func(_, _ int32) { g.dataVersion.Bump() })
	}
	if g.doorCommandDp != nil {
		_ = g.doorCommandDp.OnConfirmedUpdate(func(_, _ int32) { g.dataVersion.Bump() })
	}
	if g.sectionDp != nil {
		_ = g.sectionDp.OnConfirmedUpdate(func(_, _ int32) { g.dataVersion.Bump() })
	}
	g.attachDoorMode(cfg.Channel)
	return g
}

// attachDoorMode gives a vent-capable drive a selectable door mode.
//
// The ventilation position is a real physical state of the drive, but it
// is not on any axis a cover exposes: Home Assistant's cover platform has
// no ventilation state, and Matter's WindowCovering has no ventilation
// position. Before this data point existed the only way to reach it was
// to write a cover position that happens to land between the closed and
// open thresholds — an interaction nothing can discover, label, or read
// back. As a combined select it is a first-class three-way control.
//
// Only vent-capable drives get it. On a drive without the intermediate
// position the third option would be a control that renders and does
// nothing.
func (g *Garage) attachDoorMode(ch *device.Channel) {
	if ch == nil || !g.Capabilities.SupportsVent {
		return
	}
	g.doorMode = combined.NewEnumSelect(combined.EnumSelectConfig{
		Address:           ch.Address,
		CentralName:       ch.CentralName(),
		Writer:            g.writer,
		Kind:              GarageDoorModeKind,
		LabelKey:          "discovery.garage_door_mode",
		CombinedParameter: parameterDoorMode,
		StateParameter:    hmenum.ParameterDoorState,
		CommandParameter:  hmenum.ParameterDoorCommand,
		// Presentation order follows the door's travel, closed to open,
		// so the list reads the way the door moves.
		Modes: []combined.EnumSelectMode{
			{State: string(DoorStateClosed), Command: string(DoorCommandClose)},
			{State: string(DoorStateVentilation), Command: string(DoorCommandPartialOpen)},
			{State: string(DoorStateOpen), Command: string(DoorCommandOpen)},
		},
	})
	if g.doorMode != nil {
		ch.AttachCalculatedDataPoint(g.doorMode)
	}
}

// DataPointKey returns the composite identifier used by the materializer
// to attach this custom DP to its primary channel. Satisfies
// [device.AttachableDataPoint].
func (g *Garage) DataPointKey() hmtypes.DataPointKey { return g.key }

// DoorState returns the last observed DOOR_STATE.
func (g *Garage) DoorState() (DoorState, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.state, g.hasSt
}

// Position returns a virtual position derived from the door state: OPEN →
// 1.0, VENTILATION → 0.5, CLOSED → 0.0.
func (g *Garage) Position() (custom.Position, bool) {
	st, ok := g.DoorState()
	if !ok {
		return custom.Position{}, false
	}
	switch st { //nolint:exhaustive // DoorStateUnknown has no meaningful position; falls through to return zero Position and false
	case DoorStateOpen:
		return custom.NewPosition(1.0), true
	case DoorStateVentilation:
		return custom.NewPosition(custom.VentLevel), true
	case DoorStateClosed:
		return custom.NewPosition(0.0), true
	}
	return custom.Position{}, false
}

// IsClosed mirrors.
func (g *Garage) IsClosed() bool {
	st, ok := g.DoorState()
	return ok && st == DoorStateClosed
}

// IsOpening reports whether SECTION == 2.
func (g *Garage) IsOpening() bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.hasSec && g.sectionV == sectionOpening
}

// IsClosing reports whether SECTION == 5.
func (g *Garage) IsClosing() bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.hasSec && g.sectionV == sectionClosing
}

// Open commands the door to fully open.
func (g *Garage) Open(ctx context.Context, priority hmenum.CommandPriority) error {
	open := true
	if !g.IsStateChangeArgs(StateChangeArgs{Open: &open}) {
		return nil
	}
	return g.command(ctx, DoorCommandOpen, priority)
}

// Close commands the door to fully close.
func (g *Garage) Close(ctx context.Context, priority hmenum.CommandPriority) error {
	closing := true
	if !g.IsStateChangeArgs(StateChangeArgs{Close: &closing}) {
		return nil
	}
	return g.command(ctx, DoorCommandClose, priority)
}

// Stop commands the door to halt motion.
func (g *Garage) Stop(ctx context.Context, priority hmenum.CommandPriority) error {
	return g.command(ctx, DoorCommandStop, priority)
}

// Vent commands the door to the partial-open / ventilation position.
func (g *Garage) Vent(ctx context.Context, priority hmenum.CommandPriority) error {
	vent := true
	if !g.IsStateChangeArgs(StateChangeArgs{Vent: &vent}) {
		return nil
	}
	return g.command(ctx, DoorCommandPartialOpen, priority)
}

// SetPosition maps a target position 0..1 onto a DOOR_COMMAND.
// Thresholds mirror the Python reference (cover.py):
//
//	_COVER_VENT_MAX_POSITION = 50, _CoverPosition.VENT = 10 (scale 0-100)
//	→ normalised to 0-1: > 0.50 → OPEN, 0.10 < x ≤ 0.50 → VENT, ≤ 0.10 → CLOSE.
func (g *Garage) SetPosition(ctx context.Context, target float64, priority hmenum.CommandPriority) error {
	switch {
	case target > 0.50:
		return g.Open(ctx, priority)
	case target > 0.10:
		return g.Vent(ctx, priority)
	default:
		return g.Close(ctx, priority)
	}
}

// command writes the DOOR_COMMAND parameter.
func (g *Garage) command(ctx context.Context, c DoorCommand, priority hmenum.CommandPriority) error {
	if g.writer == nil {
		return errors.New("garage: writer required")
	}
	if err := g.writer.SetValue(custom.EnsureContext(ctx), g.Address, hmenum.ParameterDoorCommand, string(c), priority); err != nil {
		return fmt.Errorf("garage: DOOR_COMMAND=%s: %w", c, err)
	}
	// Tell the mode select what was just commanded. Every cover operation
	// funnels through here, so this is the one place that sees them all.
	// Without it the select keeps showing the mode of the last command it
	// issued itself — indefinitely after a STOP, which leaves DOOR_STATE
	// at a non-mode token with no further event to correct it.
	if g.doorMode != nil {
		g.doorMode.NoteCommand(string(c))
	}
	return nil
}

// IsStateChange reports whether the requested target position would
// alter the door state.
func (g *Garage) IsStateChange(target float64) bool {
	pos, ok := g.Position()
	if !ok {
		return true
	}
	return pos.Level() != target
}

// IsStateChangeArgs reports whether any of the kwarg-equivalents in
// args would amount to a door-state change. Garage doors recognise
// Open / Close / Vent / Position (the parent Cover axes plus Vent).
//
// Mirrors `CustomDpGarage.is_state_change(**kwargs)` (cover.py:640-650).
func (g *Garage) IsStateChangeArgs(args StateChangeArgs) bool {
	pos, observed := g.Position()
	if !observed {
		return true
	}
	if args.Open != nil && *args.Open && !pos.Open() {
		return true
	}
	if args.Close != nil && *args.Close && !pos.Closed() {
		return true
	}
	if args.Vent != nil && *args.Vent && !pos.Vent() {
		return true
	}
	if args.Position != nil && *args.Position != pos.Level() {
		return true
	}
	return false
}

// NamePostfix is empty for garage doors.
func (g *Garage) NamePostfix() string { return "" }

// Subscribe wires DOOR_STATE / SECTION updates into [OnState] /
// [OnSection]. Replays the wire DP's currently observed values
// through the same handlers so the Garage's hot-path-cached state
// (g.state / g.sectionV) lands in sync with the CCU at boot, not
// only on the next push. Implements [device.SubscribingDataPoint].
func (g *Garage) Subscribe(ch *device.Channel) func() {
	if ch == nil {
		return func() {}
	}
	var unsubs []func()
	// DOOR_STATE is a read-only ENUM projected onto a raw-index sensor, so an
	// update carries the index; resolve it to its VALUE_LIST label (the same
	// sensor g.doorStateDp holds, already updated when this fires) before
	// recording the DoorState.
	applyState := func(_ any) {
		if label, ok := custom.EnumLabelValue(g.doorStateDp); ok {
			g.OnState(DoorState(label))
		}
	}
	applySection := func(next any) {
		if v, ok := toInt32(next); ok {
			g.OnSection(v)
		}
	}
	// Both slots are the ones the profile schema named, resolved once in
	// [NewGarage] — not looked up again on the own channel here. A second
	// own-channel lookup binds a different data point than the one the
	// constructor's DataVersion hook and applyState's label read already
	// observe whenever the schema puts the field on a sibling channel.
	if g.doorStateDp != nil {
		unsubs = append(unsubs, g.doorStateDp.OnAnyUpdate(func(_, next any) {
			applyState(next)
		}))
		custom.ReplayCurrentValue(g.doorStateDp, applyState)
	}
	if g.sectionDp != nil {
		unsubs = append(unsubs, g.sectionDp.OnAnyUpdate(func(_, next any) {
			applySection(next)
		}))
		custom.ReplayCurrentValue(g.sectionDp, applySection)
	}
	return func() {
		for _, u := range unsubs {
			if u != nil {
				u()
			}
		}
	}
}

// OnState records a CCU-emitted DOOR_STATE update.
func (g *Garage) OnState(s DoorState) {
	g.mu.Lock()
	g.state = s
	g.hasSt = true
	g.mu.Unlock()
	// Feed the Matter projection. Only when it already exists: building
	// it here would create a cluster server for a drive no controller has
	// asked about, and the projection seeds itself from DoorState on
	// first read anyway.
	g.closure.publishIfBuilt()
}

// OnSection records a CCU-emitted SECTION update.
//
// A moving→stopped transition snaps the stored Matter target back to
// mirroring the current position — the matter.js handleStopMovement
// semantics (WindowCoveringServer.ts:485-493) applied to a stop the
// drive reports on its own (wall button, end position reached) rather
// than a Matter StopMotion command.
//
// The Matter side needs no target bookkeeping here: ClosureControl drops
// its target when the drive reports a named stop, which arrives on
// DOOR_STATE rather than on SECTION. See [Garage.OnState].
func (g *Garage) OnSection(v int32) {
	g.mu.Lock()
	g.sectionV = v
	g.hasSec = true
	g.mu.Unlock()
	// SECTION is the drive's motion signal, and the Matter projection reports
	// it as MainState. Without this the cluster could only ever learn about
	// motion from a DOOR_STATE push — which carries no travelling value — so
	// a door that started moving stayed reported as stopped until it arrived.
	g.closure.publishIfBuilt()
}

func toInt32(v any) (int32, bool) {
	switch x := v.(type) {
	case int:
		return int32(x), true //nolint:gosec // G115: CCU door-state values are small integers well within int32 range; see #20
	case int32:
		return x, true
	case int64:
		return int32(x), true //nolint:gosec // G115: CCU door-state values are small integers well within int32 range; see #20
	case float64:
		return int32(x), true //nolint:gosec // G115: CCU door-state values are small integers well within int32 range; see #20
	}
	return 0, false
}
