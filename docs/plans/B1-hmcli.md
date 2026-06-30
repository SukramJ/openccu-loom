# B1 — `hmcli` power-user CLI

**Status**: prioritised, not started. **Effort**: M.

Grow `cmd/hmcli` from a four-command admin helper into a full
command-line REST client against a running daemon, so operators and
scripts can list/read/write devices, sysvars, programs and paramsets
and tail the live event stream without curl or the SPA. This is a
**pure REST client** — it adds no daemon-side code and consumes only
endpoints that already exist in `assets/openapi.yaml`, so **no OpenAPI /
WS schema change and no `APIVersion` bump are required**.

---

## 1. Current state (verified)

`cmd/hmcli/` (≈542 LOC, hand-rolled `flag`, no CLI framework):

- `main.go` — `run()` dispatches on `args[0]` via a `switch`:
  `version` / `config validate <file>` / `cache clear` / `export-def` /
  `help`. The package doc comment is itself stale (mentions an
  `openapi show` command that does not exist) — fix it while here.
- `export_def.go` — **the canonical daemon-client pattern** to copy:
  `flag.NewFlagSet`; flags `-host` (default `http://localhost:8119`),
  `-address`, `-out`, `-token` (sent as `Authorization: Bearer`),
  `-user` / `-password` (`req.SetBasicAuth`), `-timeout` (default 60s);
  `http.NewRequestWithContext`; `&http.Client{Timeout:…}`; on non-200 it
  reads up to 4 KiB of the body into the error.
- `cache.go` — `cache clear` has an online (REST) and `--offline`
  (direct SQLite) path. **Inconsistency to fix:** its online default is
  `--url http://localhost:2121` while `export-def` uses `-host
  http://localhost:8119`. 8119 is the current default REST port
  (v0.21.3). Standardise every command on `http://localhost:8119`.
- Tests: `main_test.go`, `cache_test.go`, `export_def_test.go` —
  table-driven, no network (or `httptest`). Follow this style.

DTOs:

- `pkg/hmapi/api.go` is an **in-process** facade (`HomematicAPI`), **not**
  a REST client — do not use it for wire decoding.
- `pkg/hmapi/rest_contract.go` holds REST wire DTOs and is importable:
  `Incident`, `InterfaceState`, `Link`, `BackupEntry`, `ConfigSnapshot`,
  schedule/`UISchema*` types. Device / sysvar / program list+detail DTOs
  live in `internal/north/rest/dto` (same Go module, so `cmd/hmcli` may
  import it — prefer this over re-declaring structs, to stay in sync).

Endpoints the new commands map onto (all already in
`assets/openapi.yaml`, so reuse, do not add):

| Command | Method + path |
|---|---|
| `devices list` | `GET /devices` |
| `devices get <addr>` | `GET /devices/{addr}` |
| `devices set <addr> <ch> <param> <val>` | `PUT /devices/{addr}/channels/{no}/data-points/{param}/value` |
| `devices get-value <addr> <ch> <param>` | `GET …/data-points/{param}/value` |
| `paramset get <addr> <MASTER\|VALUES>` | `GET /devices/{addr}/paramsets/{key}` |
| `paramset set <addr> <key> k=v…` | `PUT /devices/{addr}/paramsets/{key}` |
| `program run <id>` | `POST /programs/{id}/execute` |
| `sysvar list` | `GET /sysvars` |
| `sysvar get <name>` | `GET /sysvars/{name}` |
| `sysvar set <name> <val>` | `PUT /sysvars/{name}` |
| `events tail` | `GET /events` (RFC 6455 WebSocket upgrade) |

`/events` is a **WebSocket** stream, not SSE: after upgrade the client
sends `{"op":"subscribe","topics":["device.*","hub.*"]}`; the server
pushes `WsEnvelope` frames and supports replay via a `since` cursor. The
daemon's WS implementation uses `github.com/gorilla/websocket v1.5.3`
(already in `go.mod`, currently `// indirect`).

---

## 2. Design decisions

1. **Pure REST/WS client.** No daemon code, no new endpoints. Confirm a
   target endpoint exists before adding a command; if one is genuinely
   missing, that is a *separate* item (and would then trigger the API
   contract rules — see §5), not part of B1.
2. **Shared client + flags.** Extract a `cmd/hmcli/client.go` with a
   `daemonClient` (base URL, auth, timeout, `http.Client`) and helpers
   `getJSON(ctx, path, &out)`, `sendJSON(ctx, method, path, body, &out)`
   that centralise the `export_def.go` request/auth/error-body pattern.
   Shared persistent flags: `--host` (default `http://localhost:8119`),
   `--token`, `--user`, `--password`, `--timeout`. Fix `cache.go` to use
   the same default host.
3. **CLI framework — Cobra (decided).** Adopt `github.com/spf13/cobra`
   (MIT — allowed) as the command framework now; the hand-rolled `switch`
   dispatch in `main.go` is **removed**, not kept as a fallback. The
   command surface is structured as Cobra subcommands: a root command
   carrying the shared **persistent** flags (`--host`, `--token`,
   `--user`, `--password`, `--timeout`), with command groups
   `devices {list,get,get-value,set}`, `sysvar {list,get,set}`,
   `program {run}`, `paramset {get,set}`, `events {tail}`, plus the
   existing `version`, `config`, `cache`, `export-def` migrated verbatim
   so their behaviour and tests are unchanged. This is a settled
   decision — not an open question.
4. **Multi-CCU.** Device/paramset operations address by device address
   (the daemon resolves the owning central). Sysvars and programs are
   per-central: add a `--central` flag where the endpoint needs it; list
   commands print the owning central column when more than one exists,
   mirroring the SPA `DeviceList` behaviour.
5. **`events tail`** connects with `gorilla/websocket`, subscribes to a
   `--topics` set (default `["#"]` / all), and prints one line per
   envelope (`ts type topic payload`), with `--json` for raw frames.
   Promote `gorilla/websocket` from indirect to a direct dependency.
6. **Output.** Human-readable tables by default; `--json` on every read
   command emits the decoded DTO verbatim for scripting.
7. **D3 boundary (explicit).** This REST-client `hmcli` does **not**
   import or embed the `openccu-data` archives — it only speaks to the
   daemon. Therefore it does **not** become the "second Go consumer" and
   does **not** unblock the `openccu-data-go` module (roadmap D3 blocker
   stands). State this in the PR description so the link is not assumed.

---

## 3. Implementation steps

1. **`cmd/hmcli/client.go` (new).** `daemonClient` struct + constructor
   from the shared flags; `getJSON` / `sendJSON` / `doRaw` helpers
   carrying auth (`Bearer` or basic) and the 4-KiB error-body decode
   from `export_def.go`. SPDX header.
2. **Introduce Cobra in `main.go`.** Root command with persistent client
   flags; register subcommands. Re-wire `version`, `config`, `cache`,
   `export-def` onto Cobra commands calling the existing functions
   unchanged. Keep `run([]string, stdout, stderr) error` test seam.
3. **`cmd/hmcli/devices.go` (new).** `devices list|get|get-value|set`
   using `client.go` + `internal/north/rest/dto` types. `set` maps to the
   `PUT …/data-points/{param}/value` body shape (check the handler/DTO
   for the exact JSON: `{"value": …}`).
4. **`cmd/hmcli/paramset.go` (new).** `paramset get <addr> <key>` and
   `paramset set <addr> <key> k=v …` (parse `k=v` pairs, type-coerce as
   the PUT handler expects).
5. **`cmd/hmcli/hub.go` (new).** `sysvar list|get|set` and `program run`,
   with `--central` where the endpoint requires it.
6. **`cmd/hmcli/events.go` (new).** `events tail` — `gorilla/websocket`
   dial to `ws(s)://<host>/events` (derive scheme from `--host`), send
   the subscribe op, stream frames; `--topics`, `--json`, `--since`.
7. **Fix `cache.go`** default host to `http://localhost:8119`; fix the
   stale `main.go` package doc comment.
8. **`go.mod`** — add `spf13/cobra` (direct); promote `gorilla/websocket`
   to direct. Run `go mod tidy`.

---

## 4. Config & API contract changes

**None.** B1 adds no `cfg:`-tagged config and changes neither
`assets/openapi.yaml` nor `assets/wsapi.json`. Therefore:

- No `config.field.*` / `config.help.*` i18n entries are required.
- No `make export-schemas` run and **no `APIVersion` bump** are required
  (the API contract guard only triggers on spec edits).

If implementation reveals a genuinely missing endpoint, stop and treat
that endpoint as its own change: edit `openapi.yaml`, run
`make export-schemas`, bump `APIVersion` — then resume the CLI command.

---

## 5. Tests

- One `*_test.go` per command file (`devices_test.go`, `paramset_test.go`,
  `hub_test.go`, `events_test.go`, `client_test.go`), named after the
  unit — **no** `*_coverageN` / `*_batchN` names.
- Each read/write command: drive against an `httptest.Server` returning
  canned JSON; assert request method/path/auth header and decoded
  output. Mirror `export_def_test.go` / `cache_test.go`.
- `events tail`: spin a tiny `httptest.Server` that upgrades with
  `gorilla/websocket`, accepts the subscribe op, pushes one envelope;
  assert the client prints it and exits on context cancel.
- Keep the existing `main_test.go` green after the Cobra migration.

---

## 6. Project-rule checklist

- [ ] SPDX header on every new `.go` file (`// SPDX-License-Identifier:
      MIT` + the project copyright line).
- [ ] No CGo (`CGO_ENABLED=0` build stays green).
- [ ] `context.Context` first arg on every request helper
      (`http.NewRequestWithContext`).
- [ ] No `panic` outside `main`.
- [ ] Multi-CCU: `--central` on sysvar/program ops; central column in
      list output when >1 central.
- [ ] New deps are MIT/BSD (Cobra MIT, gorilla/websocket BSD-2) — no
      GPL/LGPL/MPL/AGPL.
- [ ] `CHANGELOG.md` entry (user-visible: new CLI commands).
- [ ] `make lint && make test` green; full-repo lint (`golangci-lint
      run ./...`), not scoped.

---

## 7. Acceptance criteria

- `hmcli devices list --host … --token …` prints the device inventory;
  `--json` emits the raw DTO array.
- `hmcli devices set <addr> <ch> STATE true` flips a real datapoint
  (verified against a running daemon / godevccu).
- `hmcli sysvar set <name> <val> --central <c>` and `hmcli program run
  <id> --central <c>` succeed against the matching endpoints.
- `hmcli events tail` prints live frames and exits cleanly on Ctrl-C.
- All commands share one auth/host flag set and default to
  `http://localhost:8119`.

---

## 8. Effort & sequencing

`client.go` + Cobra migration (keep existing commands green) →
`devices` → `paramset` → `hub` (sysvar/program) → `events tail`. Each
command is independently shippable. Effort **M**; `events tail` is the
only non-trivial piece (WS client).

---

## 9. References

- `CLAUDE.md` → *Repository Structure* (`cmd/hmcli`), *Common Tasks → Add
  a REST endpoint* (DTO location `internal/north/rest/dto`), *Critical
  Rules* (SPDX, no CGo, multi-CCU), *Code Quality* (ctx-first, errors).
- `assets/openapi.yaml` — endpoint contracts the client consumes
  (`/devices*`, `/sysvars*`, `/programs*`, `/events`).
- `docs/external-clients/topic-hierarchy.md` — `/events` topic wildcards
  for `events tail`.
- `docs/roadmap.md` → *Planned development items* (B1) and *Optional: Go
  wrapper module for openccu-data* (D3 boundary above).
