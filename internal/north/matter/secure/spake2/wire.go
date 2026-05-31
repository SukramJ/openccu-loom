// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package spake2

import (
	"errors"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// PASE wire-format helpers per Matter Core Spec §4.13.5.
//
// Pake1, Pake2 and Pake3 each ride as a top-level TLV Structure with
// a small set of context-tagged octet-string fields:
//
//	Pake1 = { [1]: pA }
//	Pake2 = { [1]: pB, [2]: cB }
//	Pake3 = { [1]: cA }
//
// PBKDFParamRequest / PBKDFParamResponse are richer (they carry the
// salt + iterations chosen by the responder, plus an optional
// ResponderMRPParams sub-struct so a sleepy-end-device commissioner
// can size its retransmit timers to the bridge's active-period).

// PASE wire field tags (context-tagged inside the message struct).
const (
	tagPake1PA uint8 = 1
	tagPake2PB uint8 = 1
	tagPake2CB uint8 = 2
	tagPake3CA uint8 = 1
)

// PBKDFParamRequest field tags per Matter §4.13.5.2.
const (
	tagPBKDFReqInitiatorRandom    uint8 = 1
	tagPBKDFReqInitiatorSessionID uint8 = 2
	tagPBKDFReqPasscodeID         uint8 = 3
	tagPBKDFReqHasPBKDFParameters uint8 = 4
	tagPBKDFReqInitiatorMRPParams uint8 = 5 // optional, decoded as raw octets / skipped
)

// PBKDFParamResponse field tags per Matter §4.13.5.2.
const (
	tagPBKDFRespInitiatorRandom    uint8 = 1
	tagPBKDFRespResponderRandom    uint8 = 2
	tagPBKDFRespResponderSessionID uint8 = 3
	tagPBKDFRespPBKDFParameters    uint8 = 4 // nested Structure, only when requested
	tagPBKDFRespResponderMRPParams uint8 = 5 // optional, omitted in v1.1
)

// PBKDFParameters nested-struct field tags.
const (
	tagPBKDFParamsIterations uint8 = 1
	tagPBKDFParamsSalt       uint8 = 2
)

// MRPParameters nested-struct field tags per Matter §4.13.5.2 (used
// inside both InitiatorMRPParams and ResponderMRPParams). All three
// fields are optional; absent fields signal "use commissioner-side
// defaults".
const (
	tagMRPParamsIdleRetransTimeoutMs   uint8 = 1
	tagMRPParamsActiveRetransTimeoutMs uint8 = 2
	tagMRPParamsActiveThresholdTimeMs  uint8 = 3
)

// PBKDFRandomSize is the byte length of the InitiatorRandom /
// ResponderRandom fields per Matter §4.13.5.2.
const PBKDFRandomSize = 32

// Wire-format errors.
var (
	// ErrWireMalformed is returned when a Pake message does not parse
	// as the expected TLV shape (wrong top-level type, missing field,
	// or wrong field length).
	ErrWireMalformed = errors.New("spake2: malformed wire message")
)

// EncodePake1 builds the on-wire TLV bytes for a Pake1 message
// carrying pA. The bridge consumes Pake1 from the commissioner via
// [DecodePake1]; this encoder is here for symmetry / tests.
func EncodePake1(pA []byte) []byte {
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutOctets(tlv.ContextTag(tagPake1PA), pA)
	_ = enc.EndContainer()
	out, _ := enc.Bytes()
	return out
}

// DecodePake1 extracts the pA octet string from a Pake1 TLV payload.
// Returns [ErrWireMalformed] when the payload is not the expected
// shape.
func DecodePake1(payload []byte) ([]byte, error) {
	pA, err := decodeSingleOctet(payload, tagPake1PA, "Pake1")
	if err != nil {
		return nil, err
	}
	if len(pA) != PointSize {
		return nil, fmt.Errorf("%w: Pake1.pA length %d, want %d", ErrWireMalformed, len(pA), PointSize)
	}
	return pA, nil
}

// EncodePake2 builds the on-wire TLV bytes for a Pake2 message from
// the verifier's [Pake2Output].
func EncodePake2(out *Pake2Output) []byte {
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutOctets(tlv.ContextTag(tagPake2PB), out.Y)
	enc.PutOctets(tlv.ContextTag(tagPake2CB), out.CB)
	_ = enc.EndContainer()
	buf, _ := enc.Bytes()
	return buf
}

// DecodePake2 extracts pB (Y) + cB from a Pake2 TLV payload.
// Returns [ErrWireMalformed] for shape errors. The decoded fields
// should be lengths [PointSize] and [ConfirmTagSize] respectively;
// the function enforces both.
func DecodePake2(payload []byte) (pB, cB []byte, err error) {
	dec := tlv.NewDecoder(payload)
	open, err := dec.Next()
	if err != nil {
		return nil, nil, fmt.Errorf("%w: Pake2 top: %w", ErrWireMalformed, err)
	}
	if open.Type != tlv.TypeStructure {
		return nil, nil, fmt.Errorf("%w: Pake2 top must be Structure", ErrWireMalformed)
	}
	for {
		el, err := dec.Next()
		if err != nil {
			return nil, nil, fmt.Errorf("%w: %w", ErrWireMalformed, err)
		}
		if el.IsEndContainer {
			break
		}
		if el.Tag.Kind != tlv.TagKindContext {
			continue
		}
		switch uint8(el.Tag.Number) { //nolint:gosec // G115: context tag number is 0..7 in spec-conforming TLV per Matter A.7.2
		case tagPake2PB:
			pB = appendOctets(pB, el.Octets)
		case tagPake2CB:
			cB = appendOctets(cB, el.Octets)
		}
	}
	if len(pB) != PointSize {
		return nil, nil, fmt.Errorf("%w: Pake2.pB length %d, want %d", ErrWireMalformed, len(pB), PointSize)
	}
	if len(cB) != ConfirmTagSize {
		return nil, nil, fmt.Errorf("%w: Pake2.cB length %d, want %d", ErrWireMalformed, len(cB), ConfirmTagSize)
	}
	return pB, cB, nil
}

// EncodePake3 builds the on-wire TLV bytes for a Pake3 message
// carrying the prover confirmation tag cA.
func EncodePake3(cA []byte) []byte {
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutOctets(tlv.ContextTag(tagPake3CA), cA)
	_ = enc.EndContainer()
	out, _ := enc.Bytes()
	return out
}

// DecodePake3 extracts the cA octet string from a Pake3 TLV payload.
func DecodePake3(payload []byte) ([]byte, error) {
	cA, err := decodeSingleOctet(payload, tagPake3CA, "Pake3")
	if err != nil {
		return nil, err
	}
	if len(cA) != ConfirmTagSize {
		return nil, fmt.Errorf("%w: Pake3.cA length %d, want %d", ErrWireMalformed, len(cA), ConfirmTagSize)
	}
	return cA, nil
}

// decodeSingleOctet is the shared helper behind DecodePake1 +
// DecodePake3 (single-field Structure with one context-tagged octet
// string). Returns the octet bytes; length validation is the
// caller's job.
func decodeSingleOctet(payload []byte, wantTag uint8, msgName string) ([]byte, error) {
	dec := tlv.NewDecoder(payload)
	open, err := dec.Next()
	if err != nil {
		return nil, fmt.Errorf("%w: %s top: %w", ErrWireMalformed, msgName, err)
	}
	if open.Type != tlv.TypeStructure {
		return nil, fmt.Errorf("%w: %s top must be Structure", ErrWireMalformed, msgName)
	}
	var found []byte
	for {
		el, err := dec.Next()
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %w", ErrWireMalformed, msgName, err)
		}
		if el.IsEndContainer {
			break
		}
		if el.Tag.Kind == tlv.TagKindContext && el.Tag.Number == uint32(wantTag) {
			found = appendOctets(found, el.Octets)
		}
	}
	if found == nil {
		return nil, fmt.Errorf("%w: %s missing field tag %d", ErrWireMalformed, msgName, wantTag)
	}
	return found, nil
}

// appendOctets is a defensive copy. The TLV decoder's Octets slice
// aliases the input buffer which the caller may recycle; the
// adapter must take ownership before returning to the caller.
func appendOctets(dst, src []byte) []byte {
	if dst == nil {
		dst = make([]byte, 0, len(src))
	}
	return append(dst, src...)
}

// PBKDFParamRequest is the in-memory form of the commissioner's
// initial PASE message per Matter §4.13.5.2. The bridge consumes
// it via [DecodePBKDFParamRequest], assembles a matching
// [PBKDFParamResponse], and ships it back via the SecureChannel
// router's reply path.
type PBKDFParamRequest struct {
	// InitiatorRandom is the 32-byte random the commissioner picks
	// per request. The bridge echoes it verbatim in the response.
	InitiatorRandom []byte
	// InitiatorSessionID is the local session ID the commissioner
	// has reserved for the eventual PASE session. Goes into every
	// inbound encrypted Header.SessionID once the exchange completes.
	InitiatorSessionID uint16
	// PasscodeID identifies which passcode on the bridge the
	// commissioner expects to authenticate against. The default
	// passcode lives at PasscodeID=0; multi-passcode bridges use
	// > 0 to address per-fabric onboarding codes.
	PasscodeID uint16
	// HasPBKDFParameters reports whether the commissioner already
	// knows the bridge's PBKDF salt + iterations (e.g. carried in
	// the QR code or Manual Pairing Code). When true, the
	// responder omits the [PBKDFParamResponse.PBKDFParameters]
	// field; when false, the responder MUST include it.
	HasPBKDFParameters bool
}

// DecodePBKDFParamRequest parses a Matter PBKDFParamRequest TLV
// payload. The optional InitiatorMRPParams field (tag 5) is
// silently skipped — the bridge's MRP defaults match Matter's
// recommended values and operators have no knob to override
// commissioner-side params.
func DecodePBKDFParamRequest(payload []byte) (PBKDFParamRequest, error) {
	var req PBKDFParamRequest
	dec := tlv.NewDecoder(payload)
	open, err := dec.Next()
	if err != nil {
		return req, fmt.Errorf("%w: PBKDFParamRequest top: %w", ErrWireMalformed, err)
	}
	if open.Type != tlv.TypeStructure {
		return req, fmt.Errorf("%w: PBKDFParamRequest top must be Structure", ErrWireMalformed)
	}
	for {
		el, err := dec.Next()
		if err != nil {
			return req, fmt.Errorf("%w: %w", ErrWireMalformed, err)
		}
		if el.IsEndContainer {
			break
		}
		if el.Tag.Kind != tlv.TagKindContext {
			continue
		}
		switch uint8(el.Tag.Number) { //nolint:gosec // G115: context tag number is 0..7 in spec-conforming TLV per Matter A.7.2
		case tagPBKDFReqInitiatorRandom:
			req.InitiatorRandom = appendOctets(req.InitiatorRandom, el.Octets)
		case tagPBKDFReqInitiatorSessionID:
			req.InitiatorSessionID = uint16(el.Uint) //nolint:gosec // 16-bit field per spec
		case tagPBKDFReqPasscodeID:
			req.PasscodeID = uint16(el.Uint) //nolint:gosec // 16-bit field per spec
		case tagPBKDFReqHasPBKDFParameters:
			req.HasPBKDFParameters = el.Bool
		case tagPBKDFReqInitiatorMRPParams:
			// Skip the nested Structure if present — when a
			// commissioner sends MRP params we use defaults instead.
			if el.IsContainer {
				if err := skipContainer(dec); err != nil {
					return req, fmt.Errorf("%w: %w", ErrWireMalformed, err)
				}
			}
		}
	}
	if len(req.InitiatorRandom) != PBKDFRandomSize {
		return req, fmt.Errorf("%w: PBKDFParamRequest.InitiatorRandom length %d, want %d",
			ErrWireMalformed, len(req.InitiatorRandom), PBKDFRandomSize)
	}
	return req, nil
}

// PBKDFParamResponse is the bridge's reply per Matter §4.13.5.2. The
// caller fills in fields and calls [PBKDFParamResponse.Marshal] to
// get the wire bytes.
type PBKDFParamResponse struct {
	// InitiatorRandom MUST equal the value from the inbound
	// PBKDFParamRequest — Matter §4.13.5.2 ties the request /
	// response into the verifier's transcript hash.
	InitiatorRandom []byte
	// ResponderRandom is a fresh 32-byte random the bridge picks
	// per response; included in the verifier's transcript.
	ResponderRandom []byte
	// ResponderSessionID is the local session ID the bridge has
	// reserved for the eventual PASE session. Goes into every
	// outbound encrypted Header.SessionID once the exchange completes.
	ResponderSessionID uint16
	// Parameters is included only when the request flagged
	// HasPBKDFParameters=false; nil otherwise.
	Parameters *PBKDFParameters
	// ResponderMRPParams is the optional nested-struct that lets the
	// bridge advertise its MRP retransmit-timer profile to the
	// commissioner. Nil = omit the field; commissioners then fall
	// back to spec defaults (Matter §4.12 — IdleRetransTimeout 500ms,
	// ActiveRetransTimeout 300ms, ActiveThresholdTime 4000ms).
	ResponderMRPParams *MRPParameters
}

// MRPParameters carries the optional MRP retransmit-timer triplet a
// PASE peer may advertise inside PBKDFParamRequest.InitiatorMRPParams
// or PBKDFParamResponse.ResponderMRPParams. Each pointer field is
// independently optional — a nil leaves the corresponding TLV tag
// absent on the wire so the peer falls back to its own defaults
// (Matter §4.12.8).
type MRPParameters struct {
	IdleRetransTimeoutMs   *uint16
	ActiveRetransTimeoutMs *uint16
	ActiveThresholdTimeMs  *uint16
}

// PBKDFParameters carries the bridge's PBKDF salt + iteration count
// per Matter §4.13.5.2. The commissioner uses these to derive
// (w0, w1) on its side identically to the bridge.
type PBKDFParameters struct {
	Iterations uint32
	Salt       []byte
}

// PBKDF salt + iteration count bounds per Matter §3.10.3 (pbkdf2_*
// fields). Decoded responses outside these bounds are rejected so a
// hostile or buggy peer cannot push us into a brute-forceable region
// (low iterations) or DoS us with extreme values (huge iterations).
const (
	// PBKDFMinSaltSize is the lower bound on the PBKDF2 salt length
	// in bytes (Matter §3.10.3 — pbkdf2_iterations_salt MIN).
	PBKDFMinSaltSize = 16
	// PBKDFMaxSaltSize is the upper bound on the PBKDF2 salt length
	// in bytes (Matter §3.10.3 — pbkdf2_iterations_salt MAX).
	PBKDFMaxSaltSize = 32
	// PBKDFMinIterations is the lower bound on the PBKDF2 iteration
	// count (Matter §3.10.3 — pbkdf2_iterations MIN).
	PBKDFMinIterations uint32 = 1000
	// PBKDFMaxIterations is the upper bound on the PBKDF2 iteration
	// count (Matter §3.10.3 — pbkdf2_iterations MAX).
	PBKDFMaxIterations uint32 = 100000
)

// Validate reports whether the parameters are within the bounds the
// Matter spec mandates. Use this before deriving (w0, w1) from
// peer-supplied params; invalid bounds get wrapped in [ErrWireMalformed]
// by the response decoder.
func (p PBKDFParameters) Validate() error {
	if len(p.Salt) < PBKDFMinSaltSize || len(p.Salt) > PBKDFMaxSaltSize {
		return fmt.Errorf("%w: PBKDFParameters.Salt length %d, want %d..%d",
			ErrWireMalformed, len(p.Salt), PBKDFMinSaltSize, PBKDFMaxSaltSize)
	}
	if p.Iterations < PBKDFMinIterations || p.Iterations > PBKDFMaxIterations {
		return fmt.Errorf("%w: PBKDFParameters.Iterations %d, want %d..%d",
			ErrWireMalformed, p.Iterations, PBKDFMinIterations, PBKDFMaxIterations)
	}
	return nil
}

// Marshal encodes resp as a Matter PBKDFParamResponse TLV payload.
// Caller is expected to populate every required field; missing
// InitiatorRandom / ResponderRandom (wrong length) produces a
// well-formed but invalid payload — callers SHOULD validate before
// shipping. (We keep Marshal infallible to match the
// EncodePake1/2/3 pattern that already discards encoder errors.)
func (resp PBKDFParamResponse) Marshal() []byte {
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutOctets(tlv.ContextTag(tagPBKDFRespInitiatorRandom), resp.InitiatorRandom)
	enc.PutOctets(tlv.ContextTag(tagPBKDFRespResponderRandom), resp.ResponderRandom)
	enc.PutUint(tlv.ContextTag(tagPBKDFRespResponderSessionID), uint64(resp.ResponderSessionID))
	if resp.Parameters != nil {
		enc.StartStruct(tlv.ContextTag(tagPBKDFRespPBKDFParameters))
		enc.PutUint(tlv.ContextTag(tagPBKDFParamsIterations), uint64(resp.Parameters.Iterations))
		enc.PutOctets(tlv.ContextTag(tagPBKDFParamsSalt), resp.Parameters.Salt)
		_ = enc.EndContainer()
	}
	if resp.ResponderMRPParams != nil {
		enc.StartStruct(tlv.ContextTag(tagPBKDFRespResponderMRPParams))
		if v := resp.ResponderMRPParams.IdleRetransTimeoutMs; v != nil {
			enc.PutUint(tlv.ContextTag(tagMRPParamsIdleRetransTimeoutMs), uint64(*v))
		}
		if v := resp.ResponderMRPParams.ActiveRetransTimeoutMs; v != nil {
			enc.PutUint(tlv.ContextTag(tagMRPParamsActiveRetransTimeoutMs), uint64(*v))
		}
		if v := resp.ResponderMRPParams.ActiveThresholdTimeMs; v != nil {
			enc.PutUint(tlv.ContextTag(tagMRPParamsActiveThresholdTimeMs), uint64(*v))
		}
		_ = enc.EndContainer()
	}
	_ = enc.EndContainer()
	out, _ := enc.Bytes()
	return out
}

// DecodePBKDFParamResponse parses a Matter PBKDFParamResponse TLV
// payload. Provided for symmetry with the encoder + tests; the
// bridge itself only encodes responses (it never receives them).
func DecodePBKDFParamResponse(payload []byte) (PBKDFParamResponse, error) {
	var resp PBKDFParamResponse
	dec := tlv.NewDecoder(payload)
	open, err := dec.Next()
	if err != nil {
		return resp, fmt.Errorf("%w: PBKDFParamResponse top: %w", ErrWireMalformed, err)
	}
	if open.Type != tlv.TypeStructure {
		return resp, fmt.Errorf("%w: PBKDFParamResponse top must be Structure", ErrWireMalformed)
	}
	for {
		el, err := dec.Next()
		if err != nil {
			return resp, fmt.Errorf("%w: %w", ErrWireMalformed, err)
		}
		if el.IsEndContainer {
			break
		}
		if el.Tag.Kind != tlv.TagKindContext {
			continue
		}
		switch uint8(el.Tag.Number) { //nolint:gosec // G115: context tag number is 0..7 in spec-conforming TLV per Matter A.7.2
		case tagPBKDFRespInitiatorRandom:
			resp.InitiatorRandom = appendOctets(resp.InitiatorRandom, el.Octets)
		case tagPBKDFRespResponderRandom:
			resp.ResponderRandom = appendOctets(resp.ResponderRandom, el.Octets)
		case tagPBKDFRespResponderSessionID:
			resp.ResponderSessionID = uint16(el.Uint) //nolint:gosec // 16-bit field per spec
		case tagPBKDFRespPBKDFParameters:
			if !el.IsContainer {
				return resp, fmt.Errorf("%w: PBKDFParameters not a container", ErrWireMalformed)
			}
			params, err := decodePBKDFParameters(dec)
			if err != nil {
				return resp, err
			}
			resp.Parameters = &params
		case tagPBKDFRespResponderMRPParams:
			if !el.IsContainer {
				return resp, fmt.Errorf("%w: ResponderMRPParams not a container", ErrWireMalformed)
			}
			params, err := decodeMRPParameters(dec)
			if err != nil {
				return resp, err
			}
			resp.ResponderMRPParams = &params
		}
	}
	if len(resp.InitiatorRandom) != PBKDFRandomSize {
		return resp, fmt.Errorf("%w: PBKDFParamResponse.InitiatorRandom length %d, want %d",
			ErrWireMalformed, len(resp.InitiatorRandom), PBKDFRandomSize)
	}
	if len(resp.ResponderRandom) != PBKDFRandomSize {
		return resp, fmt.Errorf("%w: PBKDFParamResponse.ResponderRandom length %d, want %d",
			ErrWireMalformed, len(resp.ResponderRandom), PBKDFRandomSize)
	}
	return resp, nil
}

// decodePBKDFParameters consumes the nested PBKDFParameters
// structure off dec. The opening Structure element has already been
// consumed by the caller. Salt + iterations are validated against
// the Matter §3.10.3 bounds; out-of-range values wrap [ErrWireMalformed].
func decodePBKDFParameters(dec *tlv.Decoder) (PBKDFParameters, error) {
	var p PBKDFParameters
	for {
		el, err := dec.Next()
		if err != nil {
			return p, fmt.Errorf("%w: PBKDFParameters: %w", ErrWireMalformed, err)
		}
		if el.IsEndContainer {
			break
		}
		if el.Tag.Kind != tlv.TagKindContext {
			continue
		}
		switch uint8(el.Tag.Number) { //nolint:gosec // G115: context tag number is 0..7 in spec-conforming TLV per Matter A.7.2
		case tagPBKDFParamsIterations:
			p.Iterations = uint32(el.Uint) //nolint:gosec // 32-bit field per spec
		case tagPBKDFParamsSalt:
			p.Salt = appendOctets(p.Salt, el.Octets)
		}
	}
	if err := p.Validate(); err != nil {
		return p, err
	}
	return p, nil
}

// decodeMRPParameters consumes the nested MRPParameters structure
// off dec. The opening Structure element has already been consumed
// by the caller. All three sub-fields are optional; absent fields
// are reflected as nil pointers on the returned struct.
func decodeMRPParameters(dec *tlv.Decoder) (MRPParameters, error) {
	var p MRPParameters
	for {
		el, err := dec.Next()
		if err != nil {
			return p, fmt.Errorf("%w: MRPParameters: %w", ErrWireMalformed, err)
		}
		if el.IsEndContainer {
			break
		}
		if el.Tag.Kind != tlv.TagKindContext {
			continue
		}
		switch uint8(el.Tag.Number) { //nolint:gosec // G115: context tag number is 0..7 in spec-conforming TLV per Matter A.7.2
		case tagMRPParamsIdleRetransTimeoutMs:
			v := uint16(el.Uint) //nolint:gosec // 16-bit field per spec
			p.IdleRetransTimeoutMs = &v
		case tagMRPParamsActiveRetransTimeoutMs:
			v := uint16(el.Uint) //nolint:gosec // 16-bit field per spec
			p.ActiveRetransTimeoutMs = &v
		case tagMRPParamsActiveThresholdTimeMs:
			v := uint16(el.Uint) //nolint:gosec // 16-bit field per spec
			p.ActiveThresholdTimeMs = &v
		}
	}
	return p, nil
}

// skipContainer drains a nested container element by consuming
// elements until the matching EndContainer arrives. Used when the
// decoder must skip optional fields (e.g. MRPParams).
func skipContainer(dec *tlv.Decoder) error {
	depth := 1
	for depth > 0 {
		el, err := dec.Next()
		if err != nil {
			return fmt.Errorf("skipContainer: %w", err)
		}
		if el.IsContainer {
			depth++
			continue
		}
		if el.IsEndContainer {
			depth--
		}
	}
	return nil
}
