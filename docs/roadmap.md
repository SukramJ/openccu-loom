# OpenCCU-Loom — open roadmap items

This file tracks deliverables that are scoped but deferred. Items land
here when we explicitly choose not to do them now but commit to
revisiting them later. Completed items are moved to `CHANGELOG.md`.

## HomegearBackend depth-parity

**Status**: backend abstraction + basic backend in place; depth-parity
deferred to a post-0.1.0 release.

The `internal/client/backends/homegear.go` backend speaks the same
XML-RPC surface as `CcuBackend` and works end-to-end for devices,
data points, and value writes. What is intentionally **not** ported
to 0.1.0:

- **Programs** — Homegear exposes its automation programs through a
  different JSON-RPC surface than the CCU's ReGa runtime; the
  per-program API surface, including the WS commands and REST
  routes, would need a Homegear-flavoured ReGa adapter.
- **Rooms / Functions** — the CCU's `Subsection.getAll` + `Room.getAll`
  JSON-RPC methods are CCU-specific; the Homegear analogue is a
  per-device metadata field rather than a top-level catalogue, and
  the daemon's room/function model currently assumes the CCU shape.
- **Sysvar parity** — Homegear's sysvar surface diverges from the CCU
  on type coercion and persistence; the ad-hoc handling needs a
  proper adapter to avoid silent mis-renders in the SPA.

These are scoped against the existing surfaces in
`internal/central/adapter/hub*.go` and the REST routes under
`/api/v1/programs/...`, `/api/v1/rooms/...`, `/api/v1/functions/...`,
`/api/v1/sysvars/...`. A Homegear-backed installation runs today
with sensors + actors working and the upper four surfaces returning
empty results — acceptable for v0.1.0; not acceptable long-term.

## Upstream pin: openccu-data

**Status**: steady state
**Owner**: upstream ([openccu-data](https://github.com/SukramJ/openccu-data))

The CCU metadata archives under `internal/ccudata/embedded/` are
mirrored from [openccu-data](https://github.com/SukramJ/openccu-data).
`make update-ccu-data` performs a one-shot resync; there is no longer
a plan to reimplement the extractors inside OpenCCU-Loom (see
[ADR 0003](./adr/0003-embed-occu-extracts.md)).

Open hygiene items:

- Periodically re-sync after openccu-data tags a new release.
- Consider promoting the pin to a GitHub-release artifact (e.g.
  `openccu-data-go-<ver>.tar.gz`) once a second Go consumer needs
  the archives.

## Optional: Go wrapper module for openccu-data

**Status**: not started
**Priority**: low
**Blocked by**: emergence of a second Go consumer (e.g. an externalised
`hmcli`, third-party tools).

### Context

Today OpenCCU-Loom vendors the openccu-data archives directly via
`go:embed`. If another Go project needs the same data, the right
answer is a lightweight companion module:

- `github.com/SukramJ/openccu-data-go` ships a single Go package with
  the `.json.gz` files under `go:embed`, mirroring the contents of
  openccu-data's `openccu_data/data/` tree.
- Released in sync with openccu-data's Python package (same version
  tag).

openccu-loom would then `go get` the module and drop its local embed
copy. Until that second consumer appears the overhead of a separate
repo is hard to justify.

### Deliverables (if/when we do it)

1. `openccu-data-go` repo under SukramJ with a CI pipeline that rebuilds
   the embed on every openccu-data release.
2. `openccu-loom` imports the module, removes the local mirror, drops
   `make update-ccu-data`.

## Dropped: native Go re-implementation of the CCU extractors

An earlier version of this roadmap proposed porting the Python
extractors to Go. That plan has been **cancelled** — openccu-data
is the single source of truth and covers the whole ecosystem. The
port would duplicate ~3500 LoC of curated heuristics with no
incremental benefit. See ADR 0003 for the reasoning.
