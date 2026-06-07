// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package wire

import (
	"errors"

	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// WindowCoveringClusterID is the Matter cluster identifier for WindowCovering.
const WindowCoveringClusterID uint32 = 0x0102

// WindowCovering attribute IDs.
const (
	WindowCoveringAttrType                             uint32 = 0x0000
	WindowCoveringAttrCurrentPositionLiftPercentage    uint32 = 0x0008
	WindowCoveringAttrOperationalStatus                uint32 = 0x000A
	WindowCoveringAttrTargetPositionLiftPercent100ths  uint32 = 0x000B
	WindowCoveringAttrCurrentPositionLiftPercent100ths uint32 = 0x000E
	WindowCoveringAttrEndProductType                   uint32 = 0x000D
	WindowCoveringAttrConfigStatus                     uint32 = 0x0007
	WindowCoveringAttrMode                             uint32 = 0x0017
	WindowCoveringAttrSafetyStatus                     uint32 = 0x001A
)

// WindowCovering command IDs per Matter §5.3.7.
const (
	WindowCoveringCmdUpOrOpen           uint32 = 0x00
	WindowCoveringCmdDownOrClose        uint32 = 0x01
	WindowCoveringCmdStopMotion         uint32 = 0x02
	WindowCoveringCmdGoToLiftValue      uint32 = 0x04
	WindowCoveringCmdGoToLiftPercentage uint32 = 0x05
	WindowCoveringCmdGoToTiltValue      uint32 = 0x07
	WindowCoveringCmdGoToTiltPercentage uint32 = 0x08
)

// ErrWindowCoveringMalformed is returned for malformed WindowCovering
// command payloads.
var ErrWindowCoveringMalformed = errors.New("wire: WindowCovering command malformed")

// GoToLiftPercentageRequest mirrors Matter §5.3.7.5.
// Matter 1.5.1 deprecated the old uint8 LiftPercentageValue field;
// the canonical encoding is uint16 LiftPercent100thsValue.
type GoToLiftPercentageRequest struct {
	LiftPercent100thsValue uint16
}

// GoToTiltPercentageRequest mirrors Matter §5.3.7.6.
type GoToTiltPercentageRequest struct {
	TiltPercent100thsValue uint16
}

// GoToLiftValueRequest mirrors Matter §5.3.7.4.
type GoToLiftValueRequest struct {
	LiftValue uint16 // raw position
}

// GoToTiltValueRequest mirrors Matter §5.3.7.4.
type GoToTiltValueRequest struct {
	TiltValue uint16
}

// DecodeGoToLiftPercentage parses GoToLiftPercentage payloads.
func DecodeGoToLiftPercentage(payload []byte) (GoToLiftPercentageRequest, error) {
	var req GoToLiftPercentageRequest
	if err := walkContext(payload, ErrWindowCoveringMalformed, func(tag uint32, el tlv.Element) {
		// Matter 1.4+ moved the value to context tag 1; older devices
		// may send tag 0 with the deprecated 8-bit field. We accept
		// both: tag 0 gets scaled up, tag 1 is canonical.
		switch tag {
		case 0:
			req.LiftPercent100thsValue = uint16(el.Uint&0xFF) * 100 // 8-bit percent → 16-bit percent100ths
		case 1:
			req.LiftPercent100thsValue = uint16(el.Uint & 0xFFFF) // 16-bit field per spec
		}
	}); err != nil {
		return req, err
	}
	return req, nil
}

// DecodeGoToTiltPercentage parses GoToTiltPercentage payloads.
func DecodeGoToTiltPercentage(payload []byte) (GoToTiltPercentageRequest, error) {
	var req GoToTiltPercentageRequest
	if err := walkContext(payload, ErrWindowCoveringMalformed, func(tag uint32, el tlv.Element) {
		switch tag {
		case 0:
			req.TiltPercent100thsValue = uint16(el.Uint&0xFF) * 100 // 8-bit percent → 16-bit percent100ths
		case 1:
			req.TiltPercent100thsValue = uint16(el.Uint & 0xFFFF) // 16-bit field per spec
		}
	}); err != nil {
		return req, err
	}
	return req, nil
}

// DecodeGoToLiftValue parses GoToLiftValue payloads.
func DecodeGoToLiftValue(payload []byte) (GoToLiftValueRequest, error) {
	var req GoToLiftValueRequest
	if err := walkContext(payload, ErrWindowCoveringMalformed, func(tag uint32, el tlv.Element) {
		if tag == 0 {
			req.LiftValue = uint16(el.Uint & 0xFFFF) // 16-bit raw position per spec
		}
	}); err != nil {
		return req, err
	}
	return req, nil
}

// DecodeGoToTiltValue parses GoToTiltValue payloads.
func DecodeGoToTiltValue(payload []byte) (GoToTiltValueRequest, error) {
	var req GoToTiltValueRequest
	if err := walkContext(payload, ErrWindowCoveringMalformed, func(tag uint32, el tlv.Element) {
		if tag == 0 {
			req.TiltValue = uint16(el.Uint & 0xFFFF) // 16-bit raw position per spec
		}
	}); err != nil {
		return req, err
	}
	return req, nil
}
