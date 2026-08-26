// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package im

import (
	"context"
	"errors"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// InvokeRequestMessage tag numbers (Matter Core Spec §10.6.7).
const (
	tagInvokeReqSuppressResponse uint8 = 0
	tagInvokeReqTimedRequest     uint8 = 1
	tagInvokeReqInvokeRequests   uint8 = 2
)

// InvokeResponseMessage tag numbers.
const (
	tagInvokeRespSuppressResponse uint8 = 0
	tagInvokeRespResponses        uint8 = 1
)

// DefaultMaxPathsPerInvoke is the ceiling on CommandDataIBs a single
// InvokeRequest batch may carry. Mirrors matter.js
// packages/types/src/protocol/definitions/interaction.ts
// DEFAULT_MAX_PATHS_PER_INVOKE — the same value the bridge advertises
// via BasicInformation.MaxPathsPerInvoke (0x0016,
// cluster/core/basic_information.go) and the CASE SessionParameters
// negotiation. Defined here (the protocol layer) rather than in
// cluster/core so [ValidateInvokeBatch] can enforce the exact number
// the bridge advertises without an import cycle.
const DefaultMaxPathsPerInvoke = 10

// CommandDataIB / CommandStatusIB tag numbers.
const (
	tagCmdDataPath   uint8 = 0
	tagCmdDataFields uint8 = 1
	tagCmdDataRef    uint8 = 2

	tagCmdStatusPath   uint8 = 0
	tagCmdStatusStatus uint8 = 1
	tagCmdStatusRef    uint8 = 2

	// InvokeResponseIB choice tags.
	tagInvokeRespCommandData   uint8 = 0
	tagInvokeRespCommandStatus uint8 = 1
)

// Errors.
var (
	// ErrInvalidInvokeRequest is returned for malformed invokes.
	ErrInvalidInvokeRequest = errors.New("im: invalid InvokeRequest")
)

// InvokeRequest is the in-memory form of an InvokeRequestMessage.
type InvokeRequest struct {
	SuppressResponse bool
	TimedRequest     bool
	Invokes          []CommandInvocation
}

// CommandInvocation is one entry in InvokeRequests.
type CommandInvocation struct {
	Path          ConcreteCommandPath
	Fields        any // cluster-native struct produced by [CommandFieldsReader]
	CommandRef    uint16
	HasCommandRef bool
}

// CommandFieldsReader extracts the cluster-native fields struct from
// the TLV stream. The IM layer does not own cluster-native decoding;
// the implementation switches on `path.Cluster` / `path.Command` and
// pulls elements from `dec` until the matching EndContainer arrives.
//
// `el` is the FIELDS container's opening element (TLV TypeStructure);
// `dec` is positioned just inside that container — calling
// `dec.Next()` returns the first field. Path is supplied so a single
// reader can dispatch across all cluster commands without an
// out-of-band cluster registry.
//
// Implementations that don't recognise the (cluster, command)
// pair MUST drain the container via the decoder's skip helper and
// return (nil, nil); the IM layer treats that as "fields skipped"
// and propagates path-only Invoke to the cluster server.
type CommandFieldsReader func(path ConcreteCommandPath, dec *tlv.Decoder, el tlv.Element) (any, error)

// UnmarshalInvokeRequestTLV decodes an InvokeRequestMessage.
func UnmarshalInvokeRequestTLV(dec *tlv.Decoder, fieldsReader CommandFieldsReader) (InvokeRequest, error) {
	open, err := dec.Next()
	if err != nil {
		return InvokeRequest{}, err
	}
	if !open.IsContainer || open.Type != tlv.TypeStructure {
		return InvokeRequest{}, fmt.Errorf("%w: expected struct, got 0x%02X", ErrInvalidInvokeRequest, open.Type)
	}
	var req InvokeRequest
	for {
		el, err := dec.Next()
		if err != nil {
			return InvokeRequest{}, fmt.Errorf("%w: %w", ErrInvalidInvokeRequest, err)
		}
		if el.IsEndContainer {
			return req, nil
		}
		if el.Tag.Kind != tlv.TagKindContext {
			continue
		}
		switch uint8(el.Tag.Number & 0xFF) {
		case tagInvokeReqSuppressResponse:
			req.SuppressResponse = el.Bool
		case tagInvokeReqTimedRequest:
			req.TimedRequest = el.Bool
		case tagInvokeReqInvokeRequests:
			if !el.IsContainer || el.Type != tlv.TypeArray {
				return InvokeRequest{}, fmt.Errorf("%w: InvokeRequests not array", ErrInvalidInvokeRequest)
			}
			invokes, err := readCommandData(dec, fieldsReader)
			if err != nil {
				return InvokeRequest{}, err
			}
			req.Invokes = invokes
		default:
			if el.IsContainer {
				if err := skipContainer(dec); err != nil {
					return InvokeRequest{}, err
				}
			}
		}
	}
}

func readCommandData(dec *tlv.Decoder, fieldsReader CommandFieldsReader) ([]CommandInvocation, error) {
	var out []CommandInvocation
	for {
		el, err := dec.Next()
		if err != nil {
			return nil, err
		}
		if el.IsEndContainer {
			return out, nil
		}
		if !el.IsContainer || el.Type != tlv.TypeStructure {
			return nil, fmt.Errorf("%w: CommandDataIB not struct", ErrInvalidInvokeRequest)
		}
		inv, err := readCommandInvocation(dec, fieldsReader)
		if err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
}

func readCommandInvocation(dec *tlv.Decoder, fieldsReader CommandFieldsReader) (CommandInvocation, error) {
	var inv CommandInvocation
	for {
		el, err := dec.Next()
		if err != nil {
			return CommandInvocation{}, err
		}
		if el.IsEndContainer {
			return inv, nil
		}
		if el.Tag.Kind != tlv.TagKindContext {
			continue
		}
		switch uint8(el.Tag.Number & 0xFF) {
		case tagCmdDataPath:
			if !el.IsContainer || el.Type != tlv.TypeList {
				return CommandInvocation{}, fmt.Errorf("%w: CommandPathIB not list", ErrInvalidInvokeRequest)
			}
			p, err := readCommandPathFields(dec)
			if err != nil {
				return CommandInvocation{}, err
			}
			inv.Path = p
		case tagCmdDataFields:
			if fieldsReader == nil {
				// No cluster-native reader wired — skip the container.
				// The dispatcher fan-out can still route on path alone
				// (ArmFailSafe / disarm flows, where the path uniquely
				// identifies the command and fields carry only optional
				// arguments). Without this branch the nil-reader call
				// below panics.
				if el.IsContainer {
					if err := skipContainer(dec); err != nil {
						return CommandInvocation{}, fmt.Errorf("%w: skip fields: %w", ErrInvalidInvokeRequest, err)
					}
				}
				continue
			}
			f, err := fieldsReader(inv.Path, dec, el)
			if err != nil {
				return CommandInvocation{}, fmt.Errorf("%w: fields: %w", ErrInvalidInvokeRequest, err)
			}
			inv.Fields = f
		case tagCmdDataRef:
			inv.CommandRef = uint16(el.Uint & 0xFFFF)
			inv.HasCommandRef = true
		}
	}
}

func readCommandPathFields(dec *tlv.Decoder) (ConcreteCommandPath, error) {
	var p ConcreteCommandPath
	for {
		el, err := dec.Next()
		if err != nil {
			return ConcreteCommandPath{}, err
		}
		if el.IsEndContainer {
			if !p.HasCluster || !p.HasCommand {
				return ConcreteCommandPath{}, fmt.Errorf("%w: missing cluster/command", ErrInvalidPath)
			}
			return p, nil
		}
		if el.Tag.Kind != tlv.TagKindContext {
			continue
		}
		switch uint8(el.Tag.Number & 0xFF) {
		case tagCmdPathEndpoint:
			p.Endpoint = uint16(el.Uint & 0xFFFF)
			p.HasEndpoint = true
		case tagCmdPathCluster:
			p.Cluster = uint32(el.Uint & 0xFFFFFFFF)
			p.HasCluster = true
		case tagCmdPathCommand:
			p.Command = uint32(el.Uint & 0xFFFFFFFF)
			p.HasCommand = true
		}
	}
}

// InvokeResponse is the in-memory form of an InvokeResponseMessage.
type InvokeResponse struct {
	SuppressResponse bool
	Responses        []InvokeResponseEntry
}

// InvokeResponseEntry is one element in InvokeResponses — either a
// CommandData (with response payload) or a CommandStatus.
type InvokeResponseEntry struct {
	Path          ConcreteCommandPath
	Response      any
	HasResponse   bool
	Status        StatusIB
	IsStatus      bool
	CommandRef    uint16
	HasCommandRef bool
}

// HasCommandData reports whether any entry is a CommandDataIB (a command
// that produced a response payload) rather than a CommandStatusIB. Per
// Matter §8.8.3.2.1 a SuppressResponse invoke still ships a response when a
// CommandDataIB is generated; when the response carries only status entries
// the server sends nothing. Mirrors matter.js
// packages/node/src/node/server/InteractionServer.ts:1043-1074
// (`suppressedBuffer` is flushed on the first `cmd-response`; otherwise the
// invoke returns without sending).
func (ir InvokeResponse) HasCommandData() bool {
	for i := range ir.Responses {
		if !ir.Responses[i].IsStatus {
			return true
		}
	}
	return false
}

// ValidateInvokeBatch enforces the batch-invoke path rules a controller may
// violate when it packs more than one CommandDataIB into a single
// InvokeRequest. Returns StatusInvalidAction on the first offending command,
// else StatusSuccess. A single-command invoke always passes. Mirrors matter.js
// packages/protocol/src/action/server/CommandInvokeResponse.ts:64-92,171-185
// (CommandInvokeResponse.process / #processConcrete):
//   - the batch must not exceed [DefaultMaxPathsPerInvoke] paths, checked
//     before any per-command validation or dispatch (matter.js
//     packages/node/src/node/server/InteractionServer.ts:950-955
//     `if (invokeRequests.length > this.#maxPathsPerInvoke) throw
//     new StatusResponseError(..., Status.InvalidAction)`);
//   - a wildcard-endpoint path is illegal in a batch (matter.js "Wildcard path
//     must not be used with multiple invokes");
//   - every concrete path in a batch MUST carry a CommandRef ("The CommandRef
//     field must be specified for all commands in a batch invoke");
//   - duplicate concrete (endpoint, cluster, command) paths are rejected
//     ("Duplicate concrete command path on batch invoke");
//   - duplicate CommandRefs are rejected ("Duplicate commandRef on batch
//     invoke").
//
// The wire decoder guarantees every command path carries a cluster and a
// command, so a "wildcard" here is a path without a concrete endpoint.
func ValidateInvokeBatch(req InvokeRequest) StatusCode {
	if len(req.Invokes) > DefaultMaxPathsPerInvoke {
		return StatusInvalidAction
	}
	multiple := len(req.Invokes) > 1
	seenPaths := make(map[[3]uint32]struct{}, len(req.Invokes))
	seenRefs := make(map[uint16]struct{}, len(req.Invokes))
	for i := range req.Invokes {
		inv := req.Invokes[i]
		if !inv.Path.HasEndpoint {
			// Wildcard-endpoint invoke: legal only as the sole command.
			if multiple {
				return StatusInvalidAction
			}
			continue
		}
		if multiple && !inv.HasCommandRef {
			return StatusInvalidAction
		}
		key := [3]uint32{uint32(inv.Path.Endpoint), inv.Path.Cluster, inv.Path.Command}
		if _, dup := seenPaths[key]; dup {
			return StatusInvalidAction
		}
		seenPaths[key] = struct{}{}
		if inv.HasCommandRef {
			if _, dup := seenRefs[inv.CommandRef]; dup {
				return StatusInvalidAction
			}
			seenRefs[inv.CommandRef] = struct{}{}
		}
	}
	return StatusSuccess
}

// CommandFieldsWriter is the encoder counterpart of [CommandFieldsReader].
type CommandFieldsWriter func(enc *tlv.Encoder, tag tlv.Tag, fields any)

// MarshalTLV encodes ir. fieldsWriter is invoked for each non-status
// response that carries a payload.
func (ir InvokeResponse) MarshalTLV(enc *tlv.Encoder, fieldsWriter CommandFieldsWriter) {
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutBool(tlv.ContextTag(tagInvokeRespSuppressResponse), ir.SuppressResponse)
	enc.StartArray(tlv.ContextTag(tagInvokeRespResponses))
	for _, ent := range ir.Responses {
		ent.marshal(enc, fieldsWriter)
	}
	_ = enc.EndContainer()
	enc.PutUint(tlv.ContextTag(tagInteractionModelRevision), uint64(MatterInteractionModelRevision))
	_ = enc.EndContainer()
}

func (ent InvokeResponseEntry) marshal(enc *tlv.Encoder, fieldsWriter CommandFieldsWriter) {
	enc.StartStruct(tlv.AnonymousTag())
	if ent.IsStatus {
		enc.StartStruct(tlv.ContextTag(tagInvokeRespCommandStatus))
		ent.Path.MarshalTLV(enc, tlv.ContextTag(tagCmdStatusPath))
		ent.Status.MarshalTLV(enc, tlv.ContextTag(tagCmdStatusStatus))
		if ent.HasCommandRef {
			enc.PutUint(tlv.ContextTag(tagCmdStatusRef), uint64(ent.CommandRef))
		}
		_ = enc.EndContainer()
	} else {
		enc.StartStruct(tlv.ContextTag(tagInvokeRespCommandData))
		ent.Path.MarshalTLV(enc, tlv.ContextTag(tagCmdDataPath))
		if ent.HasResponse {
			fieldsWriter(enc, tlv.ContextTag(tagCmdDataFields), ent.Response)
		}
		if ent.HasCommandRef {
			enc.PutUint(tlv.ContextTag(tagCmdDataRef), uint64(ent.CommandRef))
		}
		_ = enc.EndContainer()
	}
	_ = enc.EndContainer()
}

// HandleInvokeRequest dispatches a parsed InvokeRequest through d and
// returns the assembled InvokeResponse.
//
// ACL gate: mirrors matter.js
// packages/node/src/node/server/OnlineServerInteraction.ts
// FabricAccessControl.forRequest and chip src/app/CommandHandler.cpp
// "Execute the ACL Access Granting Algorithm before existence checks".
// When d implements [ACLChecker], the requesting fabric's privilege is
// verified before each command path is dispatched. The required
// privilege for a plain Invoke is Operate (3) per Matter §9.10.4.4;
// individual cluster servers may enforce additional Manage/Admin gates
// internally for sensitive commands such as RemoveFabric. fabricIndex
// is extracted via [FabricFilterFromContext]; fabricIndex==0 (PASE)
// bypasses the ACL check so commissioning invokes (ArmFailSafe,
// CommissioningComplete) arrive before the fabric's ACL entry exists.
func HandleInvokeRequest(ctx context.Context, d Dispatcher, req InvokeRequest) InvokeResponse {
	_, fabricIndex := FabricFilterFromContext(ctx)
	subjectNodeID, subjectCATs := SubjectFromContext(ctx)
	aclChecker, hasACL := d.(ACLChecker)
	privProvider, hasPrivProvider := d.(CommandInvokePrivilegeProvider)

	// invokePrivilege returns the minimum privilege needed to invoke the
	// given command. Falls back to Operate (3) — the Matter §9.10.4.4
	// default invoke privilege — when no CommandInvokePrivilegeProvider
	// is wired or the command has no elevated requirement.
	invokePrivilege := func(inv CommandInvocation) uint8 {
		const privilegeOperate uint8 = 3
		if hasPrivProvider {
			return privProvider.MinInvokePrivilege(inv.Path.Endpoint, inv.Path.Cluster, inv.Path.Command)
		}
		return privilegeOperate
	}

	var ir InvokeResponse
	// The InvokeResponseMessage.SuppressResponse field is deprecated and matter.js
	// always emits it as false (packages/node/src/node/server/InteractionServer.ts:987-988
	// `emptyInvokeResponse.suppressResponse: false`). The controller's suppress-response
	// intent governs whether the bridge sends the InvokeResponse at all (handled at the
	// dispatch layer via [InvokeResponse.HasCommandData] per Matter §8.8.3.2.1), not the
	// value of this wire field — so it stays at its zero value here.
	for _, inv := range req.Invokes {
		// ACL gate. PASE (fabricIndex==0) skips ACL: commissioning
		// invokes arrive before the fabric's ACL entry exists. The
		// required privilege is per-command (RemoveFabric,
		// OpenCommissioningWindow, … → Administer) rather than a flat
		// Operate, so an Operate-only subject cannot invoke an
		// administrative command.
		if hasACL && fabricIndex != 0 {
			if status := aclChecker.CheckACL(ctx, fabricIndex, subjectNodeID, subjectCATs, inv.Path.Endpoint, inv.Path.Cluster, invokePrivilege(inv)); !status.IsSuccess() {
				ir.Responses = append(ir.Responses, InvokeResponseEntry{
					Path:          ConcreteCommandPath{Endpoint: inv.Path.Endpoint, Cluster: inv.Path.Cluster, Command: inv.Path.Command, HasEndpoint: true, HasCluster: true, HasCommand: true},
					CommandRef:    inv.CommandRef,
					HasCommandRef: inv.HasCommandRef,
					IsStatus:      true,
					Status:        StatusIB{Status: status},
				})
				continue
			}
		}
		res := d.Invoke(ctx, inv.Path, inv.Fields)
		ent := InvokeResponseEntry{
			Path:          res.Path,
			CommandRef:    inv.CommandRef,
			HasCommandRef: inv.HasCommandRef,
		}
		if res.Status != StatusSuccess {
			ent.IsStatus = true
			ent.Status = StatusIB{Status: res.Status, ClusterStatus: res.ClusterStatus, HasClusterStatus: res.HasClusterStatus}
		} else if res.Response != nil {
			ent.Response = res.Response
			ent.HasResponse = true
		} else {
			// Status-only success path: explicit StatusIB(Success).
			ent.IsStatus = true
			ent.Status = StatusIB{Status: StatusSuccess}
		}
		ir.Responses = append(ir.Responses, ent)
	}
	return ir
}
