// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package cover

import (
	"context"
	"sync"

	"github.com/SukramJ/go-fabric/cluster/closure"
	clusterwire "github.com/SukramJ/go-fabric/cluster/wire"

	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// matterDeviceTypeClosure is the Matter Closure device type (0x0230).
//
// Mirrors matter.js packages/model/src/standard/elements/closure-device.element.ts:13.
// The device type requires ClosureControl and forbids WindowCovering
// (conformance "X" at :20), which is why a garage drive projecting as a
// Closure carries no WindowCovering server at all.
const matterDeviceTypeClosure uint16 = 0x0230

// garageClosureServer projects a [Garage] onto the ClosureControl cluster.
//
// A garage drive's travel has named stops — closed, ventilation, open —
// and no meaningful position between them. WindowCovering, the cluster
// this projection replaced, has only a lift percentage, so the
// ventilation stop had to be encoded as "somewhere around the middle":
// a position no controller can label, no user can find, and no read can
// distinguish from a door that happens to be halfway. ClosureControl
// names the stop, so it survives the round trip.
//
// The mapping between a Matter position and DOOR_STATE / DOOR_COMMAND
// lives here; the cluster server itself holds no CCU knowledge.
type garageClosureServer struct {
	g *Garage

	// srv holds the attribute state a controller reads. The Garage feeds
	// it through [Garage.publishClosureState]; commands travel the other
	// way through the handlers below.
	srv *closure.ControlServer
}

// closureStateFor maps a door state onto the Matter CurrentPositionEnum.
//
// A travelling door reports a state that is not a named stop, and that
// maps to nil — a null Position — rather than to the nearest stop. The
// difference matters: PartiallyOpened is a claim the drive is resting
// between stops, which is not what a door mid-travel is doing.
func closureStateFor(state DoorState) *clusterwire.ClosureCurrentPosition {
	var pos clusterwire.ClosureCurrentPosition
	switch state {
	case DoorStateClosed:
		pos = clusterwire.ClosureCurrentPositionFullyClosed
	case DoorStateOpen:
		pos = clusterwire.ClosureCurrentPositionFullyOpened
	case DoorStateVentilation:
		pos = clusterwire.ClosureCurrentPositionOpenedForVentilation
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
func closureCommandFor(target clusterwire.ClosureTargetPosition) (DoorCommand, bool) {
	switch target {
	case clusterwire.ClosureTargetPositionMoveToFullyClosed:
		return DoorCommandClose, true
	case clusterwire.ClosureTargetPositionMoveToFullyOpen:
		return DoorCommandOpen, true
	case clusterwire.ClosureTargetPositionMoveToVentilationPosition:
		return DoorCommandPartialOpen, true
	case clusterwire.ClosureTargetPositionMoveToPedestrianPosition,
		clusterwire.ClosureTargetPositionMoveToSignaturePosition:
		return "", false
	default:
		return "", false
	}
}

// newGarageClosureServer builds the ClosureControl projection for g and
// seeds it with whatever the drive has already reported.
func newGarageClosureServer(g *Garage) *garageClosureServer {
	p := &garageClosureServer{g: g}
	p.srv = closure.NewControlServer(closure.Config{
		// The handlers name matterDispatchPriority themselves: the cluster
		// contract carries no priority, and Critical is the zero value of
		// the command-priority enum, so a value left to travel implicitly
		// would escalate rather than degrade.
		Move: func(ctx context.Context, target clusterwire.ClosureTargetPosition) error {
			command, ok := closureCommandFor(target)
			if !ok {
				return errMatterUnknownCommand
			}
			return g.command(ctx, command, matterDispatchPriority)
		},
		Stop: func(ctx context.Context) error {
			return g.Stop(ctx, matterDispatchPriority)
		},
	})
	p.publish()
	return p
}

// publish pushes the drive's current state into the cluster server.
func (s *garageClosureServer) publish() {
	if s == nil || s.srv == nil || s.g == nil {
		return
	}
	state, observed := s.g.DoorState()
	if !observed {
		// Nothing heard from the drive yet. Leaving the server at its
		// constructed SetupRequired / null-position state is the honest
		// reading; seeding a stop would invent one.
		return
	}
	s.srv.SetCurrentPosition(closureStateFor(state))
	// Motion is the drive's own signal (SECTION), not something to infer from
	// the position: DOOR_STATE has no travelling value at all, so a position
	// alone can never say whether the door is moving.
	s.srv.SetMainState(closureMainStateFor(s.g.IsOpening(), s.g.IsClosing()))
}

// closureMainStateFor maps the model's motion predicates onto the cluster's
// MainState, the same way the window-covering projection maps them onto
// OperationalStatus (motionForOpeningClosing).
func closureMainStateFor(opening, closing bool) clusterwire.ClosureMainState {
	if opening || closing {
		return clusterwire.ClosureMainStateMoving
	}
	return clusterwire.ClosureMainStateStopped
}

// --- interfaces.MatterClusterServer -------------------------------

func (s *garageClosureServer) MatterClusterID() uint32 { return s.srv.MatterClusterID() }

func (s *garageClosureServer) MatterRead(attrID uint32) (value any, ok bool) {
	return s.srv.MatterRead(attrID)
}

func (s *garageClosureServer) MatterWrite(
	ctx context.Context, attrID uint32, value any,
) error {
	return s.srv.MatterWrite(ctx, attrID, value)
}

func (s *garageClosureServer) MatterInvoke(
	ctx context.Context, cmdID uint32, fields any,
) (response any, err error) {
	resp, err := s.srv.MatterInvoke(ctx, cmdID, fields)
	if err != nil {
		return nil, err
	}
	s.g.dataVersion.Bump()
	return resp, nil
}

func (s *garageClosureServer) MatterReportable() []uint32 { return s.srv.MatterReportable() }

// MatterAttributes lists every attribute the projection serves. Apple
// Home's service rebuild reads the full set; without it the dispatcher
// falls back to the reportable subset.
func (s *garageClosureServer) MatterAttributes() []uint32 { return s.srv.MatterAttributes() }

// MatterAcceptedCommands implements [interfaces.MatterClusterCommandLister].
//
// Calibrate is absent: it is conformance "CL" and this profile does not
// advertise Calibration, so listing it would offer a controller a command
// the server refuses.
func (s *garageClosureServer) MatterAcceptedCommands() []uint32 {
	return []uint32{
		clusterwire.ClosureControlCmdStop,
		clusterwire.ClosureControlCmdMoveTo,
	}
}

// MatterGeneratedCommands implements [interfaces.MatterClusterCommandLister].
func (s *garageClosureServer) MatterGeneratedCommands() []uint32 { return nil }

// closureProjection holds the per-Garage ClosureControl projection.
//
// One per drive, built lazily and kept: the cluster server owns the
// attribute state a controller subscribes to, so rebuilding it on every
// read would reset the position to "nothing observed" mid-session.
type closureProjection struct {
	once sync.Once
	mu   sync.RWMutex
	srv  *garageClosureServer
}

// get returns the projection, building it on first use.
//
// The write happens under the lock even though sync.Once orders it for
// every caller of Do, because [closureProjection.publishIfBuilt] reads
// the field without calling Do — a device event arriving while the first
// Matter read builds the projection would otherwise race.
func (p *closureProjection) get(g *Garage) *garageClosureServer {
	p.once.Do(func() {
		srv := newGarageClosureServer(g)
		p.mu.Lock()
		p.srv = srv
		p.mu.Unlock()
	})
	p.mu.RLock()
	srv := p.srv
	p.mu.RUnlock()
	return srv
}

// publishIfBuilt pushes the drive's state into an already-built
// projection and does nothing otherwise.
//
// The guard is what keeps a device event from constructing a cluster
// server for a drive no controller has subscribed to. It is not a
// missed update: [closureProjection.get] seeds the projection from the
// drive's current state when it does build it.
func (p *closureProjection) publishIfBuilt() {
	p.mu.RLock()
	srv := p.srv
	p.mu.RUnlock()
	srv.publish()
}

// Compile-time assertions: the projection satisfies both the cluster
// contract and the command lister the endpoint assembler probes for.
var (
	_ interfaces.MatterClusterServer        = (*garageClosureServer)(nil)
	_ interfaces.MatterClusterCommandLister = (*garageClosureServer)(nil)
)
