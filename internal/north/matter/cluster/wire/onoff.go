// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package wire

import (
	"errors"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// OnOff command IDs per Matter §1.5.6.
const (
	OnOffCmdOff                     uint32 = 0x00
	OnOffCmdOn                      uint32 = 0x01
	OnOffCmdToggle                  uint32 = 0x02
	OnOffCmdOffWithEffect           uint32 = 0x40
	OnOffCmdOnWithRecallGlobalScene uint32 = 0x41
	OnOffCmdOnWithTimedOff          uint32 = 0x42
)

// Errors.
var (
	// ErrOnOffMalformed is returned for malformed OnOff command payloads.
	ErrOnOffMalformed = errors.New("wire: OnOff command malformed")
)

// OffWithEffectRequest mirrors Matter §1.5.7.4.
type OffWithEffectRequest struct {
	EffectIdentifier uint8
	EffectVariant    uint8
}

// OnWithTimedOffRequest mirrors Matter §1.5.7.6.
type OnWithTimedOffRequest struct {
	OnOffControl uint8  // bit 0 = AcceptOnlyWhenOn
	OnTime       uint16 // 1/10s units
	OffWaitTime  uint16 // 1/10s units
}

// DecodeOffWithEffect parses the OffWithEffect command payload.
func DecodeOffWithEffect(payload []byte) (OffWithEffectRequest, error) {
	var req OffWithEffectRequest
	d := tlv.NewDecoder(payload)
	open, err := d.Next()
	if err != nil {
		return req, fmt.Errorf("%w: top: %w", ErrOnOffMalformed, err)
	}
	if open.Type != tlv.TypeStructure {
		return req, fmt.Errorf("%w: top must be Structure", ErrOnOffMalformed)
	}
	for {
		el, err := d.Next()
		if err != nil {
			return req, fmt.Errorf("%w: %w", ErrOnOffMalformed, err)
		}
		if el.IsEndContainer {
			break
		}
		if el.Tag.Kind != tlv.TagKindContext {
			continue
		}
		switch el.Tag.Number {
		case 0:
			req.EffectIdentifier = uint8(el.Uint & 0xFF) // payload field width 1 byte per spec
		case 1:
			req.EffectVariant = uint8(el.Uint & 0xFF) // payload field width 1 byte per spec
		}
	}
	return req, nil
}

// DecodeOnWithTimedOff parses the OnWithTimedOff command payload.
func DecodeOnWithTimedOff(payload []byte) (OnWithTimedOffRequest, error) {
	var req OnWithTimedOffRequest
	d := tlv.NewDecoder(payload)
	open, err := d.Next()
	if err != nil {
		return req, fmt.Errorf("%w: top: %w", ErrOnOffMalformed, err)
	}
	if open.Type != tlv.TypeStructure {
		return req, fmt.Errorf("%w: top must be Structure", ErrOnOffMalformed)
	}
	for {
		el, err := d.Next()
		if err != nil {
			return req, fmt.Errorf("%w: %w", ErrOnOffMalformed, err)
		}
		if el.IsEndContainer {
			break
		}
		if el.Tag.Kind != tlv.TagKindContext {
			continue
		}
		switch el.Tag.Number {
		case 0:
			req.OnOffControl = uint8(el.Uint & 0xFF) // 8-bit bitmap per spec
		case 1:
			req.OnTime = uint16(el.Uint & 0xFFFF) // 16-bit field per spec
		case 2:
			req.OffWaitTime = uint16(el.Uint & 0xFFFF) // 16-bit field per spec
		}
	}
	return req, nil
}
