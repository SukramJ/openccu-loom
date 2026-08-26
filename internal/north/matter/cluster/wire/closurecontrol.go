// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package wire

import (
	"errors"
	"math"

	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// ClosureControlClusterID is the Matter cluster identifier for
// ClosureControl.
//
// Mirrors matter.js packages/model/src/standard/elements/closure-control.element.ts:20.
const ClosureControlClusterID uint32 = 0x0104

// ClosureControl attribute IDs.
//
// Mirrors matter.js closure-control.element.ts:37-56.
const (
	ClosureControlAttrCountdownTime       uint32 = 0x0000
	ClosureControlAttrMainState           uint32 = 0x0001
	ClosureControlAttrCurrentErrorList    uint32 = 0x0002
	ClosureControlAttrOverallCurrentState uint32 = 0x0003
	ClosureControlAttrOverallTargetState  uint32 = 0x0004
	ClosureControlAttrLatchControlModes   uint32 = 0x0005
)

// ClosureControl command IDs.
//
// Mirrors matter.js closure-control.element.ts:75-85.
const (
	ClosureControlCmdStop      uint32 = 0x00
	ClosureControlCmdMoveTo    uint32 = 0x01
	ClosureControlCmdCalibrate uint32 = 0x02
)

// ClosureControl event IDs.
//
// Mirrors matter.js closure-control.element.ts:58-73.
const (
	ClosureControlEventOperationalError   uint32 = 0x00
	ClosureControlEventMovementCompleted  uint32 = 0x01
	ClosureControlEventEngageStateChanged uint32 = 0x02
	ClosureControlEventSecureStateChanged uint32 = 0x03
)

// ClosureControl FeatureMap bits.
//
// Mirrors matter.js closure-control.element.ts:25-33. The conformance
// column matters as much as the bit position: VT and PD and CL are all
// "[PS]", so advertising any of them without Positioning produces a
// FeatureMap the spec does not allow.
const (
	ClosureControlFeaturePositioning      uint32 = 1 << 0 // PS
	ClosureControlFeatureMotionLatching   uint32 = 1 << 1 // LT
	ClosureControlFeatureInstantaneous    uint32 = 1 << 2 // IS
	ClosureControlFeatureSpeed            uint32 = 1 << 3 // SP, [PS & !IS]
	ClosureControlFeatureVentilation      uint32 = 1 << 4 // VT, [PS]
	ClosureControlFeaturePedestrian       uint32 = 1 << 5 // PD, [PS]
	ClosureControlFeatureCalibration      uint32 = 1 << 6 // CL, [PS]
	ClosureControlFeatureProtection       uint32 = 1 << 7 // PT
	ClosureControlFeatureManuallyOperable uint32 = 1 << 8 // MO
)

// ClosureCurrentPosition is the CurrentPositionEnum reported inside
// OverallCurrentState.
//
// Mirrors matter.js closure-control.element.ts:88-95. OpenedForPedestrian
// is conformance PD and OpenedForVentilation is conformance VT, so a
// server reports them only when it advertises that feature.
type ClosureCurrentPosition uint8

// ClosureCurrentPosition values.
const (
	ClosureCurrentPositionFullyClosed          ClosureCurrentPosition = 0
	ClosureCurrentPositionFullyOpened          ClosureCurrentPosition = 1
	ClosureCurrentPositionPartiallyOpened      ClosureCurrentPosition = 2
	ClosureCurrentPositionOpenedForPedestrian  ClosureCurrentPosition = 3
	ClosureCurrentPositionOpenedForVentilation ClosureCurrentPosition = 4
	ClosureCurrentPositionOpenedAtSignature    ClosureCurrentPosition = 5
)

// ClosureTargetPosition is the TargetPositionEnum accepted by MoveTo and
// reported inside OverallTargetState.
//
// Mirrors matter.js closure-control.element.ts:97-104.
type ClosureTargetPosition uint8

// ClosureTargetPosition values.
const (
	ClosureTargetPositionMoveToFullyClosed         ClosureTargetPosition = 0
	ClosureTargetPositionMoveToFullyOpen           ClosureTargetPosition = 1
	ClosureTargetPositionMoveToPedestrianPosition  ClosureTargetPosition = 2
	ClosureTargetPositionMoveToVentilationPosition ClosureTargetPosition = 3
	ClosureTargetPositionMoveToSignaturePosition   ClosureTargetPosition = 4
)

// ClosureMainState is the MainStateEnum reported on attribute 0x0001.
//
// Mirrors matter.js closure-control.element.ts:106-116.
type ClosureMainState uint8

// ClosureMainState values.
const (
	ClosureMainStateStopped          ClosureMainState = 0
	ClosureMainStateMoving           ClosureMainState = 1
	ClosureMainStateWaitingForMotion ClosureMainState = 2
	ClosureMainStateError            ClosureMainState = 3
	ClosureMainStateCalibrating      ClosureMainState = 4
	ClosureMainStateProtected        ClosureMainState = 5
	ClosureMainStateDisengaged       ClosureMainState = 6
	ClosureMainStateSetupRequired    ClosureMainState = 7
)

// ClosureError is the ClosureErrorEnum carried in CurrentErrorList and
// in the OperationalError event.
//
// Mirrors matter.js closure-control.element.ts:118-125.
type ClosureError uint8

// ClosureError values.
const (
	ClosureErrorPhysicallyBlocked    ClosureError = 0
	ClosureErrorBlockedBySensor      ClosureError = 1
	ClosureErrorTemperatureLimited   ClosureError = 2
	ClosureErrorMaintenanceRequired  ClosureError = 3
	ClosureErrorInternalInterference ClosureError = 4
)

// ClosureErrorList is CurrentErrorList (attribute 0x0002): a TLV array of
// ClosureErrorEnum, constraint "max 10[all]".
//
// It is a named type rather than a bare []uint8 on purpose. []uint8 is
// []byte in Go, so the attribute writer's `case []byte` would claim it
// and encode an octet string where the controller expects an array — a
// wire-shape error that decodes without complaint on some stacks and
// aborts the read on others.
type ClosureErrorList []ClosureError

// ClosureErrorListMax is the "max 10[all]" constraint on CurrentErrorList.
//
// Mirrors matter.js closure-control.element.ts:41.
const ClosureErrorListMax = 10

// ClosureOverallCurrentState is OverallCurrentStateStruct (attribute
// 0x0003).
//
// Mirrors matter.js closure-control.element.ts:127-138. Every field is
// conformance-gated: Position is PS, Latch is LT, Speed is SP, and only
// SecureState is mandatory. Fields whose feature the server does not
// advertise are absent from the wire entirely, not encoded as null — a
// null says "this exists and has no value", which is a different claim.
//
// Position and SecureState both carry quality X, so a nil pointer is the
// value "null" and an absent feature is a nil field the encoder skips.
type ClosureOverallCurrentState struct {
	// Position is field 0, conformance PS, quality X.
	Position *ClosureCurrentPosition
	// SecureState is field 3, conformance M, quality X.
	SecureState *bool
}

// ClosureOverallTargetState is OverallTargetStateStruct (attribute
// 0x0004).
//
// Mirrors matter.js closure-control.element.ts:140-145.
type ClosureOverallTargetState struct {
	// Position is field 0, conformance PS, quality X.
	Position *ClosureTargetPosition
}

// OverallCurrentState / OverallTargetState field tags.
//
// Mirrors matter.js closure-control.element.ts:128-144.
const (
	ClosureOverallStateFieldPosition    uint8 = 0
	ClosureOverallStateFieldLatch       uint8 = 1
	ClosureOverallStateFieldSpeed       uint8 = 2
	ClosureOverallStateFieldSecureState uint8 = 3
)

// ErrClosureControlMalformed is returned for malformed ClosureControl
// command payloads.
var ErrClosureControlMalformed = errors.New("wire: ClosureControl command malformed")

// MoveToRequest mirrors the ClosureControl MoveTo command payload.
//
// Mirrors matter.js closure-control.element.ts:78-83. All three fields
// carry conformance "O.a+": each is optional, but at least one must be
// present. A request carrying none is malformed, which
// [DecodeClosureMoveTo] reports rather than silently treating as a no-op.
type MoveToRequest struct {
	// Position is field 0. Nil when the request omitted it.
	Position *ClosureTargetPosition
	// Latch is field 1. Nil when the request omitted it.
	Latch *bool
	// Speed is field 2. Nil when the request omitted it.
	Speed *uint8
}

// MoveTo field tags.
const (
	closureMoveToFieldPosition uint32 = 0
	closureMoveToFieldLatch    uint32 = 1
	closureMoveToFieldSpeed    uint32 = 2
)

// DecodeClosureMoveTo parses a MoveTo payload.
//
// Returns [ErrClosureControlMalformed] when the payload carries none of
// the three optional fields: the "O.a+" conformance makes at least one
// mandatory, and a request that asks for nothing is a controller bug
// worth reporting rather than an expensive no-op.
func DecodeClosureMoveTo(payload []byte) (MoveToRequest, error) {
	var req MoveToRequest
	if err := walkContext(payload, ErrClosureControlMalformed, func(tag uint32, el tlv.Element) {
		// A null element for an optional field means "explicitly absent";
		// treating it as present would put a zero-valued Position on the
		// target state, which reads as MoveToFullyClosed.
		if el.IsNull {
			return
		}
		switch tag {
		case closureMoveToFieldPosition:
			// TargetPositionEnum is enum8, so a wider element is a
			// malformed field rather than a large position.
			if el.Type == tlv.TypeUnsignedInt1 && el.Uint <= math.MaxUint8 {
				pos := ClosureTargetPosition(el.Uint)
				req.Position = &pos
			}
		case closureMoveToFieldLatch:
			if el.Type == tlv.TypeBoolTrue || el.Type == tlv.TypeBoolFalse {
				latch := el.Bool
				req.Latch = &latch
			}
		case closureMoveToFieldSpeed:
			// ThreeLevelAutoEnum is enum8; same reasoning as Position.
			if el.Type == tlv.TypeUnsignedInt1 && el.Uint <= math.MaxUint8 {
				speed := uint8(el.Uint)
				req.Speed = &speed
			}
		}
	}); err != nil {
		return MoveToRequest{}, err
	}
	if req.Position == nil && req.Latch == nil && req.Speed == nil {
		return MoveToRequest{}, ErrClosureControlMalformed
	}
	return req, nil
}
