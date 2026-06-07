// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package wire

import (
	"errors"

	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// ColorControlClusterID is the Matter cluster ID for ColorControl.
const ColorControlClusterID uint32 = 0x0300

// ColorControl attribute IDs per Matter §3.2.6.
const (
	ColorCtrlAttrCurrentHue             uint32 = 0x0000
	ColorCtrlAttrCurrentSaturation      uint32 = 0x0001
	ColorCtrlAttrCurrentX               uint32 = 0x0003
	ColorCtrlAttrCurrentY               uint32 = 0x0004
	ColorCtrlAttrColorTemperatureMireds uint32 = 0x0007
	ColorCtrlAttrColorMode              uint32 = 0x0008
	ColorCtrlAttrOptions                uint32 = 0x000F
	ColorCtrlAttrNumberOfPrimaries      uint32 = 0x0010 // nullable uint8, quality X; mandatory per spec §3.2.6
	ColorCtrlAttrEnhancedColorMode      uint32 = 0x4001
	ColorCtrlAttrColorCapabilities      uint32 = 0x400A
	ColorCtrlAttrColorTempPhysicalMin   uint32 = 0x400B
	ColorCtrlAttrColorTempPhysicalMax   uint32 = 0x400C
)

// ColorControl command IDs per Matter §3.2.7.
const (
	ColorCtrlCmdMoveToHue              uint32 = 0x00
	ColorCtrlCmdMoveHue                uint32 = 0x01
	ColorCtrlCmdStepHue                uint32 = 0x02
	ColorCtrlCmdMoveToSaturation       uint32 = 0x03
	ColorCtrlCmdMoveSaturation         uint32 = 0x04
	ColorCtrlCmdStepSaturation         uint32 = 0x05
	ColorCtrlCmdMoveToHueAndSaturation uint32 = 0x06
	ColorCtrlCmdMoveToColor            uint32 = 0x07
	ColorCtrlCmdMoveColor              uint32 = 0x08
	ColorCtrlCmdStepColor              uint32 = 0x09
	ColorCtrlCmdMoveToColorTemperature uint32 = 0x0A
	ColorCtrlCmdStopMoveStep           uint32 = 0x47
	ColorCtrlCmdMoveColorTemperature   uint32 = 0x4B
	ColorCtrlCmdStepColorTemperature   uint32 = 0x4C
)

// HueDirection values per Matter §3.2.7.4.2.
const (
	ColorHueDirShortest uint8 = 0
	ColorHueDirLongest  uint8 = 1
	ColorHueDirUp       uint8 = 2
	ColorHueDirDown     uint8 = 3
)

// ErrColorControlMalformed is returned for malformed ColorControl
// command payloads.
var ErrColorControlMalformed = errors.New("wire: ColorControl command malformed")

// MoveToHueRequest mirrors Matter §3.2.7.4.
type MoveToHueRequest struct {
	Hue             uint8
	Direction       uint8
	TransitionTime  uint16
	OptionsMask     uint8
	OptionsOverride uint8
}

// MoveToSaturationRequest mirrors Matter §3.2.7.7.
type MoveToSaturationRequest struct {
	Saturation      uint8
	TransitionTime  uint16
	OptionsMask     uint8
	OptionsOverride uint8
}

// MoveToHueAndSaturationRequest mirrors Matter §3.2.7.10.
type MoveToHueAndSaturationRequest struct {
	Hue             uint8
	Saturation      uint8
	TransitionTime  uint16
	OptionsMask     uint8
	OptionsOverride uint8
}

// MoveToColorTemperatureRequest mirrors Matter §3.2.7.18.
type MoveToColorTemperatureRequest struct {
	ColorTemperatureMireds uint16
	TransitionTime         uint16
	OptionsMask            uint8
	OptionsOverride        uint8
}

// MoveHueRequest mirrors Matter §3.2.7.5. Rate is in units per
// second; MoveMode 0 = Stop, 1 = Up, 3 = Down.
type MoveHueRequest struct {
	MoveMode        uint8
	Rate            uint8
	OptionsMask     uint8
	OptionsOverride uint8
}

// StepHueRequest mirrors Matter §3.2.7.6. StepMode 1 = Up, 3 = Down.
type StepHueRequest struct {
	StepMode        uint8
	StepSize        uint8
	TransitionTime  uint16
	OptionsMask     uint8
	OptionsOverride uint8
}

// MoveSaturationRequest mirrors Matter §3.2.7.8. Rate is in units
// per second; MoveMode 0 = Stop, 1 = Up, 3 = Down.
type MoveSaturationRequest struct {
	MoveMode        uint8
	Rate            uint8
	OptionsMask     uint8
	OptionsOverride uint8
}

// StepSaturationRequest mirrors Matter §3.2.7.9.
type StepSaturationRequest struct {
	StepMode        uint8
	StepSize        uint8
	TransitionTime  uint16
	OptionsMask     uint8
	OptionsOverride uint8
}

// ColorMoveMode values per Matter §3.2.7.5 / §3.2.7.8.
const (
	ColorMoveModeStop uint8 = 0
	ColorMoveModeUp   uint8 = 1
	ColorMoveModeDown uint8 = 3
)

// ColorStepMode values per Matter §3.2.7.6 / §3.2.7.9.
const (
	ColorStepModeUp   uint8 = 1
	ColorStepModeDown uint8 = 3
)

// DecodeMoveToHue parses a MoveToHue command payload.
func DecodeMoveToHue(payload []byte) (MoveToHueRequest, error) {
	var req MoveToHueRequest
	if err := walkContext(payload, ErrColorControlMalformed, func(tag uint32, el tlv.Element) {
		switch tag {
		case 0:
			req.Hue = uint8(el.Uint & 0xFF) // 8-bit field per spec
		case 1:
			req.Direction = uint8(el.Uint & 0xFF) // 8-bit enum per spec
		case 2:
			req.TransitionTime = uint16(el.Uint & 0xFFFF) // 16-bit field per spec
		case 3:
			req.OptionsMask = uint8(el.Uint & 0xFF) // 8-bit bitmap per spec
		case 4:
			req.OptionsOverride = uint8(el.Uint & 0xFF) // 8-bit bitmap per spec
		}
	}); err != nil {
		return req, err
	}
	return req, nil
}

// DecodeMoveToSaturation parses a MoveToSaturation command payload.
func DecodeMoveToSaturation(payload []byte) (MoveToSaturationRequest, error) {
	var req MoveToSaturationRequest
	if err := walkContext(payload, ErrColorControlMalformed, func(tag uint32, el tlv.Element) {
		switch tag {
		case 0:
			req.Saturation = uint8(el.Uint & 0xFF) // 8-bit field per spec
		case 1:
			req.TransitionTime = uint16(el.Uint & 0xFFFF) // 16-bit field per spec
		case 2:
			req.OptionsMask = uint8(el.Uint & 0xFF) // 8-bit bitmap per spec
		case 3:
			req.OptionsOverride = uint8(el.Uint & 0xFF) // 8-bit bitmap per spec
		}
	}); err != nil {
		return req, err
	}
	return req, nil
}

// DecodeMoveToHueAndSaturation parses a MoveToHueAndSaturation command
// payload.
func DecodeMoveToHueAndSaturation(payload []byte) (MoveToHueAndSaturationRequest, error) {
	var req MoveToHueAndSaturationRequest
	if err := walkContext(payload, ErrColorControlMalformed, func(tag uint32, el tlv.Element) {
		switch tag {
		case 0:
			req.Hue = uint8(el.Uint & 0xFF) // 8-bit field per spec
		case 1:
			req.Saturation = uint8(el.Uint & 0xFF) // 8-bit field per spec
		case 2:
			req.TransitionTime = uint16(el.Uint & 0xFFFF) // 16-bit field per spec
		case 3:
			req.OptionsMask = uint8(el.Uint & 0xFF) // 8-bit bitmap per spec
		case 4:
			req.OptionsOverride = uint8(el.Uint & 0xFF) // 8-bit bitmap per spec
		}
	}); err != nil {
		return req, err
	}
	return req, nil
}

// DecodeMoveHue parses a MoveHue command payload.
func DecodeMoveHue(payload []byte) (MoveHueRequest, error) {
	var req MoveHueRequest
	if err := walkContext(payload, ErrColorControlMalformed, func(tag uint32, el tlv.Element) {
		switch tag {
		case 0:
			req.MoveMode = uint8(el.Uint & 0xFF) // 8-bit enum per spec
		case 1:
			req.Rate = uint8(el.Uint & 0xFF) // 8-bit field per spec
		case 2:
			req.OptionsMask = uint8(el.Uint & 0xFF) // 8-bit bitmap per spec
		case 3:
			req.OptionsOverride = uint8(el.Uint & 0xFF) // 8-bit bitmap per spec
		}
	}); err != nil {
		return req, err
	}
	return req, nil
}

// DecodeStepHue parses a StepHue command payload.
func DecodeStepHue(payload []byte) (StepHueRequest, error) {
	var req StepHueRequest
	if err := walkContext(payload, ErrColorControlMalformed, func(tag uint32, el tlv.Element) {
		switch tag {
		case 0:
			req.StepMode = uint8(el.Uint & 0xFF) // 8-bit enum per spec
		case 1:
			req.StepSize = uint8(el.Uint & 0xFF) // 8-bit field per spec
		case 2:
			req.TransitionTime = uint16(el.Uint & 0xFFFF) // 16-bit field per spec
		case 3:
			req.OptionsMask = uint8(el.Uint & 0xFF) // 8-bit bitmap per spec
		case 4:
			req.OptionsOverride = uint8(el.Uint & 0xFF) // 8-bit bitmap per spec
		}
	}); err != nil {
		return req, err
	}
	return req, nil
}

// DecodeMoveSaturation parses a MoveSaturation command payload.
func DecodeMoveSaturation(payload []byte) (MoveSaturationRequest, error) {
	var req MoveSaturationRequest
	if err := walkContext(payload, ErrColorControlMalformed, func(tag uint32, el tlv.Element) {
		switch tag {
		case 0:
			req.MoveMode = uint8(el.Uint & 0xFF) // 8-bit enum per spec
		case 1:
			req.Rate = uint8(el.Uint & 0xFF) // 8-bit field per spec
		case 2:
			req.OptionsMask = uint8(el.Uint & 0xFF) // 8-bit bitmap per spec
		case 3:
			req.OptionsOverride = uint8(el.Uint & 0xFF) // 8-bit bitmap per spec
		}
	}); err != nil {
		return req, err
	}
	return req, nil
}

// DecodeStepSaturation parses a StepSaturation command payload.
func DecodeStepSaturation(payload []byte) (StepSaturationRequest, error) {
	var req StepSaturationRequest
	if err := walkContext(payload, ErrColorControlMalformed, func(tag uint32, el tlv.Element) {
		switch tag {
		case 0:
			req.StepMode = uint8(el.Uint & 0xFF) // 8-bit enum per spec
		case 1:
			req.StepSize = uint8(el.Uint & 0xFF) // 8-bit field per spec
		case 2:
			req.TransitionTime = uint16(el.Uint & 0xFFFF) // 16-bit field per spec
		case 3:
			req.OptionsMask = uint8(el.Uint & 0xFF) // 8-bit bitmap per spec
		case 4:
			req.OptionsOverride = uint8(el.Uint & 0xFF) // 8-bit bitmap per spec
		}
	}); err != nil {
		return req, err
	}
	return req, nil
}

// DecodeMoveToColorTemperature parses a MoveToColorTemperature command
// payload.
func DecodeMoveToColorTemperature(payload []byte) (MoveToColorTemperatureRequest, error) {
	var req MoveToColorTemperatureRequest
	if err := walkContext(payload, ErrColorControlMalformed, func(tag uint32, el tlv.Element) {
		switch tag {
		case 0:
			req.ColorTemperatureMireds = uint16(el.Uint & 0xFFFF) // 16-bit field per spec
		case 1:
			req.TransitionTime = uint16(el.Uint & 0xFFFF) // 16-bit field per spec
		case 2:
			req.OptionsMask = uint8(el.Uint & 0xFF) // 8-bit bitmap per spec
		case 3:
			req.OptionsOverride = uint8(el.Uint & 0xFF) // 8-bit bitmap per spec
		}
	}); err != nil {
		return req, err
	}
	return req, nil
}
