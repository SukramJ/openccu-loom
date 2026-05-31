// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmproto

import (
	"encoding/json"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ParameterData describes a single parameter inside a paramset.
//
// Numeric bounds (Min/Max/Default) arrive on the wire as values whose
// Go type depends on TYPE; we keep them as [json.RawMessage] so the
// original lexical form survives normalisation and hashing. The
// normaliser typed-coerces them when producing the canonical shape.
type ParameterData struct {
	Type       hmenum.ParameterType `json:"TYPE"`
	Flags      hmenum.Flag          `json:"FLAGS"`
	Operations hmenum.Operations    `json:"OPERATIONS"`

	Min     json.RawMessage `json:"MIN,omitempty"`
	Max     json.RawMessage `json:"MAX,omitempty"`
	Default json.RawMessage `json:"DEFAULT,omitempty"`

	Unit      string   `json:"UNIT,omitempty"`
	ValueList []string `json:"VALUE_LIST,omitempty"`
	TabOrder  *int     `json:"TAB_ORDER,omitempty"`

	// ID survives on the wire for some CCU firmwares.
	ID string `json:"ID,omitempty"`

	// Control hints (display-role for the CCU WebUI).
	Control string `json:"CONTROL,omitempty"`

	// Special is an arbitrary per-parameter blob; preserved verbatim.
	Special json.RawMessage `json:"SPECIAL,omitempty"`

	// Extra captures fields we do not model explicitly.
	Extra map[string]json.RawMessage `json:"-"`
}

// Signature returns a stable 16-character hex fingerprint of the
// parameter descriptor, derived from a JSON-canonical encoding via
// FNV-1a 64-bit. Used by the configuration coordinator as the cheap
// change-detection key — when the descriptor mutates (vendor update,
// CCU firmware bump, operator patch) the signature flips and the
// downstream reload pipeline kicks.
//
// Errors during JSON marshalling fall through to an empty string so
// callers can treat "no signature" as "force reload" without a panic.
func (p *ParameterData) Signature() string {
	buf, err := json.Marshal(p)
	if err != nil {
		return ""
	}
	const (
		fnvOffset64 = 1469598103934665603
		fnvPrime64  = 1099511628211
	)
	h := uint64(fnvOffset64)
	for _, b := range buf {
		h ^= uint64(b)
		h *= fnvPrime64
	}
	const hex = "0123456789abcdef"
	out := make([]byte, 16)
	for i := 15; i >= 0; i-- {
		out[i] = hex[h&0xF]
		h >>= 4
	}
	return string(out)
}

// IsReadable reports whether OPERATIONS has the READ bit set.
func (p *ParameterData) IsReadable() bool { return p.Operations.IsReadable() }

// IsWritable reports whether OPERATIONS has the WRITE bit set.
func (p *ParameterData) IsWritable() bool { return p.Operations.IsWritable() }

// IsEvent reports whether OPERATIONS has the EVENT bit set.
func (p *ParameterData) IsEvent() bool { return p.Operations.IsEvent() }

// IsVisible reports whether FLAGS has the VISIBLE bit set.
func (p *ParameterData) IsVisible() bool { return p.Flags.IsVisible() }

// IsInternal reports whether FLAGS has the INTERNAL bit set.
func (p *ParameterData) IsInternal() bool { return p.Flags.IsInternal() }

// IsService reports whether FLAGS has the SERVICE bit set.
func (p *ParameterData) IsService() bool { return p.Flags.IsService() }

// Paramset maps a parameter name to its description. The CCU returns
// a paramset as a map on the wire; our type keeps it that way and
// provides ordered iteration helpers for normalisation.
type Paramset map[string]ParameterData
