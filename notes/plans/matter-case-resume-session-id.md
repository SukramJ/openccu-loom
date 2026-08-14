# Implementation plan — CASE resume and the session-id it reuses

**Status:** partially executed. Only the **observability** half shipped in
0.59.1 — the resume path itself is untouched and the behaviour question is
still **open and still blocked on hardware, not on effort**. What landed: one
debug record per resume, an occupancy block on `GET /api/v1/matter/sessions`
and in the Matter diagnostics view, and a resume row in the live-controller
brief. What is still open: whether a resumed session must carry a **new**
session id, the retransmit-idempotence guard that either answer needs, and the
`by_design.md` entry recording the divergence. One thing the observability work
did settle: matter.js takes a fresh id where we keep the announced one, so the
divergence is now **known** rather than suspected. See
[What shipped](#what-shipped).
**Audience:** a fresh agent with no access to the review conversation.
Read [`CLAUDE.md`](../../CLAUDE.md) §"matter.js as the Matter Gold Standard"
first; the hard rule there governs every step below.

## Why this exists

0.59.1 fixed three real CASE defects in `internal/north/matter/secure/`:

- the responder reset for a fresh Sigma1 did not clear the peer's CASE
  Authenticated Tags, so a second controller inherited the first one's
  administrator-group tag (privilege escalation);
- session-id placeholders from aborted handshakes were never reclaimed,
  exhausting the 16-bit id space;
- `OpenFromSigmaWithID` overwrote a live entry for a different peer without
  closing it, serving the first peer's subscriptions under the second's keys.

While fixing the first of those, the agent noticed a fourth thing on the
**resume fast path** (`Responder.tryResume`,
`internal/north/matter/secure/sigma/protocol.go`) and deliberately left it:

> The session id is not renewed on the resume fast path. `CaseAdapter.ProcessSigma1`
> only fires `onEstablished` on the FIRST establishment per adapter, so a resume on
> an adapter that already completed a full handshake never opens a second session;
> and where it could, the new `installEntryLocked` guard now tears the displaced
> session down properly. Renewing the id there would burn ids on MRP retransmits of
> a resume Sigma1 and cannot be validated without live Apple hardware.

## The open question

Matter's CASE resumption (Sigma1-Resume / Sigma2-Resume) establishes a new
**session** from a cached shared secret. The question this plan exists to
settle is whether that new session must carry a **new session id**, and what
our implementation does today when a controller resumes repeatedly.

Two failure shapes are possible and they pull in opposite directions:

1. **Reusing the id** across a resume means the peer's old session state
   (message counters, subscriptions) may be conflated with the new one. If the
   counters do not reset consistently on both sides, messages are rejected as
   replays and the controller appears to hang.
2. **Renewing the id** on every resume Sigma1 burns one id per **retransmit**.
   MRP retransmits a Sigma1-Resume when the response is lost, and each copy
   would allocate. With a 16-bit space and no reclaim on the abort path, a
   lossy network would exhaust it — the exact defect 0.59.1 just fixed on the
   full path.

Neither can be settled from the spec text alone, because the interoperability
behaviour is what matters and that lives in the certified stacks.

## What to do

1. **Read matter.js first.** `packages/protocol/src/session/case/` —
   specifically how `CaseServer` handles a resume, whether `SessionManager`
   allocates a fresh id for the resumed session, and what it does with the
   previous session bound to the same peer. matter.js is the gold standard for
   this and its behaviour has been through real interop testing. Cite the file
   and function in the Go comment.
2. **Read chip's behaviour** where matter.js is ambiguous
   (`CASESession::HandleSigma1Resume` and its session-manager interaction).
3. **Only then decide**, and write the decision into
   [`notes/parity/by_design.md`](../parity/by_design.md) if it diverges from
   matter.js in any way.
4. **Guard the retransmit case explicitly.** Whatever the decision, a repeated
   Sigma1-Resume carrying the same `initiatorRandom` and resumption id is a
   retransmit, not a new resume, and must be idempotent. If ids are renewed,
   this is the guard that keeps the space from draining.

Step 1 is now partly done — see below — and steps 2 to 4 are untouched.

## Why it is blocked

The behaviour that matters is what a real controller does across a network
partition, an iOS Home Hub failover, and a bridge restart. The in-tree
`godevccu` simulator does not commission over Matter, and the chip-tool
capability suite runs a controller but **cannot exercise resumption on
demand** — resumption happens when the controller decides to resume, which is a
function of its own cache lifetime.

So the honest validation path is:
- a live Apple Home (or Google Home) fabric,
- a bridge restart or network partition that provokes a resume rather than a
  full handshake,
- and the session table observed across it.

[`notes/contributor/chip-tool-test-brief.md`](../contributor/chip-tool-test-brief.md)
is the existing procedure for live-controller work; extend it with a resume
scenario rather than inventing a parallel one. **Do not ship a change to the
resume path validated only by unit tests** — the failure modes above are both
interop failures, and both look fine in isolation.

## If the hardware is unavailable

There is a cheap intermediate step worth taking on its own: make the current
behaviour **observable**. Log (at debug) each resume with the resumption id,
the session id before and after, and whether an existing session for that peer
was displaced; and surface the session table's occupancy on the existing Matter
diagnostics surface. That turns "we do not know what happens on resume" into
something an operator report can answer, and it is safe to ship without
hardware.

## What shipped

Exactly that intermediate step, and nothing beyond it. What the resume path
*does* is unchanged — the only lines added to it stamp the record described
below, and a full handshake clears it again.

- `Responder.ResumeInfo` records what the fast path did — the resumption id
  the initiator presented, the id handed back for the initiator's next resume,
  and the session id before and after — stamped before the fast path returns so
  the session-open callback can read it
  (`TestSigma_Resume_ResumeInfoRecordsWhatTheFastPathDid`,
  `TestSigma_Resume_ResumeInfoClearedByAFullHandshake`).
- The daemon emits one `matter.bridge.case.session_resumed` debug record per
  resume, including the sessions the install displaced, and builds nothing when
  debug logging is off.
- `GET /api/v1/matter/sessions` carries an `occupancy` block sourced from the
  id allocator — live, reserved, capacity, free — and the Matter diagnostics
  view renders the same line above the controller table. A reserved id appears
  in no session row and holds its slot for twenty minutes, so a draining space
  used to look exactly like a quiet bridge
  (`TestOccupancy_SeparatesLiveSessionsFromReservedIDs`).
- The live-controller brief gained a resume row: observed rather than driven,
  with the debug record and the occupancy block as the two artefacts a capture
  must bring back.

Step 1's matter.js read produced one hard fact worth carrying forward:
`packages/protocol/src/session/case/CaseServer.ts` `#resume` calls
`getNextAvailableSessionId`, so **matter.js allocates a fresh id for a resumed
session where we keep the one already announced**. That makes this a known
divergence from the gold standard rather than an unexamined one — which is why
step 3's `by_design.md` entry is now owed either way: if the capture says keep
the id, the divergence is deliberate and must be recorded; if it says renew,
the divergence is a defect and the entry is moot.

## What did not, and why

- **The behaviour is unchanged**, deliberately. Both candidate answers are
  interop failures that look correct in a unit test, and neither can be chosen
  from the spec text. Shipping the observability first is what makes the
  eventual capture readable.
- **No retransmit-idempotence guard yet.** It is only worth writing against the
  decided behaviour: under "keep the id" a retransmit is already idempotent by
  construction, under "renew" it is the guard that stops the id space draining.
  Writing it now would pin the current behaviour and make the change harder.
- **No `by_design.md` entry yet**, for the reason above — the entry states a
  decision, and the decision is what is blocked.
