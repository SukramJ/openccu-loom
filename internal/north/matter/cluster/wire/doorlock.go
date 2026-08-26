// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package wire

import (
	"errors"

	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// DoorLockClusterID is the Matter DoorLock cluster identifier (0x0101).
const DoorLockClusterID uint32 = 0x0101

// DoorLock attribute IDs per Matter §5.2.6.
const (
	DoorLockAttrLockState               uint32 = 0x0000
	DoorLockAttrLockType                uint32 = 0x0001
	DoorLockAttrActuatorEnabled         uint32 = 0x0002
	DoorLockAttrOperatingMode           uint32 = 0x0025
	DoorLockAttrSupportedOperatingModes uint32 = 0x0026
)

// DoorLock command IDs per Matter §5.2.7.
const (
	DoorLockCmdLockDoor   uint32 = 0x00
	DoorLockCmdUnlockDoor uint32 = 0x01
	DoorLockCmdUnboltDoor uint32 = 0x27 // Matter 1.3+ "open latch"
)

// DoorLock event IDs per Matter §5.2.10. All three carry conformance M
// and priority critical; DoorStateChange (0x01, DPS) and LockUserChange
// (0x04, USR) are feature-gated and absent from this projection.
// Mirrors matter.js door-lock-cluster.element.ts:172-198.
const (
	DoorLockEventDoorLockAlarm      uint32 = 0x00
	DoorLockEventLockOperation      uint32 = 0x02
	DoorLockEventLockOperationError uint32 = 0x03
)

// ErrDoorLockMalformed is returned for malformed DoorLock command
// payloads.
var ErrDoorLockMalformed = errors.New("wire: DoorLock command malformed")

// DoorLockRequest is the common shape for LockDoor / UnlockDoor /
// UnboltDoor. The PIN field is optional and only meaningful when the
// cluster advertises the PIN_CREDENTIAL feature — openccu-loom does
// not, so PinCode is decoded but ignored by the model layer (HM
// devices have no PIN concept; access control is enforced at the
// Matter fabric level).
type DoorLockRequest struct {
	// PinCode is the optional credential supplied by the controller.
	// nil when absent; non-nil even when length is zero (an explicit
	// empty octet-string is meaningful in TLV).
	PinCode []byte
}

// DecodeDoorLockRequest parses any of the LockDoor / UnlockDoor /
// UnboltDoor command payloads. They share a single optional PinCode
// field at context tag 0; an empty payload (no struct fields) is
// valid and yields an empty request.
func DecodeDoorLockRequest(payload []byte) (DoorLockRequest, error) {
	var req DoorLockRequest
	if err := walkContext(payload, ErrDoorLockMalformed, func(tag uint32, el tlv.Element) {
		if tag != 0 {
			return
		}
		if el.IsNull {
			return
		}
		// Defensive copy: walkContext hands out an Element whose Octets
		// alias the input buffer, which the caller is free to recycle.
		buf := make([]byte, len(el.Octets))
		copy(buf, el.Octets)
		req.PinCode = buf
	}); err != nil {
		return req, err
	}
	return req, nil
}
