// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package im

import "context"

// fabricFilterContext carries the FabricFiltered flag + the operational
// FabricIndex through the IM dispatcher to fabric-scoped cluster
// servers. Mirrors matter.js's OnlineContext.{forFabricFilteredRead,
// fabricIndex} (packages/protocol/src/interaction/InteractionServer.ts:
// startReadInteraction). openccu-loom threads via context.Context to
// avoid breaking the Dispatcher.Read signature.
type fabricFilterContext struct {
	filtered    bool
	fabricIndex uint8
}

type fabricFilterCtxKey struct{}

// WithFabricFilter returns a derived context carrying the FabricFiltered
// flag from the request and the operational FabricIndex resolved from
// the inbound session. Cluster servers that expose fabric-scoped
// attributes (OperationalCredentials.Fabrics, AccessControl.ACL) read
// this via [FabricFilterFromContext] inside their MatterReadFiltered and
// project the underlying list down to the requesting fabric when
// filtered=true. fabricIndex==0 means "pre-fabric / PASE session" —
// matter.js treats reads from such sessions as if FabricFiltered=false.
func WithFabricFilter(ctx context.Context, filtered bool, fabricIndex uint8) context.Context {
	return context.WithValue(ctx, fabricFilterCtxKey{}, fabricFilterContext{filtered: filtered, fabricIndex: fabricIndex})
}

// FabricFilterFromContext extracts the FabricFiltered flag + FabricIndex
// stamped by [WithFabricFilter]. Returns (false, 0) when no filter
// context is present — the safe default that yields the unfiltered
// list (Matter §7.5.2).
func FabricFilterFromContext(ctx context.Context) (filtered bool, fabricIndex uint8) {
	v, ok := ctx.Value(fabricFilterCtxKey{}).(fabricFilterContext)
	if !ok {
		return false, 0
	}
	return v.filtered, v.fabricIndex
}

// subjectContext carries the requesting peer's identity for ACL
// subject-matching (Matter §9.10.5.6). For CASE sessions: NodeID is
// the operational subject from the peer's NOC, CATs is the set of
// CASE Authenticated Tags lifted from the same NOC subject. A request
// with no subject context (NodeID==0) is treated as anonymous —
// matches only ACL entries whose Subjects list is empty (the
// fabric-wide wildcard).
type subjectContext struct {
	nodeID uint64
	cats   []uint32
}

type subjectCtxKey struct{}

// WithSubject stamps the operational peer subject — NodeID lifted out
// of the peer NOC plus the set of CASE Authenticated Tags from the
// same NOC — into ctx so [TopologyDispatcher.CheckACL] can enforce
// per-subject ACEs (Matter §9.10.5.6). PASE sessions stamp NodeID=0
// and nil CATs; the ACL gate bypasses subject matching on PASE via
// the fabricIndex==0 check.
//
// Mirrors connectedhomeip/src/access/AccessControl.cpp:441 — the
// `subjectDescriptor` the access check uses carries (fabricIndex,
// authMode, subject, cats). openccu-loom carries the same triple via
// ctx so the IM dispatcher's signature stays minimal.
func WithSubject(ctx context.Context, nodeID uint64, cats []uint32) context.Context {
	var copyCATs []uint32
	if len(cats) > 0 {
		copyCATs = append(copyCATs, cats...)
	}
	return context.WithValue(ctx, subjectCtxKey{}, subjectContext{nodeID: nodeID, cats: copyCATs})
}

// SubjectFromContext extracts the peer subject (NodeID + CATs)
// stamped by [WithSubject]. Returns (0, nil) when no subject is
// present — the safe default that limits the request to entries
// whose Subjects list is empty (i.e. "any subject on this fabric").
func SubjectFromContext(ctx context.Context) (nodeID uint64, cats []uint32) {
	v, ok := ctx.Value(subjectCtxKey{}).(subjectContext)
	if !ok {
		return 0, nil
	}
	return v.nodeID, v.cats
}
