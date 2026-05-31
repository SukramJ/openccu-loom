// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package im

import (
	"errors"
	"fmt"
	"io"

	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// AttributePathIB tag numbers (Matter Core Spec §10.6.2).
const (
	tagAttrPathEnableTagCompression uint8 = 0
	tagAttrPathNode                 uint8 = 1
	tagAttrPathEndpoint             uint8 = 2
	tagAttrPathCluster              uint8 = 3
	tagAttrPathAttribute            uint8 = 4
	tagAttrPathListIndex            uint8 = 5
)

// CommandPathIB tag numbers (Matter Core Spec §10.6.2).
const (
	tagCmdPathEndpoint uint8 = 0
	tagCmdPathCluster  uint8 = 1
	tagCmdPathCommand  uint8 = 2
)

// Errors.
var (
	// ErrInvalidPath is returned when a TLV-encoded path violates
	// structural invariants (wrong container type, missing required
	// field, repeated field).
	ErrInvalidPath = errors.New("im: invalid path")
)

// ConcreteAttributePath identifies one attribute. Each Has* flag
// distinguishes "field present, value below" from "wildcard". The
// optional ListIndex points into an array attribute.
type ConcreteAttributePath struct {
	Node      uint64
	Endpoint  uint16
	Cluster   uint32
	Attribute uint32
	ListIndex uint16

	HasNode      bool
	HasEndpoint  bool
	HasCluster   bool
	HasAttribute bool
	HasListIndex bool
}

// IsWildcardEndpoint reports whether the path matches every endpoint.
func (p ConcreteAttributePath) IsWildcardEndpoint() bool { return !p.HasEndpoint }

// IsWildcardCluster reports whether the path matches every cluster.
func (p ConcreteAttributePath) IsWildcardCluster() bool { return !p.HasCluster }

// IsWildcardAttribute reports whether the path matches every attribute.
func (p ConcreteAttributePath) IsWildcardAttribute() bool { return !p.HasAttribute }

// MarshalTLV encodes p as a Matter AttributePathIB (LIST). The caller
// supplies the wrapping tag so the path can ride either anonymously
// (inside an Array) or context-tagged (inside an enclosing Structure).
//
// Field widths are deliberately magnitude-driven via [tlv.Encoder.PutUint]:
// Apple Home's MTRDevice IM-decoder accepts the narrow-encoded path
// fields (verified empirically — Apple parses 35 reports when paths
// ride as TypeUnsignedInt1 for sub-256 values), and the matter.js
// reference behaviour collapses to the same wire shape. The strict-
// width treatment is reserved for the AttributeData fields
// `DataVersion` (Matter §10.6.1.4 TlvUInt32) and the ReportData
// `SubscriptionId` (§10.6.4.1 TlvUInt32) — see read.go for those.
func (p ConcreteAttributePath) MarshalTLV(enc *tlv.Encoder, tag tlv.Tag) {
	enc.StartList(tag)
	if p.HasNode {
		enc.PutUint(tlv.ContextTag(tagAttrPathNode), p.Node)
	}
	if p.HasEndpoint {
		enc.PutUint(tlv.ContextTag(tagAttrPathEndpoint), uint64(p.Endpoint))
	}
	if p.HasCluster {
		enc.PutUint(tlv.ContextTag(tagAttrPathCluster), uint64(p.Cluster))
	}
	if p.HasAttribute {
		enc.PutUint(tlv.ContextTag(tagAttrPathAttribute), uint64(p.Attribute))
	}
	if p.HasListIndex {
		enc.PutUint(tlv.ContextTag(tagAttrPathListIndex), uint64(p.ListIndex))
	}
	_ = enc.EndContainer()
}

// UnmarshalAttributePathTLV reads a single AttributePathIB starting at
// the decoder's current position. The opening List element must
// already be ahead in the stream.
func UnmarshalAttributePathTLV(dec *tlv.Decoder) (ConcreteAttributePath, error) {
	open, err := dec.Next()
	if err != nil {
		return ConcreteAttributePath{}, err
	}
	if !open.IsContainer || open.Type != tlv.TypeList {
		return ConcreteAttributePath{}, fmt.Errorf("%w: expected list, got type 0x%02X", ErrInvalidPath, open.Type)
	}
	var p ConcreteAttributePath
	for {
		el, err := dec.Next()
		if err != nil {
			return ConcreteAttributePath{}, fmt.Errorf("%w: %w", ErrInvalidPath, err)
		}
		if el.IsEndContainer {
			return p, nil
		}
		if el.Tag.Kind != tlv.TagKindContext {
			return ConcreteAttributePath{}, fmt.Errorf("%w: non-context tag inside path", ErrInvalidPath)
		}
		switch uint8(el.Tag.Number) { //nolint:gosec // G115: context tags fit uint8 by IM spec
		case tagAttrPathNode:
			p.Node = el.Uint
			p.HasNode = true
		case tagAttrPathEndpoint:
			p.Endpoint = uint16(el.Uint) //nolint:gosec // G115: spec bounds endpoint to uint16
			p.HasEndpoint = true
		case tagAttrPathCluster:
			p.Cluster = uint32(el.Uint) //nolint:gosec // G115: spec bounds cluster to uint32
			p.HasCluster = true
		case tagAttrPathAttribute:
			p.Attribute = uint32(el.Uint) //nolint:gosec // G115: spec bounds attribute to uint32
			p.HasAttribute = true
		case tagAttrPathListIndex:
			p.ListIndex = uint16(el.Uint) //nolint:gosec // G115: spec bounds list index to uint16
			p.HasListIndex = true
		case tagAttrPathEnableTagCompression:
			// Tag-compression is an encoder-side optimisation we do
			// not honour on the decode path; spec §A.7 allows
			// receivers to ignore it. Skip silently.
		default:
			// Unknown context tag — Matter spec tolerates extra fields
			// for forward compatibility. Skip silently.
		}
	}
}

// ConcreteCommandPath identifies one cluster command on a specific
// endpoint. Endpoint is optional (group invocations omit it). Cluster
// and Command are mandatory on the wire — the Has* flags exist so a
// freshly-decoded path can be validated against the spec contract.
type ConcreteCommandPath struct {
	Endpoint uint16
	Cluster  uint32
	Command  uint32

	HasEndpoint bool
	HasCluster  bool
	HasCommand  bool
}

// MarshalTLV encodes p as a Matter CommandPathIB (LIST).
func (p ConcreteCommandPath) MarshalTLV(enc *tlv.Encoder, tag tlv.Tag) {
	enc.StartList(tag)
	if p.HasEndpoint {
		enc.PutUint(tlv.ContextTag(tagCmdPathEndpoint), uint64(p.Endpoint))
	}
	enc.PutUint(tlv.ContextTag(tagCmdPathCluster), uint64(p.Cluster))
	enc.PutUint(tlv.ContextTag(tagCmdPathCommand), uint64(p.Command))
	_ = enc.EndContainer()
}

// UnmarshalCommandPathTLV reads a single CommandPathIB.
func UnmarshalCommandPathTLV(dec *tlv.Decoder) (ConcreteCommandPath, error) {
	open, err := dec.Next()
	if err != nil {
		return ConcreteCommandPath{}, err
	}
	if !open.IsContainer || open.Type != tlv.TypeList {
		return ConcreteCommandPath{}, fmt.Errorf("%w: expected list, got type 0x%02X", ErrInvalidPath, open.Type)
	}
	var p ConcreteCommandPath
	for {
		el, err := dec.Next()
		if err != nil {
			return ConcreteCommandPath{}, fmt.Errorf("%w: %w", ErrInvalidPath, err)
		}
		if el.IsEndContainer {
			if !p.HasCluster || !p.HasCommand {
				return ConcreteCommandPath{}, fmt.Errorf("%w: missing required cluster/command", ErrInvalidPath)
			}
			return p, nil
		}
		if el.Tag.Kind != tlv.TagKindContext {
			return ConcreteCommandPath{}, fmt.Errorf("%w: non-context tag", ErrInvalidPath)
		}
		switch uint8(el.Tag.Number) { //nolint:gosec // G115: context tags fit uint8 by IM spec
		case tagCmdPathEndpoint:
			p.Endpoint = uint16(el.Uint) //nolint:gosec // G115: spec-bound
			p.HasEndpoint = true
		case tagCmdPathCluster:
			p.Cluster = uint32(el.Uint) //nolint:gosec // G115: spec-bound
			p.HasCluster = true
		case tagCmdPathCommand:
			p.Command = uint32(el.Uint) //nolint:gosec // G115: spec-bound
			p.HasCommand = true
		}
	}
}

// EOF guard for the decoder helpers above.
var _ = io.EOF
