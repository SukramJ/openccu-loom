# Un-Ignore UI Concept

**Status: Implemented.** Every layer described below has shipped —
config knob, SQLite persistence, REST endpoints, and the Svelte
screen. This document is kept as the design record for the feature;
where the original concept differs from the shipped shape, the
diff is called out inline.

## Background

The `un_ignore` mechanism promotes parameters that would otherwise be
suppressed by the visibility decider (internal service parameters,
rarely-used MASTER knobs) into first-class data points, on a per
`MODEL:CHANNEL:PARAMETER` pattern basis. This document sketches the
UI + the thin wire-up layer that made the backend building blocks
reachable from the SPA — all of it has since landed.

## Inventory — Backend

| Building block                                | File                                                          | Status  |
| ---------------------------------------------- | --------------------------------------------------------------- | ------- |
| Per-DP marking `MarkUnIgnored`/`IsUnIgnored`   | `internal/model/datapoint/base.go:271-287`                     | shipped |
| Visibility decider                             | `internal/store/visibility/decider.go`                          | shipped |
| Parser for `MODEL:CHANNEL:PARAMETER`           | `internal/store/visibility/parser.go`                            | shipped |
| Materializer consideration                     | `internal/model/custom/materialize.go:496+`                      | shipped |
| Per-interface pipeline application              | `internal/central/adapter/device_pipeline.go:443-454`            | shipped |
| QueryFacade candidate list                     | `internal/central/queryfacade.go:329 GetUnIgnoreCandidates`      | shipped |
| Visibility-registry API `LoadUnIgnore`          | `internal/store/visibility/registry.go:47`                       | shipped |
| Config knob (YAML)                             | `config.CentralConfig.Visibility.UnIgnore []string` (`internal/config/config.go:1383`) | shipped |
| SQLite persistence                             | `internal/store/sqlite/migrations/014_visibility_unignore.sql`   | shipped |
| REST endpoints                                 | `internal/north/rest/handlers/visibility.go` (+ `_test.go`)      | shipped |
| Svelte screen                                  | `assets/ui/src/routes/UnIgnoreList.svelte`                       | shipped |

## Inventory — Python reference

`homematicip_local` (the HA integration) walks the user through the
`Advanced Configuration` step in the config flow:

1. Settings → Devices & Services → Homematic(IP) Local → Configure
2. Tab "Interface" → enable "Advanced configuration"
3. Multi-select dropdown over all candidates (from
   `query_facade.get_un_ignore_candidates`)
4. The selection is persisted as `CONF_UN_IGNORES` in the HA config
   entry
5. `control_unit.py` picks up the list on reload and forwards it via
   `config_builder.with_un_ignore_list(...)` → `Registry.LoadUnIgnore`
6. The integration reloads automatically

Format of individual entries:

```
DEVICE_TYPE:CHANNEL:PARAMETER
```

with `*` wildcards for `DEVICE_TYPE` and/or `CHANNEL`. Examples:

| Pattern                 | Effect                                       |
| ------------------------ | -------------------------------------------- |
| `HmIP-eTRV-2:0:LOW_BAT` | Surface LOW_BAT on channel 0                 |
| `*:*:RSSI_PEER`         | RSSI_PEER on every channel of every device    |
| `*:0:OPERATING_VOLTAGE` | OPERATING_VOLTAGE on channel 0 of every device |
| `LOW_BAT`               | Short form: all devices, all channels         |

## UI concept (Svelte 5 SPA)

### Embedding

Shipped as a standalone top-level route, `/visibility` (`App.svelte`:
`if (path === "/visibility") return { kind: "visibility" };` renders
`<UnIgnoreList />`) — not nested under Settings as the original
concept proposed. It is reachable directly rather than as a Settings
sub-page.

### View structure — `UnIgnoreList.svelte`

Modeled on `assets/ui/src/routes/matter/MatterExposureList.svelte` —
same shape: multi-select over the backend candidate list, bulk
enable/disable, search, persistence via REST PUT. Shared building
blocks: `$lib/components/ui/{Button, Input}`, `$lib/i18n`,
`$lib/stores/toast.svelte`.

```
┌────────────────────────────────────────────────────────────────┐
│  Un-Ignore Parameters                                          │
│                                                                │
│  Hidden parameters that should be surfaced as data points.     │
│  Use at your own risk — excessive writes to MASTER paramset    │
│  values can damage devices.                                    │
│                                                                │
│  ☐ Include MASTER parameters (off by default)                  │
│                                                                │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │ Search… 🔍                                               │  │
│  └──────────────────────────────────────────────────────────┘  │
│                                                                │
│  Filter by device model: [HmIP-eTRV-2 (12)] [HmIP-SWDO (8)]    │
│                          [* (wildcards) (4)] [Clear]           │
│                                                                │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │ ☑ ID │ Pattern                       │ Description       │  │
│  ├──────┼───────────────────────────────┼───────────────────┤  │
│  │ ☑    │ HmIP-eTRV-2:0:LOW_BAT         │ Low battery (HmIP …│  │
│  │ ☐    │ HmIP-SWDO:1:ERROR             │ Sensor error      │  │
│  │ ☑    │ *:*:RSSI_PEER                 │ Signal strength … │  │
│  │ ☐    │ *:0:OPERATING_VOLTAGE         │ Operating voltage │  │
│  │ …                                                        │  │
│  └──────────────────────────────────────────────────────────┘  │
│                                                                │
│  [Add custom pattern…]   [Save]   [Discard]                    │
│                                                                │
│  ⚠ 3 changes pending — devices will be re-materialised on Save │
└────────────────────────────────────────────────────────────────┘
```

### Data flow

1. **Mount** → `GET /api/v1/visibility/unignore/candidates?include_master=false`
   returns the sorted candidate list of currently-hidden parameters
   (backend endpoint uses `QueryFacade.GetUnIgnoreCandidates`).
2. **Mount** → `GET /api/v1/visibility/unignore` returns the
   currently-active list (from SQLite persistence).
3. **Search/filter/toggle** → local mutation of a `$state<Set<string>>`.
4. **Add custom pattern** → opens a modal with an `<input>` for
   free-form entry; client-side validation against
   `^[A-Za-z0-9\-_*]+:[0-9*]+:[A-Z_]+$`, inline hint if the pattern
   matches no candidate.
5. **Save** → `PUT /api/v1/visibility/unignore` with the complete list
   state; the server validates via `ParseUnIgnoreLine`, persists to
   SQLite, re-invokes `Registry.LoadUnIgnore`, and triggers a
   materializer re-run per central.
6. Response includes `applied_count`, `parse_errors[]`,
   `affected_devices`. A toast shows the result.
7. **Discard** → resets state to the server-side value.

### Interaction details

- **Confirmation dialog** before "Save" when `include_master=true` or
  when the diff adds/removes a MASTER pattern: shows the count of
  affected devices + a re-materialize hint. Apple Home / MQTT / REST
  have open subscriptions that will see a re-discovery after the
  re-run.
- **Bulk toggle** via the header checkbox: enables/disables all
  filtered rows.
- **Optimistic UI** — the Save round-trip is non-trivial (a
  materializer run), so a spinner + disabled Save button until the
  response arrives; rollback to the previous state on error.
- **Read-only mode** for the viewer role: shows the list, "Save"
  greyed out.

### i18n keys

New keys in `internal/i18n/catalogs/{en,de}.json`:

```
unignore.title                = Un-Ignore Parameters
unignore.subtitle             = Hidden parameters that should be surfaced as data points.
unignore.warning              = Use at your own risk — excessive writes to MASTER paramset values can damage devices.
unignore.include_master       = Include MASTER parameters
unignore.search_placeholder   = Filter by device, channel or parameter…
unignore.add_pattern          = Add custom pattern…
unignore.save                 = Save
unignore.discard              = Discard
unignore.unsaved_changes      = {n} changes pending — devices will be re-materialised on Save
unignore.invalid_pattern      = Invalid pattern (expected MODEL:CHANNEL:PARAMETER)
unignore.no_candidates        = No hidden parameters available — all parameters already visible.
unignore.saved                = Un-ignore list updated. {n} parameters now visible.
```

## REST endpoints

| Method | Path                                       | Body                          |
| ------ | -------------------------------------------- | -------------------------------- |
| GET    | `/api/v1/visibility/unignore`                | —                                |
| PUT    | `/api/v1/visibility/unignore`                | `{patterns: ["..."]}`            |
| GET    | `/api/v1/visibility/unignore/candidates`     | Query: `include_master=bool`     |

DTOs:

```yaml
UnIgnoreListResponse:
  type: object
  required: [patterns]
  properties:
    patterns: { type: array, items: { type: string } }
    pattern_count: { type: integer }
    updated_at: { type: string, format: date-time }

UnIgnoreUpdateRequest:
  type: object
  required: [patterns]
  properties:
    patterns: { type: array, items: { type: string } }

UnIgnoreUpdateResponse:
  type: object
  required: [applied_count, parse_errors, affected_devices]
  properties:
    applied_count: { type: integer }
    parse_errors:
      type: array
      items: { type: string, description: "human-readable parse error per offending line" }
    affected_devices: { type: integer }

UnIgnoreCandidateList:
  type: object
  required: [candidates]
  properties:
    candidates:
      type: array
      items: { type: string }
    include_master: { type: boolean }
```

Authorization: `admin` to write, `operator`/`admin` to read the
active list. `viewer` may see the candidate list but not the active
list (defense-in-depth: viewers should not learn MASTER paths).

## Persistence

SQLite table added via a goose migration,
`internal/store/sqlite/migrations/014_visibility_unignore.sql`:

```sql
-- +goose Up
CREATE TABLE visibility_unignore (
    central_name TEXT NOT NULL,
    pattern      TEXT NOT NULL,
    updated_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (central_name, pattern)
);

-- +goose Down
DROP TABLE visibility_unignore;
```

The table is partitioned per `central_name` (multi-CCU first-class,
per ADR 0002). On daemon start the list is read per central and
replayed via `Registry.LoadUnIgnore(strings.NewReader(strings.Join(patterns, "\n")))`.

## Config knob (YAML)

Bootstrap path: a central-level default can be set via `config.yaml`,
used as the initial fill when the SQLite table is empty (not as an
override — runtime changes via REST win).

```yaml
centrals:
  - name: OttoGo
    ...
    visibility:
      un_ignore:
        - "HmIP-eTRV-2:0:LOW_BAT"
        - "*:*:RSSI_PEER"
```

Mapped to `config.CentralConfig.Visibility.UnIgnore []string`.

## Test plan

- **Unit**: `parser_test.go` covers `MODEL:CHANNEL:PARAMETER` parsing.
  `handlers/visibility_test.go` covers the PUT round-trip + malformed
  patterns.
- **Integration** (`-tags=integration`): load a device via `godevccu`,
  `LOW_BAT` is initially hidden, REST PUT makes it visible,
  `GET /api/v1/devices/.../channels/0` contains the data point.
- **Integration**: `tests/integration/visibility_unignore_test.go` pins the
  `IGNORE_FOR_UN_IGNORE_PARAMETERS` list (parameters that must never
  be un-ignored — e.g. internal service parameters) against the
  Python constant.
- **Snapshot parity**: `tests/integration/TestModelSnapshotDumpAgainstGodevccu`
  runs with an un-ignore list on both sides — drift stays 0.

## Design decisions (recorded)

1. **MASTER-reload hint** — dropped. MASTER paramset changes need no
   device restart. The UI stays consistent between VALUES and MASTER,
   with no inline badge and no confirm dialog. The `include_master`
   checkbox remains as a pure list filter (prevents the default
   candidate list from being swamped with MASTER parameters).
2. **Export/Import** — no. The list lives only in SQLite; backup
   happens indirectly through the existing backup mechanism
   (`var/backups/`). This avoided an extra REST endpoint and a
   text-format compatibility path.
3. **Audit-log integration** — yes: every `PUT` round-trip emits an
   audit event with:
   - user (from `reqctx`)
   - diff: `added[]`, `removed[]` patterns
   - `affected_devices` counter
   - source: `rest` (or `config_yaml` when the bootstrap knob sets the
     list — that initial load is logged as a single system event, not
     per pattern)
