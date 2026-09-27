# ADR 0074 — Per-central features and what "absent" means

- Status: accepted
- Date: 2026-09-27

## Context

Capabilities in OpenCCU-Loom were per *interface backend*
(`backends.Capabilities`) and answered "can this transport do X". They did
not answer the questions a mixed fleet raises: does this *central* have
system variables at all (a box has none), may the daemon's credential
reboot it (a token without the `power` scope may not), is it ready yet.

Where a capability was false, the code often did nothing and reported
success: a backup with no backup support stored a zero-byte archive; a
service-message suppression the interface could not perform answered 2xx.
Views and MQTT entities for things a system does not have rendered empty
or went `unavailable` for ever.

## Decision

Each central carries a **feature set** (`central.Features`): for every key in
`hmenum.AllFeatures()` — `hub.sysvars`, `hub.programs`, `system.reboot`,
`system.backup.create`, `taxonomy.tree`, `install_mode`, … — whether the
central offers it, and if not, why:

- `not_supported_by_system` — the system has no such thing;
- `missing_scope` — the daemon's credential lacks the named scope;
- `not_ready` — the central has not resolved yet.

A CCU offers every feature it always had (and not `taxonomy.tree`). A box's
set follows its token's scopes, expanded by their implications, re-read at
bring-up, every ten minutes and after any scope refusal; a change publishes
`central.features_changed`.

**"Absent" is explicit on every surface:**

- **REST**: `422` with problem type `feature_unavailable` and a `feature`
  member `{central, key, reason, scope?}` — 422 because clients already read
  it as "not supported".
- **WebSocket**: error code `feature_unavailable` with the same `details`.
- **`GET /system/ccu`**: each entry's `system_type` and `features` map;
  `central.features_changed` pushes the complete set.
- **MQTT**: a hub-plane entity whose feature is absent is not declared, and
  is retracted when the feature goes away; declared and published stay the
  same set (a round-trip test per profile).
- **SPA**: a navigation entry is offered while at least one central offers
  its feature; inside a view an absent feature's actions are hidden and the
  reason is named, never shown and failing.
- **MCP**: hub tools name the unavailable centrals and why, instead of
  returning an empty list.

**Enforcement is by what the profile installs, not by checks in shared
code.** Every port a profile cannot serve returns a
`*hmerr.FeatureUnavailableError`, which wraps the legacy sentinel the
existing code already branches on (`hub.ErrNoInboxAccepter`,
`backends.ErrUnsupported`, …), so every `errors.Is` keeps working. The lite
feature table and the lite refusing ports are built from the same table, and
`TestLiteFeatureTableMatchesRefusingPorts` calls every port behind every
absent key. The north-bound handlers gained one first case that maps the
error; the CCU never produces it, so CCU answers are unchanged.

The silent successes found on the way are fixed: an empty backup archive is
an error, and an unsuppressible service message is refused.

## Alternatives considered

- **A feature flag per call site.** Scatters the system-type question through
  shared code; exactly what ADR 0071 rules out.
- **Keep per-interface capabilities only.** They cannot express a credential
  scope or a system that has no hub at all.
- **Hide absent things silently (empty lists).** Indistinguishable from
  "nothing there yet"; a client can neither explain nor adapt.
- **`501 Not Implemented`.** Suggests the daemon lacks the code; the absence
  is the central's, and 422 is what clients already handle.

## Consequences

- A client can tell "not on this system", "not with this token" and "not
  yet" apart and say so to its user.
- A new port on a profile that cannot serve it must be added to the feature
  table, or the refusal test fails.
- The surface registry gained `feature:<key>` gates (programs, system
  variables, inbox, heating groups, backups).
