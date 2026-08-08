# ADR 0002 — Multi-CCU as a first-class feature from v1.0

- **Status**: accepted
- **Date**: 2026-04-23
- **Supersedes**: none
- **Related**: `SPECIFICATION.md` §11.0, §16, §18, §21, §22.2

## Context

`aiohomematic` is architected around a single `CentralUnit` per process.
Home Assistant users with multiple CCUs therefore run multiple
integration instances inside Home Assistant, each with its own
configuration entry. That is reasonable when the hosting environment
(Home Assistant) provides the multi-instance affordance natively.

`OpenCCU-Loom` is a standalone daemon. Requiring one daemon per CCU
means:

- Multiple systemd units / Docker containers to manage.
- N copies of the daemon footprint (SQLite, UI, metrics endpoint,
  MQTT connection, REST listener) where one would do.
- Each daemon needs a distinct set of ports (callback listener, UI,
  REST, metrics, BIN-RPC listener) — port allocation becomes a
  per-CCU configuration burden.
- MQTT discovery for a user with two CCUs means two separate
  `homeassistant/.../config` namespaces with coordinated base topics.

We can avoid all of this by allowing one daemon to manage multiple
CCUs, with everything scoped by a `central_name` discriminator. The
underlying `aiohomematic` domain code is already conceptually
per-CentralUnit; carrying that into Go is a matter of being careful
about registries and not about deep architectural change.

### Options considered

1. **Single-CCU only in v1.0, multi-CCU as v1.x roadmap feature.**
   Simpler code path in v1.0. Downsides: the refactor later disturbs
   every coordinator, every store, every north-bound adapter, and
   every topic / URL namespace. Teaching the code to be multi-CCU-safe
   *after* it was written assuming a singleton is strictly more work
   than building it multi-CCU-safe from the start.

2. **Multi-process, no multi-CCU in a single process.**
   Clean separation but pushes operational complexity onto users.
   Against the whole point of being a consolidated daemon.

3. **Multi-CCU in v1.0 from the start.**
   One daemon, many CCUs. Every coordinator, every store, every
   adapter is designed with `central_name` as a natural dimension
   from day one.

## Decision

Adopt **option 3**: multi-CCU is a first-class v1.0 feature.

### Implications throughout the spec

- **Config YAML** exposes a `centrals:` *list* rather than a single
  `central:` block. Each entry has its own `name`, host, credentials,
  interface set, timeouts, reconnect policy. A single-CCU deployment
  is the degenerate case with a one-element list.
- **`CentralRegistry`** holds all `*CentralUnit`s in the process.
  North-bound adapters iterate over or look up by `central_name`.
- **Shared callback listeners**:
  - XML-RPC on one HTTP port; URL path `/RPC2/<central_name>` routes.
  - BIN-RPC on one TCP port; `interface_id` embedded in the envelope
    routes.
  Each `CentralUnit` registers its handlers under its own
  `central_name` at startup.
- **MQTT topic scheme** includes `central_name`:
  `<base>/<central_name>/<interface>/<device>/<channel>/<parameter>`.
  HA Discovery object IDs carry the central name too:
  `openccu_loom_<central>_<device>_<channel>_<parameter>`.
- **REST paths** are scoped:
  `/api/v1/centrals/<central_name>/devices/...`. A convenience
  route `/api/v1/devices/...` is redirected to the single central
  when exactly one is configured (so the simple case stays simple).
- **State directory** layout:
  ```
  <state-dir>/
  ├── openccu-loom.db         shared (users, tokens, global config)
  └── centrals/
      ├── <central_name_1>/
      └── <central_name_2>/
  ```
  Per-CCU caches, session recorder data, incidents live under the
  per-CCU directory. Users and auth live at the top level — one
  authentication realm covers all centrals.
- **Metrics** gain a `central` label on every client-scoped metric:
  `openccu_loom_client_state{central="house_main", interface="HmIP-RF", state="CONNECTED"}`.
  Adapter-scoped metrics (MQTT publish rate, REST request duration)
  do **not** carry the label — they measure the north-bound side,
  not the south-bound side.
- **Config UI** has a central selector at the top of the sidebar.
  Selecting a central scopes the rest of the UI. Users with one
  central see the selector collapsed to a label.
- **Event Bus**: events carry `CentralName` in their base fields so
  handlers can filter. North-bound adapters subscribe centrally and
  fan out by `central_name`.

### What is intentionally *not* multi-CCU

- The authentication realm: one user pool covers all centrals. We do
  not plan per-central user sets — operators typically have access to
  all their CCUs.
- The daemon state directory layout described above is per-CCU for
  caches only, not for users / tokens / UI prefs.
- The MQTT broker connection: one connection serves all centrals.
  Subscribed topics are deduplicated; published topics are
  disambiguated by `central_name` in the path.
- The OIDC configuration: one IdP integration for the whole daemon.
- Logging: a single slog handler emits all events; `central_name` is
  a structured log field.

## Consequences

### Positive

- Operationally simpler for end users: one process, one config file,
  one set of credentials, one metrics endpoint, one log stream.
- No rewrite halfway through the project.
- `central_name` becomes a first-class dimension for debugging,
  filtering, and diagnostics.
- MQTT topic layout reflects the data model cleanly from the start;
  users building automations on top have a single consistent schema.

### Negative

- Slightly more complexity in every coordinator and every adapter
  (they must iterate over or look up by `central_name`).
- The degenerate single-CCU case carries a one-element list in YAML,
  which is marginally more verbose than a scalar.
- Callback server routing logic (path-based for XML-RPC,
  envelope-based for BIN-RPC) is a little more elaborate than a
  single-CCU handler.

### Mitigations

- Contract tests (`TestMultiCentralCallbackRouting`,
  `TestMultiCentralMQTTTopicScoping`, …) lock the contract early so
  regressions are visible.
- The `openccu-loom init` command generates a YAML skeleton with a
  single `centrals:` entry, so beginners never see an empty list.
- The Setup Wizard drives the user through adding one central first;
  adding a second central is a clearly signposted later step.

## Follow-ups

The follow-up backlog from this decision has shipped; any further
multi-CCU work would land as a new ADR or a `notes/plans/roadmap.md` entry.
