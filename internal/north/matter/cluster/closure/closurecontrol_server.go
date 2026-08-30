// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package closure contains the Matter ClosureControl cluster server
// (0x0104) for the Positioning + Ventilation feature profile. It is
// instantiated by the rich-model garage type and mounted onto its Matter
// endpoint projection.
//
// ClosureControl is what Matter offers for a closure whose travel has
// named stops rather than a continuous position. WindowCovering, which
// the garage projection used before, has only a lift axis: a garage
// drive's ventilation position had to be expressed as a percentage
// somewhere between open and closed, which no controller can label and
// no user can find.
package closure

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster"
	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ClusterRevision is the ClosureControl cluster revision this server
// implements. Pinned against matter.js HEAD by
// TestParityMatterJS_ClosureControlClusterRevision.
const ClusterRevision uint16 = 1

// PositioningVentilationFeatureMap is the feature set a garage drive with
// a ventilation stop advertises: Positioning plus Ventilation.
//
// Ventilation carries conformance "[PS]" (matter.js
// closure-control.element.ts:31), so it is never advertised alone.
const PositioningVentilationFeatureMap = wire.ClosureControlFeaturePositioning |
	wire.ClosureControlFeatureVentilation

// MoveHandler applies a target position to the underlying device.
//
// It returns an error to refuse the move; the server maps that onto a
// Failure status and leaves its target state untouched, so a controller
// that could not reach the device does not see a target the device never
// accepted.
type MoveHandler func(ctx context.Context, target wire.ClosureTargetPosition, priority hmenum.CommandPriority) error

// StopHandler halts motion.
type StopHandler func(ctx context.Context, priority hmenum.CommandPriority) error

// Config carries the handlers and initial state for a
// [ControlServer].
type Config struct {
	// FeatureMap is the advertised feature set. Zero selects
	// [PositioningVentilationFeatureMap].
	FeatureMap uint32
	// Move applies a MoveTo. Nil makes MoveTo report Failure rather than
	// accepting a command that reaches nothing.
	Move MoveHandler
	// Stop halts motion. Nil makes Stop report Failure.
	Stop StopHandler
}

// ControlServer implements
// [github.com/SukramJ/openccu-loom/pkg/interfaces.MatterClusterServer]
// for the Matter ClosureControl cluster (0x0104).
//
// The server holds the state a controller reads and forwards commands to
// the handlers in [Config]; the mapping between a Matter position and the
// device's own vocabulary lives in the rich-model projection, not here.
type ControlServer struct {
	mu sync.RWMutex

	featureMap uint32
	move       MoveHandler
	stop       StopHandler

	mainState wire.ClosureMainState
	errorList wire.ClosureErrorList

	// currentPosition and targetPosition are nil until observed, which is
	// the null the spec asks for on a quality-X field. A drive that has
	// not reported yet must not read as FullyClosed.
	currentPosition *wire.ClosureCurrentPosition
	targetPosition  *wire.ClosureTargetPosition
	secureState     *bool
}

// NewControlServer constructs a [ControlServer] from cfg.
func NewControlServer(cfg Config) *ControlServer {
	featureMap := cfg.FeatureMap
	if featureMap == 0 {
		featureMap = PositioningVentilationFeatureMap
	}
	return &ControlServer{
		featureMap: featureMap,
		move:       cfg.Move,
		stop:       cfg.Stop,
		// SetupRequired, not Stopped: the drive has reported nothing yet,
		// and Stopped is a claim about a device we have not heard from.
		// Mirrors matter.js closure-control.element.ts:113 MainStateEnum.
		mainState: wire.ClosureMainStateSetupRequired,
		errorList: wire.ClosureErrorList{},
	}
}

// MatterClusterID returns the ClosureControl cluster ID (0x0104).
func (s *ControlServer) MatterClusterID() uint32 {
	return wire.ClosureControlClusterID
}

// MatterRead resolves the mandatory ClosureControl attributes for the
// Positioning + Ventilation profile.
//
// CountdownTime (conformance "[PS & !IS]") and LatchControlModes
// (conformance "LT") are absent: the first is optional under this feature
// set and the second belongs to a feature this server does not advertise.
func (s *ControlServer) MatterRead(attrID uint32) (value any, ok bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	switch attrID {
	case wire.ClosureControlAttrMainState:
		return uint8(s.mainState), true
	case wire.ClosureControlAttrCurrentErrorList:
		// A copy: the caller must not be able to mutate cluster state
		// through a value it read.
		out := make(wire.ClosureErrorList, len(s.errorList))
		copy(out, s.errorList)
		return out, true
	case wire.ClosureControlAttrOverallCurrentState:
		return &wire.ClosureOverallCurrentState{
			Position:    s.currentPosition,
			SecureState: s.secureState,
		}, true
	case wire.ClosureControlAttrOverallTargetState:
		return &wire.ClosureOverallTargetState{Position: s.targetPosition}, true
	case cluster.AttrGlobalFeatureMap:
		// Every cluster server answers the two universal globals itself;
		// nothing upstream fills them in. Without FeatureMap a controller
		// cannot see the Ventilation feature, which is the whole reason
		// this cluster is here.
		return s.featureMap, true
	case cluster.AttrGlobalClusterRevision:
		return ClusterRevision, true
	default:
		return nil, false
	}
}

// MatterWrite reports that no ClosureControl attribute is writable.
//
// Every attribute in this cluster carries access "R V" (matter.js
// closure-control.element.ts:37-56); state changes travel through MoveTo
// and Stop.
func (s *ControlServer) MatterWrite(_ context.Context, attrID uint32, _ any, _ hmenum.CommandPriority) error {
	return fmt.Errorf("closurecontrol: attribute 0x%04X is not writable", attrID)
}

// closureUnsupportedCommandErr is a typed [im.StatusCodeError] for a
// command the advertised feature set does not include.
type closureUnsupportedCommandErr struct{ msg string }

func (e closureUnsupportedCommandErr) Error() string { return e.msg }
func (closureUnsupportedCommandErr) MatterStatusCode() im.StatusCode {
	return im.StatusUnsupportedCommand
}

// closureConstraintErr is a typed [im.StatusCodeError] for a MoveTo
// carrying a position outside the advertised feature set.
type closureConstraintErr struct{ msg string }

func (e closureConstraintErr) Error() string                 { return e.msg }
func (closureConstraintErr) MatterStatusCode() im.StatusCode { return im.StatusConstraintError }

// Compile-time assertions.
var (
	_ im.StatusCodeError = closureUnsupportedCommandErr{}
	_ im.StatusCodeError = closureConstraintErr{}
)

// MatterInvoke handles the ClosureControl commands this feature set
// carries: MoveTo (conformance M) and Stop (conformance "!IS", mandatory
// because the server does not advertise Instantaneous).
//
// Calibrate is conformance "CL" and this server does not advertise
// Calibration, so it reports UnsupportedCommand rather than succeeding
// silently.
func (s *ControlServer) MatterInvoke(
	ctx context.Context, cmdID uint32, fields any, priority hmenum.CommandPriority,
) (response any, err error) {
	switch cmdID {
	case wire.ClosureControlCmdMoveTo:
		return nil, s.invokeMoveTo(ctx, fields, priority)
	case wire.ClosureControlCmdStop:
		return nil, s.invokeStop(ctx, priority)
	case wire.ClosureControlCmdCalibrate:
		return nil, closureUnsupportedCommandErr{
			"closurecontrol: Calibrate requires the Calibration feature, which this server does not advertise",
		}
	default:
		return nil, fmt.Errorf("closurecontrol: unknown command 0x%02X", cmdID)
	}
}

// invokeMoveTo applies a MoveTo request.
func (s *ControlServer) invokeMoveTo(ctx context.Context, fields any, priority hmenum.CommandPriority) error {
	req, err := moveToRequest(fields)
	if err != nil {
		return err
	}
	if req.Position == nil {
		// Latch and Speed belong to MotionLatching and Speed, neither of
		// which this server advertises. A request carrying only those
		// asks for something the advertised feature set cannot do.
		return closureConstraintErr{
			"closurecontrol: MoveTo without a Position, and neither Latch nor Speed is supported by this feature set",
		}
	}
	if err := s.checkPositionSupported(*req.Position); err != nil {
		return err
	}
	if s.move == nil {
		return errors.New("closurecontrol: MoveTo has no handler")
	}
	if err := s.move(ctx, *req.Position, priority); err != nil {
		// Deliberately no state change: recording a target the device
		// refused would leave the controller reading a move that never
		// happened.
		return fmt.Errorf("closurecontrol: MoveTo: %w", err)
	}
	s.mu.Lock()
	target := *req.Position
	s.targetPosition = &target
	s.mainState = wire.ClosureMainStateMoving
	s.mu.Unlock()
	return nil
}

// invokeStop halts motion.
func (s *ControlServer) invokeStop(ctx context.Context, priority hmenum.CommandPriority) error {
	if s.stop == nil {
		return errors.New("closurecontrol: Stop has no handler")
	}
	if err := s.stop(ctx, priority); err != nil {
		return fmt.Errorf("closurecontrol: Stop: %w", err)
	}
	s.mu.Lock()
	// The target is dropped, not kept: after a stop the drive is heading
	// nowhere, and a retained target claims otherwise.
	s.targetPosition = nil
	s.mainState = wire.ClosureMainStateStopped
	s.mu.Unlock()
	return nil
}

// checkPositionSupported rejects a target the advertised feature set does
// not carry.
//
// MoveToPedestrianPosition is conformance PD and MoveToVentilationPosition
// is conformance VT (matter.js closure-control.element.ts:97-104).
// Accepting one whose feature is not advertised would move the drive
// somewhere the controller was never told about.
func (s *ControlServer) checkPositionSupported(p wire.ClosureTargetPosition) error {
	var need uint32
	switch p {
	case wire.ClosureTargetPositionMoveToPedestrianPosition:
		need = wire.ClosureControlFeaturePedestrian
	case wire.ClosureTargetPositionMoveToVentilationPosition:
		need = wire.ClosureControlFeatureVentilation
	case wire.ClosureTargetPositionMoveToFullyClosed,
		wire.ClosureTargetPositionMoveToFullyOpen,
		wire.ClosureTargetPositionMoveToSignaturePosition:
		return nil
	default:
		return closureConstraintErr{fmt.Sprintf("closurecontrol: MoveTo position %d is not a TargetPositionEnum value", p)}
	}
	s.mu.RLock()
	advertised := s.featureMap
	s.mu.RUnlock()
	if advertised&need == 0 {
		return closureConstraintErr{
			fmt.Sprintf("closurecontrol: MoveTo position %d needs a feature this server does not advertise", p),
		}
	}
	return nil
}

// moveToRequest normalises the decoded command payload the bridge hands
// over. It accepts the decoded struct and the raw TLV both, mirroring the
// WindowCovering server's tolerance for either shape.
func moveToRequest(fields any) (wire.MoveToRequest, error) {
	switch v := fields.(type) {
	case wire.MoveToRequest:
		return v, nil
	case *wire.MoveToRequest:
		if v == nil {
			return wire.MoveToRequest{}, wire.ErrClosureControlMalformed
		}
		return *v, nil
	case []byte:
		return wire.DecodeClosureMoveTo(v)
	default:
		return wire.MoveToRequest{}, fmt.Errorf(
			"closurecontrol: MoveTo expected wire.MoveToRequest or []byte, got %T", fields,
		)
	}
}

// MatterReportable lists the attributes that change at runtime and need
// Matter subscription reports.
func (s *ControlServer) MatterReportable() []uint32 {
	return []uint32{
		wire.ClosureControlAttrMainState,
		wire.ClosureControlAttrOverallCurrentState,
		wire.ClosureControlAttrOverallTargetState,
	}
}

// MatterAttributes lists every attribute the server resolves in
// MatterRead.
func (s *ControlServer) MatterAttributes() []uint32 {
	return []uint32{
		wire.ClosureControlAttrMainState,
		wire.ClosureControlAttrCurrentErrorList,
		wire.ClosureControlAttrOverallCurrentState,
		wire.ClosureControlAttrOverallTargetState,
		cluster.AttrGlobalFeatureMap,
		cluster.AttrGlobalClusterRevision,
	}
}

// FeatureMap reports the advertised feature set.
func (s *ControlServer) FeatureMap() uint32 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.featureMap
}

// SetCurrentPosition records a position the device reported.
//
// pos nil means the drive is somewhere between its named stops — mid
// travel, most often — which the spec expresses as a null Position rather
// than a nearest-stop guess.
func (s *ControlServer) SetCurrentPosition(pos *wire.ClosureCurrentPosition) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.currentPosition = pos
	// SecureState is "the closure is in a position that secures the
	// opening" — true only when fully closed (matter.js
	// closure-control.element.ts:134-137).
	secure := pos != nil && *pos == wire.ClosureCurrentPositionFullyClosed
	if pos == nil {
		s.secureState = nil
	} else {
		s.secureState = &secure
	}
	if pos == nil {
		// A null Position means the drive's position is unknown, which the
		// spec says plainly (matter.js closure-control.resource.ts:429-439:
		// "If the closure doesn't know accurately its current state the value
		// null shall be used"). Whether it is MOVING is a different question
		// and a different attribute, answered by the model from the drive's
		// own motion signal and delivered through [ControlServer.SetMainState]
		// — reading it off the position made a stuck or unreferenced door
		// report as perpetually in motion.
		return
	}
	// The drive arrived, so nothing is outstanding.
	s.targetPosition = nil
}

// SetMainState overrides the operational state.
func (s *ControlServer) SetMainState(state wire.ClosureMainState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mainState = state
}

// SetErrorList replaces CurrentErrorList, truncating to the spec's
// "max 10[all]" constraint.
func (s *ControlServer) SetErrorList(list wire.ClosureErrorList) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(list) > wire.ClosureErrorListMax {
		list = list[:wire.ClosureErrorListMax]
	}
	s.errorList = make(wire.ClosureErrorList, len(list))
	copy(s.errorList, list)
}
