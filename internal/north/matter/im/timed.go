// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package im

import (
	"errors"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// TimedRequestMessage tag numbers per Matter Core Spec §10.6.10.
const (
	tagTimedReqTimeout                  uint8 = 0
	tagTimedReqInteractionModelRevision uint8 = 0xFF
)

// StatusResponseMessage tag numbers per Matter Core Spec §10.6.9.
const (
	tagStatusResponseStatus                   uint8 = 0
	tagStatusResponseInteractionModelRevision uint8 = 0xFF
)

// InteractionModelRevision is kept for back-compat with timed.go's
// existing call sites. Use [MatterInteractionModelRevision] (defined
// in subscribe.go) for new code.
//
// matter.js v0.16.10 emits 13 (Matter 1.5) on every IM message; the
// previous value of 11 made Apple Home tag our subscribe transactions
// as stale-protocol and time them out after ~10 s.
//
// Original doc:
// InteractionModelRevision is the IM protocol revision the bridge
// advertises in TimedRequest replies and StatusResponses (Matter
// §8.1.4 — bumped to 11 in 1.5.1; older controllers accept lower
// values gracefully).
const InteractionModelRevision uint8 = 13

// Errors.
var (
	// ErrInvalidTimedRequest is returned for malformed TimedRequests.
	ErrInvalidTimedRequest = errors.New("im: invalid TimedRequest")
)

// TimedRequest is the in-memory form of a TimedRequestMessage. The
// commissioner sends one before a Write/Invoke that the spec
// requires to be timed (e.g. door-lock unlocks). The bridge replies
// with a [StatusResponse]{Success} and stamps a per-exchange
// deadline; the matching follow-up Write/Invoke is then gated by
// `Bridge.checkTimedGate` per Matter §8.7 — late or missing
// follow-ups get rejected with TIMEOUT (0x94) or
// NEEDS_TIMED_INTERACTION (0xC6).
type TimedRequest struct {
	// TimeoutMs is the maximum number of milliseconds the bridge
	// must wait between this TimedRequest and the follow-up
	// Write / Invoke that it gates. Per Matter §10.7.1 the value is
	// uint16 (max 65 535 ms ≈ 65 s).
	TimeoutMs uint16
}

// UnmarshalTimedRequestTLV parses a TimedRequestMessage TLV payload.
func UnmarshalTimedRequestTLV(dec *tlv.Decoder) (TimedRequest, error) {
	var req TimedRequest
	open, err := dec.Next()
	if err != nil {
		return req, fmt.Errorf("%w: top: %w", ErrInvalidTimedRequest, err)
	}
	if open.Type != tlv.TypeStructure {
		return req, fmt.Errorf("%w: top must be Structure", ErrInvalidTimedRequest)
	}
	for {
		el, err := dec.Next()
		if err != nil {
			return req, fmt.Errorf("%w: %w", ErrInvalidTimedRequest, err)
		}
		if el.IsEndContainer {
			break
		}
		if el.Tag.Kind != tlv.TagKindContext {
			continue
		}
		switch uint8(el.Tag.Number & 0xFF) {
		case tagTimedReqTimeout:
			req.TimeoutMs = uint16(el.Uint & 0xFFFF)
		case tagTimedReqInteractionModelRevision:
			// Decoded but not retained — the IM-revision field is
			// informational; we always reply with our own revision.
		}
	}
	return req, nil
}

// StatusResponse is the in-memory form of a StatusResponseMessage.
// The bridge emits it as the reply to a TimedRequest (always
// Success), and could in principle emit one for any inbound
// IM-action error condition — the IM Read/Write/Invoke handlers
// instead embed status into their dedicated response shapes today.
type StatusResponse struct {
	Status StatusCode
}

// MarshalTLV encodes sr at the top level (anonymous tag).
func (sr StatusResponse) MarshalTLV(enc *tlv.Encoder) {
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutUint(tlv.ContextTag(tagStatusResponseStatus), uint64(sr.Status))
	enc.PutUint(tlv.ContextTag(tagStatusResponseInteractionModelRevision), uint64(InteractionModelRevision))
	_ = enc.EndContainer()
}
