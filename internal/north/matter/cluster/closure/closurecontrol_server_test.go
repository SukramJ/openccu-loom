// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package closure_test

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/closure"
	clusterwire "github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// recordingHandlers captures what the server forwarded to the device.
type recordingHandlers struct {
	moved   []clusterwire.ClosureTargetPosition
	stopped int
	moveErr error
	stopErr error
}

func (h *recordingHandlers) config() closure.Config {
	return closure.Config{
		Move: func(_ context.Context, target clusterwire.ClosureTargetPosition, _ hmenum.CommandPriority) error {
			if h.moveErr != nil {
				return h.moveErr
			}
			h.moved = append(h.moved, target)
			return nil
		},
		Stop: func(_ context.Context, _ hmenum.CommandPriority) error {
			if h.stopErr != nil {
				return h.stopErr
			}
			h.stopped++
			return nil
		},
	}
}

func targetPtr(p clusterwire.ClosureTargetPosition) *clusterwire.ClosureTargetPosition { return &p }

func currentPtr(p clusterwire.ClosureCurrentPosition) *clusterwire.ClosureCurrentPosition { return &p }

// readOverallCurrent reads attribute 0x0003 as its struct.
func readOverallCurrent(t *testing.T, s *closure.ControlServer) *clusterwire.ClosureOverallCurrentState {
	t.Helper()
	v, ok := s.MatterRead(clusterwire.ClosureControlAttrOverallCurrentState)
	if !ok {
		t.Fatal("OverallCurrentState is not served")
	}
	st, ok := v.(*clusterwire.ClosureOverallCurrentState)
	if !ok {
		t.Fatalf("OverallCurrentState = %T, want *ClosureOverallCurrentState", v)
	}
	return st
}

// readOverallTarget reads attribute 0x0004 as its struct.
func readOverallTarget(t *testing.T, s *closure.ControlServer) *clusterwire.ClosureOverallTargetState {
	t.Helper()
	v, ok := s.MatterRead(clusterwire.ClosureControlAttrOverallTargetState)
	if !ok {
		t.Fatal("OverallTargetState is not served")
	}
	st, ok := v.(*clusterwire.ClosureOverallTargetState)
	if !ok {
		t.Fatalf("OverallTargetState = %T, want *ClosureOverallTargetState", v)
	}
	return st
}

// TestClosureControlReportsNullPositionBeforeTheDriveHasSpoken pins that
// an unobserved drive reads as null rather than as a position.
//
// FullyClosed is the zero value of CurrentPositionEnum, so a struct built
// without care reports a closed door for a drive that has said nothing —
// a reading, not a placeholder, and the controller cannot tell them apart.
func TestClosureControlReportsNullPositionBeforeTheDriveHasSpoken(t *testing.T) {
	t.Parallel()
	s := closure.NewControlServer(closure.Config{})

	if got := readOverallCurrent(t, s); got.Position != nil {
		t.Errorf("Position = %v before any observation, want null", *got.Position)
	}
	if got := readOverallTarget(t, s); got.Position != nil {
		t.Errorf("target Position = %v before any command, want null", *got.Position)
	}
	v, ok := s.MatterRead(clusterwire.ClosureControlAttrMainState)
	if !ok {
		t.Fatal("MainState is not served")
	}
	if v != uint8(clusterwire.ClosureMainStateSetupRequired) {
		t.Errorf("MainState = %v before any observation, want SetupRequired — "+
			"Stopped is a claim about a drive nothing has heard from", v)
	}
}

// TestClosureControlMoveToForwardsAndRecordsTheTarget pins the write path
// for each position the advertised feature set carries.
func TestClosureControlMoveToForwardsAndRecordsTheTarget(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		target clusterwire.ClosureTargetPosition
	}{
		{"fully closed", clusterwire.ClosureTargetPositionMoveToFullyClosed},
		{"fully open", clusterwire.ClosureTargetPositionMoveToFullyOpen},
		{"ventilation", clusterwire.ClosureTargetPositionMoveToVentilationPosition},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := &recordingHandlers{}
			s := closure.NewControlServer(h.config())

			_, err := s.MatterInvoke(context.Background(), clusterwire.ClosureControlCmdMoveTo,
				clusterwire.MoveToRequest{Position: targetPtr(tc.target)}, hmenum.CommandPriorityHigh)
			if err != nil {
				t.Fatalf("MoveTo: %v", err)
			}
			if len(h.moved) != 1 || h.moved[0] != tc.target {
				t.Fatalf("handler saw %v, want [%v]", h.moved, tc.target)
			}
			got := readOverallTarget(t, s)
			if got.Position == nil || *got.Position != tc.target {
				t.Fatalf("OverallTargetState.Position = %v, want %v", got.Position, tc.target)
			}
		})
	}
}

// TestClosureControlMoveToRejectsAPositionOutsideTheFeatureSet pins that a
// target whose feature is not advertised is refused with ConstraintError.
//
// MoveToPedestrianPosition is conformance PD. Accepting it on a server
// that never advertised Pedestrian would move the drive somewhere the
// controller was never told the device could go.
func TestClosureControlMoveToRejectsAPositionOutsideTheFeatureSet(t *testing.T) {
	t.Parallel()
	h := &recordingHandlers{}
	s := closure.NewControlServer(h.config())

	_, err := s.MatterInvoke(context.Background(), clusterwire.ClosureControlCmdMoveTo,
		clusterwire.MoveToRequest{Position: targetPtr(clusterwire.ClosureTargetPositionMoveToPedestrianPosition)},
		hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("MoveTo to an unadvertised position must be refused")
	}
	var statusErr im.StatusCodeError
	if !errors.As(err, &statusErr) || statusErr.MatterStatusCode() != im.StatusConstraintError {
		t.Errorf("err = %v, want a ConstraintError status", err)
	}
	if len(h.moved) != 0 {
		t.Errorf("a refused MoveTo must not reach the device, handler saw %v", h.moved)
	}
}

// TestClosureControlMoveToKeepsTheTargetUnchangedWhenTheDeviceRefuses pins
// that a failed write leaves no target behind.
//
// Recording a target the device never accepted leaves the controller
// reading a move that is not happening, with nothing to correct it.
func TestClosureControlMoveToKeepsTheTargetUnchangedWhenTheDeviceRefuses(t *testing.T) {
	t.Parallel()
	h := &recordingHandlers{moveErr: errors.New("ccu unreachable")}
	s := closure.NewControlServer(h.config())

	_, err := s.MatterInvoke(context.Background(), clusterwire.ClosureControlCmdMoveTo,
		clusterwire.MoveToRequest{Position: targetPtr(clusterwire.ClosureTargetPositionMoveToFullyOpen)},
		hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("MoveTo must report the device's refusal")
	}
	if got := readOverallTarget(t, s); got.Position != nil {
		t.Errorf("OverallTargetState.Position = %v after a refused move, want null", *got.Position)
	}
}

// TestClosureControlMoveToWithoutAPositionIsRefused pins the "O.a+"
// conformance: at least one field must be present, and Latch and Speed
// belong to features this server does not advertise.
func TestClosureControlMoveToWithoutAPositionIsRefused(t *testing.T) {
	t.Parallel()
	h := &recordingHandlers{}
	s := closure.NewControlServer(h.config())

	_, err := s.MatterInvoke(context.Background(), clusterwire.ClosureControlCmdMoveTo,
		clusterwire.MoveToRequest{}, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("MoveTo carrying no supported field must be refused")
	}
	if len(h.moved) != 0 {
		t.Errorf("a refused MoveTo must not reach the device, handler saw %v", h.moved)
	}
}

// TestClosureControlStopClearsTheTarget pins that a stop drops the
// outstanding target: after it the drive is heading nowhere, and a
// retained target says otherwise.
func TestClosureControlStopClearsTheTarget(t *testing.T) {
	t.Parallel()
	h := &recordingHandlers{}
	s := closure.NewControlServer(h.config())

	if _, err := s.MatterInvoke(context.Background(), clusterwire.ClosureControlCmdMoveTo,
		clusterwire.MoveToRequest{Position: targetPtr(clusterwire.ClosureTargetPositionMoveToFullyOpen)},
		hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MoveTo: %v", err)
	}
	if got := readOverallTarget(t, s); got.Position == nil {
		t.Fatal("setup: the move did not record a target")
	}

	if _, err := s.MatterInvoke(context.Background(), clusterwire.ClosureControlCmdStop,
		nil, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if h.stopped != 1 {
		t.Errorf("handler saw %d stops, want 1", h.stopped)
	}
	if got := readOverallTarget(t, s); got.Position != nil {
		t.Errorf("OverallTargetState.Position = %v after Stop, want null", *got.Position)
	}
}

// TestClosureControlCalibrateIsUnsupported pins that a command belonging
// to an unadvertised feature reports UnsupportedCommand rather than
// succeeding silently.
func TestClosureControlCalibrateIsUnsupported(t *testing.T) {
	t.Parallel()
	s := closure.NewControlServer(closure.Config{})

	_, err := s.MatterInvoke(context.Background(), clusterwire.ClosureControlCmdCalibrate,
		nil, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("Calibrate must be refused without the Calibration feature")
	}
	var statusErr im.StatusCodeError
	if !errors.As(err, &statusErr) || statusErr.MatterStatusCode() != im.StatusUnsupportedCommand {
		t.Errorf("err = %v, want an UnsupportedCommand status", err)
	}
}

// TestClosureControlSecureStateTracksFullyClosed pins the SecureState
// derivation: the closure secures the opening only when fully closed.
func TestClosureControlSecureStateTracksFullyClosed(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		pos        *clusterwire.ClosureCurrentPosition
		wantSecure *bool
	}{
		{"fully closed secures", currentPtr(clusterwire.ClosureCurrentPositionFullyClosed), boolPtr(true)},
		{"fully open does not", currentPtr(clusterwire.ClosureCurrentPositionFullyOpened), boolPtr(false)},
		{"ventilation does not", currentPtr(clusterwire.ClosureCurrentPositionOpenedForVentilation), boolPtr(false)},
		{"unknown position is null", nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := closure.NewControlServer(closure.Config{})
			s.SetCurrentPosition(tc.pos)
			got := readOverallCurrent(t, s).SecureState
			switch {
			case tc.wantSecure == nil && got != nil:
				t.Fatalf("SecureState = %v, want null", *got)
			case tc.wantSecure != nil && got == nil:
				t.Fatalf("SecureState = null, want %v", *tc.wantSecure)
			case tc.wantSecure != nil && *got != *tc.wantSecure:
				t.Fatalf("SecureState = %v, want %v", *got, *tc.wantSecure)
			}
		})
	}
}

// TestClosureControlTravellingReportsMovingWithNullPosition pins the
// mid-travel shape: a drive between named stops has no position to report,
// and a nearest-stop guess would be a reading nothing observed.
func TestClosureControlTravellingReportsMovingWithNullPosition(t *testing.T) {
	t.Parallel()
	s := closure.NewControlServer(closure.Config{})

	s.SetCurrentPosition(currentPtr(clusterwire.ClosureCurrentPositionFullyClosed))
	s.SetCurrentPosition(nil)

	if got := readOverallCurrent(t, s).Position; got != nil {
		t.Errorf("Position = %v while travelling, want null", *got)
	}
	v, _ := s.MatterRead(clusterwire.ClosureControlAttrMainState)
	if v != uint8(clusterwire.ClosureMainStateMoving) {
		t.Errorf("MainState = %v while travelling, want Moving", v)
	}
}

// TestClosureControlErrorListHonoursTheSpecConstraint pins the
// "max 10[all]" constraint on CurrentErrorList: a longer list is
// truncated rather than sent, since a controller reading a
// constraint-violating list may reject the whole report.
func TestClosureControlErrorListHonoursTheSpecConstraint(t *testing.T) {
	t.Parallel()
	s := closure.NewControlServer(closure.Config{})

	long := make(clusterwire.ClosureErrorList, 0, 15)
	for range 15 {
		long = append(long, clusterwire.ClosureErrorPhysicallyBlocked)
	}
	s.SetErrorList(long)

	v, ok := s.MatterRead(clusterwire.ClosureControlAttrCurrentErrorList)
	if !ok {
		t.Fatal("CurrentErrorList is not served")
	}
	list, ok := v.(clusterwire.ClosureErrorList)
	if !ok {
		t.Fatalf("CurrentErrorList = %T, want ClosureErrorList — a bare []uint8 would be "+
			"encoded as an octet string by the attribute writer", v)
	}
	if len(list) != clusterwire.ClosureErrorListMax {
		t.Errorf("len = %d, want %d (matter.js constraint \"max 10[all]\")", len(list), clusterwire.ClosureErrorListMax)
	}
}

// TestClosureControlErrorListReadIsACopy pins that a caller cannot mutate
// cluster state through a value it read.
func TestClosureControlErrorListReadIsACopy(t *testing.T) {
	t.Parallel()
	s := closure.NewControlServer(closure.Config{})
	s.SetErrorList(clusterwire.ClosureErrorList{clusterwire.ClosureErrorPhysicallyBlocked})

	v, _ := s.MatterRead(clusterwire.ClosureControlAttrCurrentErrorList)
	list, _ := v.(clusterwire.ClosureErrorList)
	list[0] = clusterwire.ClosureErrorInternalInterference

	v2, _ := s.MatterRead(clusterwire.ClosureControlAttrCurrentErrorList)
	again, _ := v2.(clusterwire.ClosureErrorList)
	if again[0] != clusterwire.ClosureErrorPhysicallyBlocked {
		t.Error("mutating a read value changed cluster state")
	}
}

// TestClosureControlNoAttributeIsWritable pins that every attribute
// carries access "R V": state travels through the commands.
func TestClosureControlNoAttributeIsWritable(t *testing.T) {
	t.Parallel()
	s := closure.NewControlServer(closure.Config{})
	for _, id := range s.MatterAttributes() {
		if err := s.MatterWrite(context.Background(), id, uint8(0), hmenum.CommandPriorityHigh); err == nil {
			t.Errorf("attribute 0x%04X accepted a write; every ClosureControl attribute is R V", id)
		}
	}
}

func boolPtr(b bool) *bool { return &b }
