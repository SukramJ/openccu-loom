# ADR 0060 — CCU-facing interface ids carry a `loom` prefix and drop the repeated name

- Status: accepted (partially supersedes ADR 0024's `InitInterfaceID` formula)
- Date: 2026-08-04

## Context

ADR 0024 established the two interface ids: the canonical, host-independent
`InterfaceID = <central_name>-<interface>` used everywhere inside the daemon
and on every external surface, and the wire-boundary
`InitInterfaceID = <instance_name>-<central_name>-<interface>` advertised to
the CCU at `init()`/`deinit()` and registered on the BIN-RPC callback server.
The `instance_name` exists to keep two daemons against the same CCU apart:
the CCU keys its callback registry by `interface_id`, so a shared value would
have one daemon's `init()` overwrite the other's registration.

Field reports from the add-on deployment surfaced two problems with the
resulting string, both about what an operator sees rather than about
correctness:

1. **The name appears twice.** Both `instance_name` and `central_name`
   default to a host-derived value. Running the daemon as the CCU's own
   add-on makes them identical, so the id reads
   `RM-Test-VM-96-RM-Test-VM-96-BidCos-RF`. The repetition adds no
   uniqueness — it is a consequence of *where* the daemon runs, not of
   *which* daemon it is.

2. **Nothing identifies the daemon.** The CCU prints the raw `interface_id`
   in its own logs and diagnostics. An id assembled purely from host and
   central names is indistinguishable from any other XML-RPC client, so an
   operator reading `rfd` output cannot attribute a registration to a
   process. This matters most in exactly the situation where attribution is
   wanted: a stale registration whose events the CCU keeps retrying.

## Decision

The CCU-facing id becomes:

```
InitInterfaceID = loom-<instance_name>-<central_name>-<interface>
```

with `<instance_name>` **omitted when it equals `<central_name>`**, yielding
`loom-<central_name>-<interface>` in the co-located add-on case.

The `loom` prefix is a constant (`adapter.IDPrefix`), not a configurable
value: its purpose is attribution in third-party logs, which a per-install
string would defeat.

`CanonicalInterfaceID(instanceName, centralName, id)` inverts the mapping for
inbound callbacks, replacing `StripInstance`. It needs the central name
because the collapsed form is not decidable from the id alone — stripping the
instance prefix unconditionally would eat the central name. Ids without the
prefix are treated as the pre-prefix ADR 0024 shape, which always carried the
instance name, so a callback in flight across an upgrade still resolves.

**Unchanged:** the canonical `InterfaceID` stays `<central_name>-<interface>`.
`DataPointKey`s, the value-writer key, MQTT topics, REST/WS payloads and the
`values_cache` are untouched and stay host-independent
(`ValuesCacheSchemaVersion` stays `1`). The change is confined to what
crosses the CCU boundary.

## Consequences

- On upgrade the daemon registers under the new id. The CCU keys its
  callback registry by `interface_id`, so the registration made by the
  previous release is not replaced but **orphaned**: the CCU keeps
  attempting delivery to it until it is restarted, logging a transport
  error per attempt. This is accepted deliberately rather than mitigated by
  deinitialising the old id at startup — the noise is bounded, ends at the
  next CCU restart, and the alternative embeds knowledge of a superseded id
  formula in the connection path indefinitely. Operators upgrading a
  long-running CCU can restart it to clear the entries; the release notes
  say so.
- Two daemons on one host against one CCU still differ, because their
  instance names do (the collapse only triggers when instance and central
  name are the same string).
- A daemon whose instance name is deliberately set to the central name
  loses the distinguishing component — the same string cannot both name
  the daemon and the CCU. This is the intended reading: identical names
  describe one deployment.
