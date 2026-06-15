# ADR 0033 — Minimal in-memory Groups cluster: rejected (stub is a deliberate, matter.js-conformant divergence)

- **Status**: rejected
- **Date**: 2026-06-15
- **Related**:
  `docs/parity/by_design.md` (BD-Matter-P2-D19, L00-BD-Groups),
  `internal/north/matter/cluster/wire/groups.go`,
  the analysis item Area 7 [W3]/[P2] in
  `docs/audit/architecture-analysis-2026-06-15.md`,
  [matter.js as the Matter gold standard](../../CLAUDE.md)

## Context

The architecture analysis (Area 7) proposed ([P2, L]): *"Add a minimal
in-memory Groups implementation
(`AddGroup`/`RemoveGroup`/`ViewGroup`/`GetGroupMembership`) … to satisfy
conformance without persistence."* It flagged ([W3]) that Groups (0x0004)
and ScenesManagement (0x0062) are *"permanent stubs returning
`UnsupportedCommand` for mandatory commands."*

That proposal conflicts with an existing, deliberate design decision.

## Why the stub stays

**The stub is already a documented by-design divergence
(BD-Matter-P2-D19).** Groups is mounted as a mandatory stub on every
OnOff device type. It advertises `NameSupport=0x00` and rejects
`AddGroup` / `RemoveGroup` / `AddGroupIfIdentifying`. The rationale is
recorded in `by_design.md` and stands:

1. **It is matter.js-conformant, not a gap.** `NameSupport=0` is exactly
   the surface matter.js's default `GroupsServer` /  `GroupsBehavior`
   exposes when no membership provider is wired
   (`packages/node/src/behaviors/groups/GroupsServer.ts`, cited in
   `by_design.md` L00-BD-Groups). Apple Home, Google Home, and chip-tool
   all tolerate it — the stub satisfies the device-type conformance
   requirement without exposing broken functionality.

2. **There is no HomeMatic primitive to map a group table onto.**
   HomeMatic has no group concept. A populated Groups cluster would
   advertise group membership that nothing on the CCU side can act on —
   a Matter-only fiction with no south-bound effect. The bridge's
   contract is to project real CCU devices, not to invent a group model.

3. **A real implementation is a much larger change than "in-memory."**
   Matter Groups is not self-contained: a working group table must
   coordinate with the **GroupKeyManagement** cluster (group key sets,
   the multicast session derivation) and honour fabric-scoping of the
   membership list. "Minimal in-memory `AddGroup`/`RemoveGroup`" that
   skips GroupKeyManagement would advertise membership the bridge cannot
   actually receive group-cast traffic for — worse than the honest stub.

**This environment also cannot satisfy the gold-standard workflow.**
Implementing real Groups behaviour means mirroring matter.js
`GroupsServer.ts` + `GroupKeyManagementServer.ts` verbatim (command IDs,
status codes, constraints, fabric-index handling) per CLAUDE.md's hard
rule against hand-coding cluster semantics from memory. matter.js is not
checked out here, so a correct mirror cannot be produced now even if the
feature were wanted.

## Decision

Do **not** implement a populated Groups (or ScenesManagement) cluster.
Keep the `NameSupport=0` stub. The decision is unchanged from
BD-Matter-P2-D19; this ADR elevates it to ADR status and closes the
Area 7 [P2] item.

The same reasoning applies to ScenesManagement (BD-Matter-P2-D18,
`SceneTableSize=0` stub).

## Alternatives considered

- **In-memory group table without GroupKeyManagement.** Rejected —
  advertises membership the bridge cannot receive multicast for;
  conformance-worse than the honest stub.
- **Full Groups + GroupKeyManagement implementation.** Out of scope and
  unbacked: no HM-side group primitive, and it requires a matter.js read
  not available in this environment. If a future release wants it (e.g.
  to expose Matter-native grouping independent of the CCU), it is its own
  feature ADR with the matter.js mirror + a group-membership store + the
  GroupKeyManagement coupling.

## Consequences

- The Area 7 [P2] Groups item is closed as "rejected — deliberate
  stub", consistent with BD-Matter-P2-D19 / BD-Matter-P2-D18.
- No code change; the existing stub and its parity catalogue entry
  remain the source of truth.
