// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package im

import (
	"context"
	"errors"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// WriteRequestMessage tag numbers (Matter Core Spec §10.6.5).
const (
	tagWriteReqSuppressResponse uint8 = 0
	tagWriteReqTimedRequest     uint8 = 1
	tagWriteReqWriteRequests    uint8 = 2
	tagWriteReqMoreChunked      uint8 = 3
)

// WriteResponseMessage tag numbers.
const (
	tagWriteRespResponses uint8 = 0
)

// Errors.
var (
	// ErrInvalidWriteRequest is returned for malformed write requests.
	ErrInvalidWriteRequest = errors.New("im: invalid WriteRequest")
)

// WriteRequest is the in-memory form of a WriteRequestMessage.
type WriteRequest struct {
	SuppressResponse bool
	TimedRequest     bool
	Writes           []AttributeWrite
}

// AttributeWrite is one entry in WriteRequests — a (path, value)
// pair the Dispatcher applies.
type AttributeWrite struct {
	Path  ConcreteAttributePath
	Value AttributeValue
	// DataVersion is optional per spec; when non-zero the device
	// MUST surface DataVersionMismatch if its current version
	// differs.
	DataVersion    uint32
	HasDataVersion bool
}

// AttributeValueReader extracts an [AttributeValue] from the TLV
// stream at the decoder's current position. Cluster-native type
// decoding lives outside the IM layer; the reader is supplied by the
// caller — typically the endpoint assembler.
//
// The path argument carries the AttributeDataIB's path (decoded just
// before this call) so the reader can dispatch on (cluster, attribute)
// to a cluster-native decoder. dec is positioned at the value's first
// element when el.IsContainer is true — the reader is responsible for
// draining the container before returning.
type AttributeValueReader func(path ConcreteAttributePath, el tlv.Element, dec *tlv.Decoder) (AttributeValue, error)

// UnmarshalWriteRequestTLV decodes a WriteRequestMessage. valueReader
// is invoked for the Data field of each AttributeDataIB; pass a
// [DefaultValueReader]-style helper that captures the Element tree
// when cluster-aware decoding is not yet available.
func UnmarshalWriteRequestTLV(dec *tlv.Decoder, valueReader AttributeValueReader) (WriteRequest, error) {
	open, err := dec.Next()
	if err != nil {
		return WriteRequest{}, err
	}
	if !open.IsContainer || open.Type != tlv.TypeStructure {
		return WriteRequest{}, fmt.Errorf("%w: expected struct, got 0x%02X", ErrInvalidWriteRequest, open.Type)
	}
	var req WriteRequest
	for {
		el, err := dec.Next()
		if err != nil {
			return WriteRequest{}, fmt.Errorf("%w: %w", ErrInvalidWriteRequest, err)
		}
		if el.IsEndContainer {
			return req, nil
		}
		if el.Tag.Kind != tlv.TagKindContext {
			continue
		}
		switch uint8(el.Tag.Number & 0xFF) {
		case tagWriteReqSuppressResponse:
			req.SuppressResponse = el.Bool
		case tagWriteReqTimedRequest:
			req.TimedRequest = el.Bool
		case tagWriteReqWriteRequests:
			if !el.IsContainer || el.Type != tlv.TypeArray {
				return WriteRequest{}, fmt.Errorf("%w: WriteRequests not array", ErrInvalidWriteRequest)
			}
			ws, err := readAttributeWrites(dec, valueReader)
			if err != nil {
				return WriteRequest{}, err
			}
			req.Writes = ws
		default:
			if el.IsContainer {
				if err := skipContainer(dec); err != nil {
					return WriteRequest{}, err
				}
			}
		}
	}
}

func readAttributeWrites(dec *tlv.Decoder, valueReader AttributeValueReader) ([]AttributeWrite, error) {
	var out []AttributeWrite
	for {
		el, err := dec.Next()
		if err != nil {
			return nil, err
		}
		if el.IsEndContainer {
			return out, nil
		}
		if !el.IsContainer || el.Type != tlv.TypeStructure {
			return nil, fmt.Errorf("%w: AttributeDataIB not struct", ErrInvalidWriteRequest)
		}
		w, err := readAttributeData(dec, valueReader)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
}

func readAttributeData(dec *tlv.Decoder, valueReader AttributeValueReader) (AttributeWrite, error) {
	var w AttributeWrite
	for {
		el, err := dec.Next()
		if err != nil {
			return AttributeWrite{}, err
		}
		if el.IsEndContainer {
			return w, nil
		}
		if el.Tag.Kind != tlv.TagKindContext {
			continue
		}
		switch uint8(el.Tag.Number & 0xFF) {
		case tagAttributeDataDataVersion:
			w.DataVersion = uint32(el.Uint & 0xFFFFFFFF)
			w.HasDataVersion = true
		case tagAttributeDataPath:
			if !el.IsContainer || el.Type != tlv.TypeList {
				return AttributeWrite{}, fmt.Errorf("%w: path not list", ErrInvalidWriteRequest)
			}
			p, err := readAttributePathFields(dec)
			if err != nil {
				return AttributeWrite{}, err
			}
			w.Path = p
		case tagAttributeDataValue:
			if valueReader == nil {
				// Without a cluster-aware reader we cannot turn the
				// element tree into a Go value. Drain the container so
				// the surrounding decoder loop continues — the
				// downstream Dispatcher.Write will see a nil Value and
				// answer UnsupportedWrite.
				if el.IsContainer {
					if err := skipContainer(dec); err != nil {
						return AttributeWrite{}, fmt.Errorf("%w: drain value: %w", ErrInvalidWriteRequest, err)
					}
				}
				continue
			}
			v, err := valueReader(w.Path, el, dec)
			if err != nil {
				return AttributeWrite{}, fmt.Errorf("%w: value: %w", ErrInvalidWriteRequest, err)
			}
			w.Value = v
		}
	}
}

// WriteResponse is the in-memory form of a WriteResponseMessage.
type WriteResponse struct {
	Responses []AttributeStatus
}

// AttributeStatus is one entry in WriteResponses.
type AttributeStatus struct {
	Path   ConcreteAttributePath
	Status StatusIB
}

// MarshalTLV encodes wr.
func (wr WriteResponse) MarshalTLV(enc *tlv.Encoder) {
	enc.StartStruct(tlv.AnonymousTag())
	enc.StartArray(tlv.ContextTag(tagWriteRespResponses))
	for _, st := range wr.Responses {
		enc.StartStruct(tlv.AnonymousTag())
		st.Path.MarshalTLV(enc, tlv.ContextTag(tagAttributeStatusPath))
		st.Status.MarshalTLV(enc, tlv.ContextTag(tagAttributeStatusStatus))
		_ = enc.EndContainer()
	}
	_ = enc.EndContainer()
	enc.PutUint(tlv.ContextTag(tagInteractionModelRevision), uint64(MatterInteractionModelRevision))
	_ = enc.EndContainer()
}

// HandleWriteRequest dispatches a parsed WriteRequest through d and
// returns the assembled WriteResponse.
//
// DataVersion mismatch check: mirrors matter.js
// packages/node/src/node/server/InteractionServer.ts DataVersion check
// before write dispatch, and chip src/app/WriteHandler.cpp
// AttributeAccessInterface DataVersion verification. When the caller
// supplies a non-zero DataVersion in an AttributeDataIB and d implements
// [DataVersionReader], the cluster's current version is queried; a mismatch
// returns StatusDataVersionMismatch (0x92) instead of dispatching the write.
//
// ACL gate: mirrors matter.js
// packages/node/src/node/server/OnlineServerInteraction.ts
// FabricAccessControl.forRequest and chip src/app/WriteHandler.cpp:780
// "Execute the ACL Access Granting Algorithm before existence checks". When
// d implements [ACLChecker], the requesting fabric's privilege is verified
// before each write path is dispatched. The required privilege for a plain
// Write is Operate (3) per Matter §9.10.4.4. fabricIndex is extracted via
// [FabricFilterFromContext]; fabricIndex==0 (PASE) bypasses the ACL check.
func HandleWriteRequest(ctx context.Context, d Dispatcher, req WriteRequest) WriteResponse {
	_, fabricIndex := FabricFilterFromContext(ctx)
	subjectNodeID, subjectCATs := SubjectFromContext(ctx)
	aclChecker, hasACL := d.(ACLChecker)
	privProvider, hasPrivProvider := d.(AttributeWritePrivilegeProvider)
	dvReader, hasDV := d.(DataVersionReader)

	// writePrivilege returns the minimum privilege needed to write the
	// given (endpoint, cluster, attribute). Falls back to Operate (3) —
	// the Matter §9.10.4.4 default write privilege — when no
	// AttributeWritePrivilegeProvider is wired, the path is not concrete,
	// or the attribute has no elevated requirement.
	writePrivilege := func(w AttributeWrite) uint8 {
		const privilegeOperate uint8 = 3
		if hasPrivProvider && w.Path.HasAttribute {
			return privProvider.MinWritePrivilege(w.Path.Endpoint, w.Path.Cluster, w.Path.Attribute)
		}
		return privilegeOperate
	}

	var wr WriteResponse
	for _, w := range req.Writes {
		// ACL gate. PASE (fabricIndex==0) skips ACL: commissioning writes
		// arrive before the fabric's ACL entry exists. The required
		// privilege is per-attribute (AccessControl.ACL → Administer,
		// BasicInformation.NodeLabel → Manage) rather than a flat Operate,
		// so an Operate-only subject cannot escalate via a privileged write.
		if hasACL && fabricIndex != 0 {
			if status := aclChecker.CheckACL(ctx, fabricIndex, subjectNodeID, subjectCATs, w.Path.Endpoint, w.Path.Cluster, writePrivilege(w)); !status.IsSuccess() {
				wr.Responses = append(wr.Responses, AttributeStatus{
					Path:   w.Path,
					Status: StatusIB{Status: status},
				})
				continue
			}
		}
		// A write that carries a DataVersion MUST target a concrete endpoint;
		// a DataVersion is meaningless against a wildcard-endpoint path.
		// Mirrors matter.js packages/protocol/src/action/request/Write.ts:33
		// (#3988, Matter §8.9.2.8.1): reject with InvalidAction rather than
		// resolving the version against endpoint 0.
		if w.HasDataVersion && !w.Path.HasEndpoint {
			wr.Responses = append(wr.Responses, AttributeStatus{
				Path:   w.Path,
				Status: StatusIB{Status: StatusInvalidAction},
			})
			continue
		}
		// DataVersion mismatch check. Mirrors matter.js InteractionServer.ts
		// version check and chip WriteHandler.cpp DataVersion guard. A zero
		// DataVersion in the request means "caller does not constrain the
		// version" — skip.
		if hasDV && w.HasDataVersion && w.DataVersion != 0 {
			if current, ok := dvReader.CurrentDataVersion(ctx, w.Path.Endpoint, w.Path.Cluster); ok && current != 0 {
				if current != w.DataVersion {
					wr.Responses = append(wr.Responses, AttributeStatus{
						Path:   w.Path,
						Status: StatusIB{Status: StatusDataVersionMismatch},
					})
					continue
				}
			}
		}
		for _, res := range d.Write(ctx, w.Path, w.Value) {
			// When a cluster-specific status is conveyed, the outer global
			// status MUST be FAILURE (not a more specific global code).
			// Mirrors matter.js
			// packages/protocol/src/action/server/AttributeWriteResponse.ts:32
			// (#3988, Matter §7.10.7).
			if res.HasClusterStatus && res.Status != StatusSuccess {
				res.Status = StatusFailure
			}
			wr.Responses = append(wr.Responses, AttributeStatus{
				Path:   res.Path,
				Status: StatusIB{Status: res.Status, ClusterStatus: res.ClusterStatus, HasClusterStatus: res.HasClusterStatus},
			})
		}
	}
	return wr
}
