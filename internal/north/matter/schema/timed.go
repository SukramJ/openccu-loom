// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package schema

// Timed-interaction conformance (Matter §8.7). A command whose matter.js model
// element carries the "T" access quality (Access.Timed.Required — see
// ../matter.js/packages/model/src/aspects/Access.ts:38) MUST be invoked inside
// a Timed interaction. matter.js enforces this server-side in
// ../matter.js/packages/protocol/src/action/server/CommandInvokeResponse.ts:266-268:
//
//	if (limits.timed && !this.session.timed) return Status.NeedsTimedInteraction;
//
// i.e. a timed-required command received outside a valid timed window is
// rejected with NEEDS_TIMED_INTERACTION regardless of the InvokeRequest's own
// Timed flag.
//
// Unlike ClusterRevisions this table is hand-authored: the generated schema
// snapshot does not yet carry per-command conformance. It is pinned against
// matter.js by TestTimedInvokeParity so it cannot silently drift, and it lists
// only clusters the bridge exposes — across that root/utility cluster set
// AdministratorCommissioning is the only one with "T"-access commands. Extend
// it (and the parity test) whenever a newly exposed cluster ships a timed
// command.
var timedInvokePaths = map[uint32]map[uint32]struct{}{
	// AdministratorCommissioning (0x003C) — every command is "A T".
	// ../matter.js/packages/model/src/standard/elements/administrator-commissioning.element.ts:31,43,49
	0x003C: {
		0x0: {}, // OpenCommissioningWindow
		0x1: {}, // OpenBasicCommissioningWindow
		0x2: {}, // RevokeCommissioning
	},
}

// IsTimedInvoke reports whether the (cluster, command) pair is timed-required
// per the matter.js "T" access quality — it may only be invoked inside a Timed
// interaction. Mirrors matter.js Access.Timed.Required
// (CommandInvokeResponse.ts:266 `limits.timed`).
func IsTimedInvoke(clusterID, commandID uint32) bool {
	cmds, ok := timedInvokePaths[clusterID]
	if !ok {
		return false
	}
	_, ok = cmds[commandID]
	return ok
}
