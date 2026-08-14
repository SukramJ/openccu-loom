// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package im

import "context"

// AttributeValue carries the result of a Read or the input of a Write.
// The bridge speaks cluster-native Go types ([bool], [uint8], [int16],
// …) and the IM layer is responsible only for routing — the TLV
// encoder bound to the cluster-server side does the wire-shape work.
type AttributeValue struct {
	// Value is the cluster-native Go value. Nil for unobserved /
	// nullable attributes.
	Value any
	// IsNull is true when the attribute value is explicitly null on
	// the wire (Matter NULL TLV element). Mutually exclusive with a
	// non-nil Value.
	IsNull bool
}

// DataVersionReader is an optional interface a [Dispatcher] implementation
// may provide to support DataVersion-conditional writes. When the dispatcher
// implements this interface, [HandleWriteRequest] calls CurrentDataVersion
// before applying each write that carries a DataVersion field.
//
// Mirrors matter.js packages/node/src/node/server/InteractionServer.ts
// DataVersion mismatch check before cluster write dispatch, and chip
// src/app/WriteHandler.cpp AttributeAccessInterface DataVersion verification.
type DataVersionReader interface {
	// CurrentDataVersion returns the current DataVersion for the cluster at
	// (endpoint, clusterID). Returns (0, false) when the cluster is not
	// tracked or does not implement version tracking.
	CurrentDataVersion(ctx context.Context, endpoint uint16, clusterID uint32) (uint32, bool)
}

// ACLChecker is an optional interface a [Dispatcher] implementation may
// provide to gate Write and Invoke calls against the AccessControl cluster's
// ACL table. When nil / not implemented, all writes and invokes are allowed
// (permissive default — preserves v1.0 behaviour for callers that have not
// wired an ACL store).
//
// Mirrors connectedhomeip/src/access/AccessControl.cpp:415-560 and matter.js
// packages/node/src/node/server/OnlineServerInteraction.ts FabricAccessControl
// forRequest gate. chip src/app/WriteHandler.cpp:780 enforces "Execute the
// ACL Access Granting Algorithm before existence checks" — the IM gate calls
// this BEFORE attribute dispatch.
type ACLChecker interface {
	// CheckACL returns [StatusUnsupportedAccess] when the requesting fabric
	// does not hold the required privilege for (endpoint, clusterID) on
	// behalf of the requesting subject. Returns [StatusSuccess] when access
	// is granted.
	//
	// fabricIndex is the requesting fabric index extracted from the session
	// context (via [FabricFilterFromContext]). fabricIndex==0 means PASE /
	// pre-commissioning — ACL does not yet apply (always granted).
	//
	// subjectNodeID + subjectCATs identify the requesting peer for per-
	// subject ACE matching (Matter §9.10.5.6). For CASE sessions both are
	// lifted from the peer NOC; for PASE both are zero / nil and the
	// fabricIndex==0 bypass applies. An ACL entry with an empty Subjects
	// list matches any subject on the fabric.
	//
	// requiredPrivilege is the minimum Matter privilege level required:
	//   1=View, 3=Operate, 4=Manage, 5=Administer.
	CheckACL(ctx context.Context, fabricIndex uint8, subjectNodeID uint64, subjectCATs []uint32, endpoint uint16, clusterID uint32, requiredPrivilege uint8) StatusCode
}

// AttributeReadPrivilegeProvider is an optional interface a [Dispatcher]
// may implement to signal that a specific (endpoint, cluster, attribute)
// path requires a higher read privilege than the default View (1).
// [HandleReadRequest] calls this before dispatching each concrete ACL
// check so elevated-privilege attributes (e.g. AccessControl.ACL and
// AccessControl.Extension which require Administer per Matter §9.10.5.3)
// are protected even on read paths. Mirrors chip
// src/app/clusters/access-control-server/access-control-server.cpp
// AttributeReadAclRequired and matter.js
// packages/model/src/standard/elements/access-control.element.ts
// access: "administer".
type AttributeReadPrivilegeProvider interface {
	// MinReadPrivilege returns the minimum Matter privilege level
	// required to read attrID on (endpoint, clusterID). Return 1
	// (View) for the common case; return 5 (Administer) for
	// fabric-security-sensitive attributes like ACL/Extension.
	MinReadPrivilege(endpoint uint16, clusterID, attrID uint32) uint8
}

// AttributeWritePrivilegeProvider is an optional interface a [Dispatcher]
// may implement to signal that a specific (endpoint, cluster, attribute)
// path requires a higher write privilege than the default Operate (3).
// [HandleWriteRequest] calls this before the per-write ACL check so
// elevated-privilege attributes (e.g. AccessControl.ACL / Extension which
// require Administer, or BasicInformation.NodeLabel which requires Manage)
// cannot be written by a merely-Operate subject. Mirrors the writeAccess
// bits in matter.js packages/model/src/standard/elements/*.element.ts and
// chip's per-attribute write-ACL guards.
type AttributeWritePrivilegeProvider interface {
	// MinWritePrivilege returns the minimum Matter privilege level
	// required to write attrID on (endpoint, clusterID). Return 3
	// (Operate) for the common case; return 4 (Manage) / 5 (Administer)
	// for elevated attributes.
	MinWritePrivilege(endpoint uint16, clusterID, attrID uint32) uint8
}

// WriteAuthorizer decides whether the requesting subject may write the
// RESOLVED (endpoint, cluster, attribute). It returns [StatusSuccess]
// when access is granted and a denial status (typically
// [StatusUnsupportedAccess]) otherwise.
//
// A wildcard-endpoint write carries no endpoint on the requested path,
// so the only place its privilege can be evaluated is where the path
// resolves. Authorizing the un-expanded request instead would check a
// zero-value endpoint and then apply the value to every endpoint the
// wildcard reaches.
type WriteAuthorizer func(endpoint uint16, clusterID, attrID uint32) StatusCode

// AuthorizingWriter is an optional interface a [Dispatcher] may implement
// so [HandleWriteRequest] can gate every location a wildcard-endpoint
// write expands to BEFORE the value is applied. Post-hoc filtering of the
// returned results is not sufficient: a write mutates cluster state (and
// drives device commands) inside the expansion loop.
//
// Mirrors matter.js
// packages/protocol/src/action/server/AttributeWriteResponse.ts:324-343
// (#writeAttributeForWildcard authorizes the resolved attribute at its own
// location and returns without emitting a status on denial).
type AuthorizingWriter interface {
	// WriteAuthorized behaves like [Dispatcher.Write] but consults
	// authorize for every resolved (endpoint, cluster, attribute) before
	// the value is applied. A denied location on a wildcard-endpoint path
	// is skipped silently (Matter §8.4.3.2 — a wildcard interaction
	// discloses only authorized paths); a denied concrete path yields the
	// denial status. A nil authorize dispatches exactly like Write.
	WriteAuthorized(ctx context.Context, path ConcreteAttributePath, value AttributeValue, authorize WriteAuthorizer) []WriteResult
}

// CommandInvokePrivilegeProvider is an optional interface a [Dispatcher]
// may implement to signal that a specific (endpoint, cluster, command)
// requires a higher invoke privilege than the default Operate (3).
// [HandleInvokeRequest] calls this before the per-invoke ACL check so
// administrative commands (e.g. OperationalCredentials.RemoveFabric,
// AdministratorCommissioning.OpenCommissioningWindow) cannot be invoked
// by a merely-Operate subject. Mirrors the invokeAccess bits in matter.js
// packages/model/src/standard/elements/*.element.ts and chip's
// per-command invoke-ACL guards.
type CommandInvokePrivilegeProvider interface {
	// MinInvokePrivilege returns the minimum Matter privilege level
	// required to invoke cmdID on (endpoint, clusterID). Return 3
	// (Operate) for the common case; return 4 (Manage) / 5 (Administer)
	// for elevated commands.
	MinInvokePrivilege(endpoint uint16, clusterID, cmdID uint32) uint8
}

// Dispatcher is the cluster-server-side surface the IM layer routes
// Read / Write / Invoke requests through. The endpoint assembler in
// [..]/north/matter/endpoint constructs and registers a Dispatcher
// that walks the model registry to satisfy each call.
//
// Implementations:
//
//   - MUST return [StatusUnsupportedEndpoint] for endpoints the bridge
//     does not advertise.
//   - MUST return [StatusUnsupportedCluster] when the endpoint exists
//     but the requested cluster is absent.
//   - MUST return [StatusUnsupportedAttribute] / [StatusUnsupportedCommand]
//     when the cluster exists but the requested ID is missing.
//   - SHOULD return [StatusBusy] when a transient backend condition
//     prevents servicing the request.
//
// Wildcards: when a path field has its Has* flag false, the
// implementation expands across every concrete value matching. The
// returned Reads slice carries one (path, value, status) per concrete
// match.
type Dispatcher interface {
	// Read resolves attribute reads. The path may carry wildcards;
	// the implementation expands them to one ReadResult per match.
	Read(ctx context.Context, path ConcreteAttributePath) []ReadResult

	// Write resolves a single attribute write. Unlike Read, write
	// paths are typically concrete; a wildcard write fans out to
	// multiple WriteResults (one per matching attribute).
	Write(ctx context.Context, path ConcreteAttributePath, value AttributeValue) []WriteResult

	// Invoke dispatches a single command. fields is the decoded
	// cluster-native struct for the command's request payload (or
	// nil for parameterless commands). The response value is the
	// cluster-native struct for the command's response (or nil for
	// status-only commands).
	Invoke(ctx context.Context, path ConcreteCommandPath, fields any) InvokeResult
}

// ReadResult is one (path, value, status) tuple produced by a Read.
// Wildcard reads fan out into multiple ReadResults — one per
// (endpoint, cluster, attribute) match.
type ReadResult struct {
	Path   ConcreteAttributePath
	Value  AttributeValue
	Status StatusCode
	// DataVersion carries the per-cluster DataVersion that the cluster
	// server reported at read time. 0 means "not provided" and the IM
	// layer defaults to 1 in the wire encoding (Matter §10.6.1.4).
	DataVersion uint32
}

// WriteResult is one (path, status) pair produced by a Write.
type WriteResult struct {
	Path   ConcreteAttributePath
	Status StatusCode
	// ClusterStatus carries the cluster-specific status byte from
	// Matter §10.6.2.2 StatusIB. Surfaced when a cluster server
	// returns an error that implements [MatterClusterStatusError]
	// (e.g. AdministratorCommissioning §11.19.7.3 PAKEParameterError
	// = 0x02). Zero + HasClusterStatus=false means "no cluster-specific
	// status" — wire encoding omits the ClusterStatus tag.
	ClusterStatus    uint8
	HasClusterStatus bool
}

// InvokeResult is the outcome of a single command dispatch.
type InvokeResult struct {
	Path     ConcreteCommandPath
	Response any // cluster-native response struct or nil for status-only
	Status   StatusCode
	// ClusterStatus carries the cluster-specific status byte per
	// Matter §10.6.2.2 — see [WriteResult.ClusterStatus] for the
	// envelope semantics.
	ClusterStatus    uint8
	HasClusterStatus bool
}
