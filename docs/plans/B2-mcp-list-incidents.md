# B2 — MCP `list_incidents` tool

**Status**: prioritised, not started. **Effort**: S.

Add the one tool missing from the otherwise-complete MCP bridge: a
read-only `list_incidents` that projects the daemon's reliability
incident journal to LLM agents, exactly as `GET /incidents` projects it
to REST clients. The incident source is real and already wired
(SQLite store + REST handler); this is purely a new MCP tool over the
existing facade. **No OpenAPI / WS schema change and no `APIVersion`
bump** — MCP tools are not described in `assets/openapi.yaml` or
`assets/wsapi.json`.

---

## 1. Current state (verified)

- `internal/north/mcp/server.go` — `Deps` struct is the wiring surface:
  `Centrals`, `Devices`, `Writer`, `Paramsets`, `Health`, `Hubs`,
  `Audit`, `AllowWrites`, `Version`. `NewServer` calls
  `registerReadTools` always, `registerWriteTools` only when
  `AllowWrites`. Each tool is gated on its own dep being non-nil
  (`if d.Audit != nil { registerListAudit(...) }`).
- `internal/north/mcp/tools.go` — read-tool template. Registration:
  ```go
  mcpsdk.AddTool(s, &mcpsdk.Tool{Name: "list_audit", Description: "…"},
    func(ctx context.Context, _ *mcpsdk.CallToolRequest, in listAuditIn)
        (*mcpsdk.CallToolResult, listAuditOut, error) { … })
  ```
  In/out structs (`listAuditIn`/`listAuditOut`, `auditSummary`) live in
  `tools.go`. `registerListAudit` already does the limit-clamp idiom
  (default 50, max 1000) — mirror it.
- `internal/north/mcp/tools_hub.go` — hub tools that span centrals via
  `HubResolver` + `CentralLister`; `centralScopeIn` (`tools_hub.go:26`)
  is the existing `{central}` arg pattern to reuse.
- **Incident source (already wired):**
  - `internal/store/sqlite/incidents.go` — `IncidentStore` with
    `GetAllIncidents(ctx, centralName)`, `Recent(ctx, centralName,
    limit)`, `IncidentCount`, etc.
  - `internal/central/adapter/incidents.go` —
    `IncidentsStoreReader.Incidents() []hmapi.Incident` builds the
    enriched, cross-central list (`toAPIIncident` sets `Component` to
    the source: central, plus interface when interface-scoped).
  - `pkg/interfaces/rest_ports.go:117` — the canonical narrow facade:
    `type IncidentsReader interface { Incidents() []hmapi.Incident }`.
    `GET /incidents` (`handlers/incidents.go`, `ListIncidents`) depends
    on exactly this.
  - `pkg/hmapi/rest_contract.go:129` — DTO: `Incident{ ID string; When
    time.Time; Component, Severity, Summary string; Detail string }`.

---

## 2. Design decisions

1. **Reuse the REST facade.** Add `Incidents interfaces.IncidentsReader`
   to `mcp.Deps` and wire the *same* instance the REST handler already
   uses, so MCP and REST project byte-identical incident data. (Define a
   local `type IncidentsReader interface { Incidents() []hmapi.Incident }`
   in the mcp package — matching the package's existing
   `CentralLister`/`DeviceLister` style — and let
   `*adapter.IncidentsStoreReader` satisfy both.)
2. **Read-only, always-registered (when wired).** Register in
   `registerReadTools` gated on `d.Incidents != nil`. **Not** behind
   `AllowWrites` — it is a read.
3. **Args** (`listIncidentsIn`): `Central string` (optional filter),
   `Limit int` (optional; clamp default 50 / max 1000 like
   `list_audit`). `Incidents()` returns the full enriched set; filter by
   central client-side (`Component` carries the central name) and sort
   newest-first by `When` before applying the limit.
4. **Output** (`listIncidentsOut`): `Incidents []incidentSummary` with
   `ID, When (RFC3339 string), Component, Severity, Summary, Detail` —
   same RFC3339 formatting idiom as `auditSummary`.

---

## 3. Implementation steps

1. **`internal/north/mcp/server.go`** — add `Incidents IncidentsReader`
   to `Deps`; declare the `IncidentsReader` interface in the mcp package
   (next to `CentralLister`).
2. **`internal/north/mcp/tools.go`** — add `listIncidentsIn` /
   `listIncidentsOut` / `incidentSummary` structs and a
   `registerListIncidents(s, d)` mirroring `registerListAudit`
   (limit-clamp, newest-first sort, optional central filter, RFC3339
   timestamps).
3. **`registerReadTools`** — add `if d.Incidents != nil {
   registerListIncidents(s, d) }`.
4. **Daemon wiring** — in the composition root that assembles
   `mcp.Deps` (the `cmd/openccu-loom/` daemon wiring that builds the MCP
   handler; grep for `mcp.Deps{` / `mcp.Handler(`), pass the existing
   `IncidentsReader` already constructed for the REST `/incidents`
   handler. No new store or adapter is created.

---

## 4. Config & API contract changes

**None.** No `cfg:` field (so no `config.field.*` / `config.help.*`
i18n entries), and MCP tools are not in `openapi.yaml` / `wsapi.json`
(so no `make export-schemas`, no `APIVersion` bump). The tool inherits
the existing `north.mcp.enabled` gate.

---

## 5. Tests

- `internal/north/mcp/server_test.go` (or a focused `tools_incidents_test.go`,
  named after the unit — **no** `*_coverageN` names): build `Deps` with a
  fake `IncidentsReader` returning a few `hmapi.Incident`s; assert
  `list_incidents` is registered, returns them newest-first, honours
  `limit` and the `central` filter, and formats `When` as RFC3339.
- Assert the tool is **absent** when `d.Incidents == nil` (parity with
  the other dep-gated read tools).

---

## 6. Project-rule checklist

- [ ] SPDX header preserved on edited files / any new file.
- [ ] No CGo; no `panic`; `context.Context` first arg on the handler.
- [ ] Multi-CCU: central filter works; `Incidents()` is already
      cross-central and source-tagged.
- [ ] Read-only — not under `AllowWrites`.
- [ ] `CHANGELOG.md` entry (new MCP tool).
- [ ] `make lint && make test` green.

---

## 7. Acceptance criteria

- An MCP client lists the `list_incidents` tool and receives the same
  incident set as `GET /incidents`, newest-first, limit-clamped.
- `central` arg narrows results to one CCU; omitting it returns all.
- Tool is unavailable when no incident reader is wired.

---

## 8. Effort & sequencing

Single, self-contained change. **S** — a few hours including tests. No
dependencies on other roadmap items.

---

## 9. References

- `CLAUDE.md` → *Architecture Quick Reference* (north adapters, MCP as a
  thin projection), *Critical Rules* (multi-CCU, SPDX).
- ADR 0025 / 0026 (MCP bridge: default-off, read-only by default).
- `pkg/interfaces/rest_ports.go` (`IncidentsReader`),
  `internal/north/rest/handlers/incidents.go` (the REST projection this
  mirrors).
- `docs/roadmap.md` → *Planned development items* (B2); note the
  incident source is no longer a stub.
