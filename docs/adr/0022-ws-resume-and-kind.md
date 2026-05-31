# ADR 0022 — WebSocket resume cursor + envelope `kind` discriminator

- **Status**: accepted
- **Date**: 2026-05-24
- **Related**:
  [ADR 0020 — external-client wire contract](./0020-external-client-wire-contract.md),
  [`docs/external-clients/asks.md`](../external-clients/asks.md) (closes B1, B2),
  [`docs/external-clients/topic-hierarchy.md`](../external-clients/topic-hierarchy.md),
  `internal/north/rest/ws/hub.go`,
  `internal/north/rest/ws/client.go`

## Decision

The daemon's WebSocket envelope grows two new fields:

1. **`seq` (uint64)** — a monotonic cursor assigned at publish time
   inside the hub. Strictly increasing across the daemon's lifetime
   (resets to 0 on daemon restart). Clients store the last received
   `seq` and pass it back as `since` on the next subscribe op to
   replay the events they missed across a reconnect.
2. **`kind` (enum)** — `initial | change | refresh`, discriminating
   the event family. `change` is the default and dominant case (a
   delta on a watched data point); `initial` marks the first
   observation of a value (e.g. during cold-start replay);
   `refresh` is reserved for periodic re-emits. Producers that do
   not distinguish the cases emit `change`; clients MUST treat
   unknown `kind` values as `change` for forward-compatibility.

The hub maintains a bounded ring buffer of recent events (default
**1024**) under a dedicated mutex. The `subscribe` op accepts an
optional `since: N` field that triggers replay of buffered events
with `Seq > N` matching the supplied topic patterns. The replay
completes with one of two control frames:

- `{op: "replay_done", seq: N}` — replay succeeded; the carried seq
  is the last-replayed value (or the original `since` if nothing
  matched). The client now has a coherent state.
- `{op: "replay_lost", oldest_seq: M}` — `since` precedes the oldest
  buffered event. The client must take a fresh `GET /snapshot` to
  resync; relying on the stream alone would silently lose state.

## Context

`docs/external-clients/asks.md` listed B1 ("Sequence/Replay-Semantik
nach WS-Reconnect") in the TL;DR Top-3 — the existing WS surface
had no way for a client to know whether it had missed events across
a reconnect, NAT-timeout, or network blip. The only recovery path was
a full `/snapshot` re-load, which:

- Cost bandwidth on every reconnect (worst case: 80k DPs × ~600 B = 50 MB).
- Couldn't distinguish "I missed events" from "nothing changed".
- Forced clients to implement a homegrown ordering inference based
  on `ts` (timestamps) — fragile under clock skew.

B2 ("`previous` ist `omitempty` — Frame-Klassifikation fehlt") was a
related concern: clients had no way to tell whether an event with a
missing `previous` was an initial push or a delta. The asks.md
primary recommendation was a `kind` discriminator on the envelope.

Both gaps land in this ADR because they share the envelope-extension
surface and the same client-side use case (correct state reconstruction
across reconnects).

## Consequences

### What ships

- `Event` struct (`internal/north/rest/ws/hub.go`) grows `Seq uint64`
  and `Kind string` fields. Both default safely (Seq assigned by
  Publish; Kind defaults to `change` on the wire when the producer
  leaves it empty).
- `Hub` grows `seqMu`, `seqNext`, `replay` (ring buffer), `replayMx`
  (ceiling). New methods: `CurrentSeq()`, `SetReplayCapacity(n int)`,
  `Replay(since, match)` returning `ReplayResult{Events, Lost, OldestSeq}`.
- `inboundMessage` (client.go) grows `Since *uint64`. `outboundEvent`
  grows `Seq` + `Kind` (always emitted; `Kind` defaults to `change`).
- New `outboundOp` variants `replay_done` (carries `seq`) and
  `replay_lost` (carries `oldest_seq`).
- `client.replayFrom(since)` walks the buffer via `hub.Replay`,
  enqueues matching events, sends the appropriate ack frame.
- `assets/openapi.yaml` `WsEnvelope` schema is updated with the new
  required fields + descriptions.
- `assets/wsapi.json` envelope description grows `seq` + `kind`; new
  root-level `resume` block documents the replay protocol +
  buffer-capacity default.
- `docs/external-clients/topic-hierarchy.md` adds a "Resume after
  reconnect" section.
- Test coverage in `internal/north/rest/ws/hub_replay_test.go`:
  monotonic Seq, Kind default + override, Replay with empty/full
  ranges, match-filter, Lost detection, SetReplayCapacity behaviour.

### Bounded-replay tradeoff

The 1024-event default is intentional. Storing every event since boot
would grow without bound; persisting the buffer across daemon
restarts would couple the replay contract to SQLite and add
schema-evolution complexity for marginal benefit (the daemon restart
itself already broke the seq continuity — clients can detect it via
`Seq` reset and re-snapshot regardless).

Operators with high-event-rate deployments can tune the cap at
construction time via `Hub.SetReplayCapacity`. A future config knob
(`North.REST.WS.ReplayCapacity`) would surface it without code
changes; deferred until concrete operator demand surfaces.

### `kind` adoption is incremental

The schema field is wired through and every emit defaults to
`change`. Producers that want to mark `initial` (e.g. the
`EventBridge.PublishInitialSnapshot` path) can do so by publishing
via the bare `hub.Publish(Event{Kind: KindInitial, ...})` or via
new typed variants that take a Kind parameter. Wiring the bridge
to emit `initial` is a follow-up — the contract is in place; the
specific producer is a small mechanical change once a consumer
relies on the distinction.

### Backwards compatibility

The envelope additions are purely additive. Existing clients that
ignore the new fields continue to work unchanged. Clients that
inspect `kind` must treat unknown values as `change` per the schema
description.

The `subscribe` op extension is opt-in: clients that don't send
`since` get the previous behaviour (subscribe + receive future
events; no replay). The new `replay_done` and `replay_lost`
control frames are only emitted in response to a `since`-bearing
subscribe.

`Seq` always starts at 1 after a daemon restart. Clients that store
`since` across daemon restarts will see `replay_lost` on first
reconnect (because the new `oldest_seq` is 1, smaller than the
stored cursor's expected continuity) and recover via snapshot. This
is intentional: the daemon doesn't promise replay continuity across
its own lifetime; clients can use the `restarted_at` from
`GET /info` to detect this.

## Alternatives considered

### A. Per-data-point generation counter on the REST snapshot

Considered as option (2) in asks.md B1: each DataPointSummary carries
a `generation: int` field; on reconnect the client compares
generations and re-fetches only the changed DPs. Rejected because
it forces an extra `/snapshot` call on every reconnect (defeating the
goal of skipping the full snapshot when the client only missed a
handful of events) and pushes the diff computation onto the client.
The seq-cursor approach handles "I missed 5 events" in 5 frame
replays; the generation-counter approach demands a full snapshot
regardless.

### B. Persistent replay buffer in SQLite

Considered for cross-restart continuity. Rejected because:

- It adds schema evolution + migration burden.
- It forces every Publish into a disk write — a latency cost the
  in-process bus today does not pay.
- The daemon restart itself is the natural seq-reset boundary;
  clients can detect it via the `restarted_at` field in `/info`.

A future variant could replay the last few seconds of events at
boot from a circular file (for crash-recovery scenarios). Not in
scope for this ADR.

### C. Separate WS endpoint for resume

Considered (`/events/resume?since=N`) so the replay protocol stays
isolated from the live stream. Rejected because the
`subscribe` op already carries the subscription set the replay
needs — sharing the connection avoids a second handshake round
and a second auth check.

## Migration impact

External clients see two additive envelope fields. Codegen that pins
on `required: [type, ts, payload]` in OpenAPI 3.1 will widen the
required set to include `seq` + `kind` on next regen. Clients that
hand-rolled their envelope parser must add the two fields (or ignore
them — both are backwards-compatible reads).

The SPA today does not read `seq` or `kind`. Adding resume on the SPA
side is a follow-up; the wire contract is in place so any external
client can ship resume support immediately.
