// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package im

import (
	"errors"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// StatusCodeError is the optional interface cluster-server errors
// implement when they carry a well-typed Matter IM status code.
// The dispatcher type-asserts against this interface FIRST in
// [writeErrorStatus] / [invokeErrorStatus]; the string-heuristic
// fallback is used only for errors that do not implement it.
//
// Pattern: define a package-private sentinel type that wraps an
// error string and returns its status code, then export an
// errors.New-style variable that implements this interface. The
// dispatcher then needs no import of the cluster package:
//
//	type busyError struct{}
//	func (busyError) Error() string          { return "busy" }
//	func (busyError) MatterStatusCode() StatusCode { return StatusBusy }
//	var ErrBusy StatusCodeError = busyError{}
//
// This replaces fragile string-contains heuristics with exact typed
// dispatch.
type StatusCodeError interface {
	MatterStatusCode() StatusCode
	Error() string
}

// MatterClusterStatusError extends [StatusCodeError] for cluster-side
// command handlers that need to return a cluster-specific status code
// alongside the generic IM status code (Matter §10.6.2.2 Table 53).
// When the dispatcher finds this interface via errors.As, it encodes
// StatusIB with both Status=IMStatus and ClusterStatus=clusterCode.
//
// Use case: AdministratorCommissioning §11.19.7.3 PAKEParameterError
// (cluster-specific code 0x02) alongside Status=ClusterSpecificFailure
// (0x01). Implement both MatterStatusCode() and MatterClusterStatus():
//
//	type pakeErr struct{}
//	func (pakeErr) Error() string            { return "PAKE parameter error" }
//	func (pakeErr) MatterStatusCode() StatusCode { return StatusFailure }
//	func (pakeErr) MatterClusterStatus() uint8   { return 0x02 }
type MatterClusterStatusError interface {
	StatusCodeError
	// MatterClusterStatus returns the cluster-specific status code to
	// embed in StatusIB.ClusterStatus per Matter Core Spec §10.6.2.2.
	MatterClusterStatus() uint8
}

// StatusCode is a Matter Interaction Model status code per Core Spec
// §8.10 Table 96. Only the values that have a non-trivial mapping to
// our [Dispatcher] surface are spelled out — extending is a one-line
// constant addition.
type StatusCode uint8

// StatusCode values.
const (
	StatusSuccess                StatusCode = 0x00
	StatusFailure                StatusCode = 0x01
	StatusInvalidSubscription    StatusCode = 0x7d
	StatusUnsupportedAccess      StatusCode = 0x7e
	StatusUnsupportedEndpoint    StatusCode = 0x7f
	StatusInvalidAction          StatusCode = 0x80
	StatusUnsupportedCommand     StatusCode = 0x81
	StatusInvalidCommand         StatusCode = 0x85
	StatusUnsupportedAttribute   StatusCode = 0x86
	StatusConstraintError        StatusCode = 0x87
	StatusUnsupportedWrite       StatusCode = 0x88
	StatusResourceExhausted      StatusCode = 0x89
	StatusNotFound               StatusCode = 0x8b
	StatusUnreportableAttr       StatusCode = 0x8c
	StatusInvalidDataType        StatusCode = 0x8d
	StatusUnsupportedRead        StatusCode = 0x8f
	StatusDataVersionMismatch    StatusCode = 0x92
	StatusTimeout                StatusCode = 0x94
	StatusBusy                   StatusCode = 0x9c
	StatusUnsupportedCluster     StatusCode = 0xc3
	StatusNoUpstreamSubscription StatusCode = 0xc5
	StatusNeedsTimedInteraction  StatusCode = 0xc6
	StatusUnsupportedEvent       StatusCode = 0xc7

	// Matter 1.4–1.5 additions per matter.js
	// packages/types/src/globals/Status.ts (HEAD 2025-05).
	StatusUnsupportedNode           StatusCode = 0x9b
	StatusAccessRestricted          StatusCode = 0x9d
	StatusPathsExhausted            StatusCode = 0xc8
	StatusTimedRequestMismatch      StatusCode = 0xc9
	StatusFailsafeRequired          StatusCode = 0xca
	StatusInvalidInState            StatusCode = 0xcb
	StatusNoCommandResponse         StatusCode = 0xcc
	StatusTermsAndConditionsChanged StatusCode = 0xcd
	StatusMaintenanceRequired       StatusCode = 0xce
	StatusDynamicConstraintError    StatusCode = 0xcf
	StatusAlreadyExists             StatusCode = 0xd0
	StatusInvalidTransportType      StatusCode = 0xd1
)

// String returns a short label for diagnostics; not part of the wire
// format.
func (s StatusCode) String() string {
	switch s {
	case StatusSuccess:
		return "Success"
	case StatusFailure:
		return "Failure"
	case StatusUnsupportedAttribute:
		return "UnsupportedAttribute"
	case StatusUnsupportedCommand:
		return "UnsupportedCommand"
	case StatusUnsupportedCluster:
		return "UnsupportedCluster"
	case StatusUnsupportedEndpoint:
		return "UnsupportedEndpoint"
	case StatusUnsupportedWrite:
		return "UnsupportedWrite"
	case StatusUnsupportedRead:
		return "UnsupportedRead"
	case StatusInvalidAction:
		return "InvalidAction"
	case StatusInvalidCommand:
		return "InvalidCommand"
	case StatusConstraintError:
		return "ConstraintError"
	case StatusResourceExhausted:
		return "ResourceExhausted"
	case StatusBusy:
		return "Busy"
	case StatusTimeout:
		return "Timeout"
	default:
		return fmt.Sprintf("Status(0x%02X)", uint8(s))
	}
}

// IsSuccess reports whether the status code is the canonical success
// value (0x00). Used at every Dispatcher boundary to short-circuit
// error paths.
func (s StatusCode) IsSuccess() bool { return s == StatusSuccess }

// StatusIB tag numbers (Matter Core Spec §10.6.2).
const (
	tagStatusIBStatus        uint8 = 0
	tagStatusIBClusterStatus uint8 = 1
)

// StatusIB is the Status Information Block — the Matter-level
// (status, cluster-status) tuple that rides inside Read / Write /
// Invoke responses.
type StatusIB struct {
	Status           StatusCode
	ClusterStatus    uint8
	HasClusterStatus bool
}

// Errors.
var (
	// ErrInvalidStatusIB surfaces when a TLV-encoded StatusIB is
	// malformed — wrong container type, missing required field.
	ErrInvalidStatusIB = errors.New("im: invalid StatusIB")
)

// MarshalTLV encodes s as a StatusIB (Structure with two context tags).
func (s StatusIB) MarshalTLV(enc *tlv.Encoder, tag tlv.Tag) {
	enc.StartStruct(tag)
	enc.PutUint(tlv.ContextTag(tagStatusIBStatus), uint64(s.Status))
	if s.HasClusterStatus {
		enc.PutUint(tlv.ContextTag(tagStatusIBClusterStatus), uint64(s.ClusterStatus))
	}
	_ = enc.EndContainer()
}

// UnmarshalStatusIBTLV decodes a StatusIB from the next element in
// the decoder.
func UnmarshalStatusIBTLV(dec *tlv.Decoder) (StatusIB, error) {
	open, err := dec.Next()
	if err != nil {
		return StatusIB{}, err
	}
	if !open.IsContainer || open.Type != tlv.TypeStructure {
		return StatusIB{}, fmt.Errorf("%w: expected struct, got 0x%02X", ErrInvalidStatusIB, open.Type)
	}
	var sib StatusIB
	sawStatus := false
	for {
		el, err := dec.Next()
		if err != nil {
			return StatusIB{}, fmt.Errorf("%w: %w", ErrInvalidStatusIB, err)
		}
		if el.IsEndContainer {
			if !sawStatus {
				return StatusIB{}, fmt.Errorf("%w: missing Status", ErrInvalidStatusIB)
			}
			return sib, nil
		}
		if el.Tag.Kind != tlv.TagKindContext {
			continue
		}
		switch uint8(el.Tag.Number) { //nolint:gosec // G115: context tags fit uint8 by IM spec
		case tagStatusIBStatus:
			sib.Status = StatusCode(el.Uint) //nolint:gosec // G115: status fits uint8 by definition
			sawStatus = true
		case tagStatusIBClusterStatus:
			sib.ClusterStatus = uint8(el.Uint) //nolint:gosec // G115: spec-bound
			sib.HasClusterStatus = true
		}
	}
}
