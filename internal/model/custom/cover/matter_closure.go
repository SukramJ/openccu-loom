// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package cover

import (
	"context"

	"github.com/SukramJ/go-fabric/cluster/spec"
	closuredef "github.com/SukramJ/go-fabric/cluster/spec/closurecontrol"
	fabricdevice "github.com/SukramJ/go-fabric/device"
)

// matterDeviceTypeClosure is the Matter Closure device type (0x0230).
//
// Mirrors matter.js packages/model/src/standard/elements/closure-device.element.ts:13.
// The device type requires ClosureControl and forbids WindowCovering
// (conformance "X" at :20), which is why a garage drive projecting as a
// Closure carries no WindowCovering server at all.
const matterDeviceTypeClosure = uint16(fabricdevice.ClosureID)

// A garage drive's travel has named stops — closed, ventilation, open —
// and no meaningful position between them. WindowCovering, the cluster
// this projection replaced, has only a lift percentage, so the
// ventilation stop had to be encoded as "somewhere around the middle":
// a position no controller can label, no user can find, and no read can
// distinguish from a door that happens to be halfway. ClosureControl
// names the stop, so it survives the round trip.
//
// The mapping between a Matter position and DOOR_STATE / DOOR_COMMAND
// lives here; the kit's cluster server holds no CCU knowledge.

// closureStateFor maps a door state onto the Matter CurrentPositionEnum.
//
// A travelling door reports a state that is not a named stop, and that
// maps to nil — a null Position — rather than to the nearest stop. The
// difference matters: PartiallyOpened is a claim the drive is resting
// somewhere between the stops, and the drive makes no such claim.
func closureStateFor(state DoorState) *closuredef.CurrentPositionEnum {
	var pos closuredef.CurrentPositionEnum
	switch state {
	case DoorStateClosed:
		pos = closuredef.CurrentPositionFullyClosed
	case DoorStateOpen:
		pos = closuredef.CurrentPositionFullyOpened
	case DoorStateVentilation:
		pos = closuredef.CurrentPositionOpenedForVentilation
	case DoorStateUnknown:
		return nil
	default:
		return nil
	}
	return &pos
}

// closureCommandFor maps a Matter TargetPositionEnum onto a DOOR_COMMAND.
//
// Only the three the advertised feature set carries appear; the cluster
// server refuses the rest before a request reaches this far.
func closureCommandFor(target closuredef.TargetPositionEnum) (DoorCommand, bool) {
	switch target {
	case closuredef.TargetPositionMoveToFullyClosed:
		return DoorCommandClose, true
	case closuredef.TargetPositionMoveToFullyOpen:
		return DoorCommandOpen, true
	case closuredef.TargetPositionMoveToVentilationPosition:
		return DoorCommandPartialOpen, true
	case closuredef.TargetPositionMoveToPedestrianPosition,
		closuredef.TargetPositionMoveToSignaturePosition:
		return "", false
	default:
		return "", false
	}
}

// garageMover is the [closuredef.Mover] of a garage drive: the kit's
// ClosureControl server's drive port.
//
// Both methods name matterDispatchPriority themselves: the cluster
// contract carries no priority, and Critical is the zero value of the
// command-priority enum, so a value left to travel implicitly would
// escalate rather than degrade.
type garageMover struct{ g *Garage }

// MoveTo implements [closuredef.Mover].
func (m garageMover) MoveTo(ctx context.Context, target closuredef.TargetPositionEnum) error {
	command, ok := closureCommandFor(target)
	if !ok {
		return errMatterUnknownCommand
	}
	return m.g.command(ctx, command, matterDispatchPriority)
}

// Stop implements [closuredef.Mover].
func (m garageMover) Stop(ctx context.Context) error {
	return m.g.Stop(ctx, matterDispatchPriority)
}

// StopIgnored implements [closuredef.StopObserver]: a Stop the kit's
// server answers without motion still reaches the drive. MainState here
// follows the drive's SECTION push, which lags the physical motion, so a
// Stop pressed in that window would otherwise answer SUCCESS and leave
// the door running (notes/parity/by_design.md
// BD-Matter-ClosureControl-StopForwarded).
func (m garageMover) StopIgnored(ctx context.Context) error {
	return m.g.Stop(ctx, matterDispatchPriority)
}

// publishClosure pushes the drive's current state into the device's
// ClosureControl server, if the device was built. Only then: building it
// here would create a device for a drive no controller has asked about,
// and the device seeds itself from DoorState when it is built anyway.
func (g *Garage) publishClosure() {
	if d := g.matterDevice.Current(); d != nil {
		g.publishClosureTo(d)
	}
}

// publishClosureTo stores the drive's position and motion on the
// ClosureControl server of d through its typed Set: the Position of
// OverallCurrentState (null between the stops) and MainState, the server
// deriving SecureState and emitting the events the change calls for.
func (g *Garage) publishClosureTo(d *fabricdevice.Device) {
	if g == nil || d == nil {
		return
	}
	srv := d.Server(closuredef.ClusterID)
	if srv == nil {
		return
	}
	state, observed := g.DoorState()
	if !observed {
		// Nothing heard from the drive yet. Leaving the server at its
		// constructed SetupRequired / null-position state is the honest
		// reading; seeding a stop would invent one.
		return
	}
	position := spec.Nullable[closuredef.CurrentPositionEnum]{Null: true}
	if pos := closureStateFor(state); pos != nil {
		position = spec.Nullable[closuredef.CurrentPositionEnum]{Value: *pos}
	}
	// Motion is the drive's own signal (SECTION), not something to infer
	// from the position: DOOR_STATE has no travelling value at all, so a
	// position alone can never say whether the door is moving.
	main := closureMainStateFor(g.IsOpening(), g.IsClosing())
	_ = closuredef.Wrap(srv).Set(closuredef.Patch{
		OverallCurrentState: &closuredef.OverallCurrentStateStruct{Position: &position},
		MainState:           &main,
	})
}

// closureMainStateFor maps the model's motion predicates onto the cluster's
// MainState, the same way the window-covering projection maps them onto
// OperationalStatus (motionForOpeningClosing).
func closureMainStateFor(opening, closing bool) closuredef.MainStateEnum {
	if opening || closing {
		return closuredef.MainStateMoving
	}
	return closuredef.MainStateStopped
}

// Compile-time assertions: the mover is the declared port, and it hears
// of a Stop the server would ignore.
var (
	_ closuredef.Mover        = garageMover{}
	_ closuredef.StopObserver = garageMover{}
)
