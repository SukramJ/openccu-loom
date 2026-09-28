# ADR 0072 — openccu-lite pushes over occulited's event stream

- Status: accepted
- Date: 2026-09-27

## Context

On a CCU every interface pushes to the daemon: OpenCCU-Loom registers a
callback URL with XML-RPC `init`, and the interface process calls
`/RPC2/<central>` for every event, new or deleted device (ADR 0002 routes
them per central). The specification's non-goal "every interface supports
push callbacks; there is no JSON-RPC-only mode" rests on that.

openccu-lite does not accept `init` on its XML-RPC proxy, and its interface
processes are not reachable from outside the box. The box's daemon,
occulited, is itself the subscriber of every interface and republishes what
it receives on one authenticated event stream (server-sent events or
WebSocket), with a replay ring, a heartbeat, `hello`/`resync` control
messages, an `interface` state message and — optionally — its own sweep of
a chosen set of datapoints. A token may hold two streams (sixteen in total
per box).

The question is how a lite central receives events without giving up
"push, never poll".

## Decision

**One server-sent-event stream per central** carries every configured
interface (`internal/central/adapter/lite_events.go`, reader in
`internal/client/transport/occulited`). It feeds the same `CallbackHandlers`
an XML-RPC callback would, so hot-plug, deletion, command tracking, PONG
correlation and bus events are shared with the CCU path.

- **Filter.** The stream asks for `event`, `interface`, `newDevices`,
  `deleteDevices`, `updateDevice`, `replaceDevice` and `readdedDevice`.
  occulited's own sweep (`state` messages) is not requested: it re-publishes
  values the value seed already reconciles and would reach the bus as changes
  no device reported. `hello` and `resync` arrive regardless.
- **Lifecycle.** The stream is attached once per central, like the callback
  route, and outlives bring-up generations. A supervisor reconnects with
  backoff and resumes from the last event id.
- **Liveness.** Every message and every heartbeat stamps the interfaces the
  stream reports up (`CallbackHandlers.NoteAlive`), so a quiet interface is
  not declared lost. An interface the box reports `down` stops being stamped
  and raises the same `ConnectionLostEvent` a CCU outage raises: forced
  device unavailability and recovery react exactly as for a CCU. A stream
  that stops delivering heartbeats is dropped and reconnected.
- **Announcer.** `init`/`deinit` are replaced: announcing an interface waits
  until the stream is live and reports the interface up; `deinit` is a no-op,
  because occulited owns the subscription on the box.
- **Reconciliation.** After a reconnect, a `resync` (gap, overflow, box
  reboot) or an interface restart, the interface is reconciled: devices are
  re-listed (new ones ingested, vanished ones deleted — a gap can drop a
  `deleteDevices`) and values are re-seeded. Reconciliations are serialised
  per central.
- **Typing.** Values on the stream are JSON; each is re-typed by its
  parameter's paramset description before it reaches the model, so a FLOAT
  sent as `1` is `1.0` and no change is invented.
- **Ping/pong** stays enabled for the lite backend kind: `ping` goes through
  the proxy and the `PONG` comes back on the stream. A real box relays the
  PONG of a caller it did not register (confirmed 2026-09-28, live check
  L-4); HmIP-RF answers on `CENTRAL:0`, BidCos-RF on `CENTRAL`.

The specification's statement becomes: **every interface pushes** — via
`init` callbacks on a CCU, via occulited's event stream on openccu-lite.
There is still no polling path.

## Alternatives considered

- **One stream per interface.** Three interfaces would exceed the two
  streams a token may hold, and would leave no slot for a reconnect overlap.
- **WebSocket instead of SSE.** Same content, but SSE is a plain `GET`
  through the box's web server and needs no framing code.
- **Consume occulited's `state` sweep as the value source.** Doubles the
  event rate and reports sweeps as changes; the value seed and the stream's
  device events are sufficient.
- **Poll `/api/rpc/v1/state`.** A polling path is a declared non-goal.

## Consequences

- A lite central registers no callback route and needs no reachable callback
  port — useful where the box cannot reach the daemon.
- The stream is a single point of delivery per central; its heartbeat and
  the per-interface state make its failure visible instead of silent.
- The two-streams-per-token limit binds other clients of the same token;
  pairing issues a dedicated token per daemon (ADR 0075).
- The SPECIFICATION's goal 1, non-goal list and §4.3 (callback servers) are
  amended accordingly.
