// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package wire

import (
	"errors"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// LevelControl command IDs per Matter §1.6.7.
const (
	LevelCtrlCmdMoveToLevel            uint32 = 0x00
	LevelCtrlCmdMove                   uint32 = 0x01
	LevelCtrlCmdStep                   uint32 = 0x02
	LevelCtrlCmdStop                   uint32 = 0x03
	LevelCtrlCmdMoveToLevelWithOnOff   uint32 = 0x04
	LevelCtrlCmdMoveWithOnOff          uint32 = 0x05
	LevelCtrlCmdStepWithOnOff          uint32 = 0x06
	LevelCtrlCmdStopWithOnOff          uint32 = 0x07
	LevelCtrlCmdMoveToClosestFrequency uint32 = 0x08
)

// MoveMode values per Matter §1.6.7.2.
const (
	LevelMoveModeUp   uint8 = 0
	LevelMoveModeDown uint8 = 1
)

// StepMode values per Matter §1.6.7.3.
const (
	LevelStepModeUp   uint8 = 0
	LevelStepModeDown uint8 = 1
)

// ErrLevelControlMalformed is returned for malformed LevelControl
// command payloads.
var ErrLevelControlMalformed = errors.New("wire: LevelControl command malformed")

// MoveToLevelRequest mirrors Matter §1.6.7.1.
type MoveToLevelRequest struct {
	Level           uint8
	TransitionTime  *uint16 // nullable
	OptionsMask     uint8
	OptionsOverride uint8
}

// MoveRequest mirrors Matter §1.6.7.2.
type MoveRequest struct {
	MoveMode        uint8
	Rate            *uint8 // nullable
	OptionsMask     uint8
	OptionsOverride uint8
}

// StepRequest mirrors Matter §1.6.7.3.
type StepRequest struct {
	StepMode        uint8
	StepSize        uint8
	TransitionTime  *uint16 // nullable
	OptionsMask     uint8
	OptionsOverride uint8
}

// StopRequest mirrors Matter §1.6.7.4.
type StopRequest struct {
	OptionsMask     uint8
	OptionsOverride uint8
}

// DecodeMoveToLevel parses MoveToLevel / MoveToLevelWithOnOff payloads.
func DecodeMoveToLevel(payload []byte) (MoveToLevelRequest, error) {
	var req MoveToLevelRequest
	if err := walkContext(payload, ErrLevelControlMalformed, func(tag uint32, el tlv.Element) {
		switch tag {
		case 0:
			req.Level = uint8(el.Uint) //nolint:gosec // 8-bit field per spec
		case 1:
			if el.IsNull {
				return
			}
			v := uint16(el.Uint) //nolint:gosec // 16-bit field per spec
			req.TransitionTime = &v
		case 2:
			req.OptionsMask = uint8(el.Uint) //nolint:gosec // 8-bit bitmap per spec
		case 3:
			req.OptionsOverride = uint8(el.Uint) //nolint:gosec // 8-bit bitmap per spec
		}
	}); err != nil {
		return req, err
	}
	return req, nil
}

// DecodeMove parses Move / MoveWithOnOff payloads.
func DecodeMove(payload []byte) (MoveRequest, error) {
	var req MoveRequest
	if err := walkContext(payload, ErrLevelControlMalformed, func(tag uint32, el tlv.Element) {
		switch tag {
		case 0:
			req.MoveMode = uint8(el.Uint) //nolint:gosec // 8-bit enum per spec
		case 1:
			if el.IsNull {
				return
			}
			v := uint8(el.Uint) //nolint:gosec // 8-bit rate per spec
			req.Rate = &v
		case 2:
			req.OptionsMask = uint8(el.Uint) //nolint:gosec // 8-bit bitmap per spec
		case 3:
			req.OptionsOverride = uint8(el.Uint) //nolint:gosec // 8-bit bitmap per spec
		}
	}); err != nil {
		return req, err
	}
	return req, nil
}

// DecodeStep parses Step / StepWithOnOff payloads.
func DecodeStep(payload []byte) (StepRequest, error) {
	var req StepRequest
	if err := walkContext(payload, ErrLevelControlMalformed, func(tag uint32, el tlv.Element) {
		switch tag {
		case 0:
			req.StepMode = uint8(el.Uint) //nolint:gosec // 8-bit enum per spec
		case 1:
			req.StepSize = uint8(el.Uint) //nolint:gosec // 8-bit field per spec
		case 2:
			if el.IsNull {
				return
			}
			v := uint16(el.Uint) //nolint:gosec // 16-bit field per spec
			req.TransitionTime = &v
		case 3:
			req.OptionsMask = uint8(el.Uint) //nolint:gosec // 8-bit bitmap per spec
		case 4:
			req.OptionsOverride = uint8(el.Uint) //nolint:gosec // 8-bit bitmap per spec
		}
	}); err != nil {
		return req, err
	}
	return req, nil
}

// DecodeStop parses Stop / StopWithOnOff payloads.
func DecodeStop(payload []byte) (StopRequest, error) {
	var req StopRequest
	if err := walkContext(payload, ErrLevelControlMalformed, func(tag uint32, el tlv.Element) {
		switch tag {
		case 0:
			req.OptionsMask = uint8(el.Uint) //nolint:gosec // 8-bit bitmap per spec
		case 1:
			req.OptionsOverride = uint8(el.Uint) //nolint:gosec // 8-bit bitmap per spec
		}
	}); err != nil {
		return req, err
	}
	return req, nil
}

// walkContext is the common Structure-walker for context-tagged
// payloads. It opens the top struct, calls visit(tag, element) for
// each child, and stops at EndContainer.
func walkContext(payload []byte, sentinel error, visit func(tag uint32, el tlv.Element)) error {
	d := tlv.NewDecoder(payload)
	open, err := d.Next()
	if err != nil {
		return fmt.Errorf("%w: top: %w", sentinel, err)
	}
	if open.Type != tlv.TypeStructure {
		return fmt.Errorf("%w: top must be Structure", sentinel)
	}
	for {
		el, err := d.Next()
		if err != nil {
			return fmt.Errorf("%w: %w", sentinel, err)
		}
		if el.IsEndContainer {
			return nil
		}
		if el.Tag.Kind != tlv.TagKindContext {
			continue
		}
		visit(el.Tag.Number, el)
	}
}
