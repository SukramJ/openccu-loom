# CLAUDE.md — Matter side of OpenCCU-Loom

This file is loaded when you touch `internal/north/matter/` (and the
related `bridge/`, `endpoint/`, `im/`, `tlv/`, `secure/` trees). The
repo-wide rules live in the root [`CLAUDE.md`](../../../CLAUDE.md).

## matter.js is the gold standard

> **Hard rule for everything under `internal/north/matter/`,
> `internal/north/matter/cluster/`, `internal/north/matter/bridge/`,
> `internal/north/matter/endpoint/`, `internal/north/matter/im/`,
> `internal/north/matter/tlv/`, `internal/north/matter/secure/` and
> any other Matter-side code:** the gold standard is
> [`matter.js`](https://github.com/project-chip/matter.js) HEAD.
> Apache-2.0 — MIT-compatible — and the certified, production-tested
> reference Matter stack. The platform-specific exceptions Apple
> Home / Google Home / Alexa apply have already been encoded into
> matter.js's behavior + protocol layers through real interop
> testing; we do not re-derive them, we mirror them.

| Repo | Local path | Role |
| --- | --- | --- |
| matter.js | `../matter.js/` | Matter Core implementation: schema (`packages/model`), wire codec (`packages/types`), behavior layer (`packages/node/src/behaviors`), device types (`packages/node/src/devices`), protocol engine (`packages/protocol`). The single Matter-side gold standard. |

[`home-assistant-matter-bridge`](https://github.com/Nabu-Casa/home-assistant-matter-bridge)
(Apache-2.0, local at `../home-assistant-matter-bridge/`) is one
specific consumer of matter.js. Useful as an occasional helper
reference for "how does a real bridge wire its Aggregator + bridged
devices end-to-end?", but **not** a gold standard — it carries
Home-Assistant-specific shims (Entity-Domain → Cluster mapping, HA
Device Registry as data source) that do not translate to
OpenCCU-Loom. When in doubt, pull the pattern from matter.js itself,
not from ha-bridge.

**Goal:** OpenCCU-Loom's Matter side is a 100 % port of matter.js —
**semantically**, not syntactically. TypeScript idioms
(decorators, `Behavior.with(...)` mixins, `Promise<T>`) translate to
Go idioms (struct-with-methods, `context.Context`, goroutines). The
same defaults, the same constraints, the same wire shape, the same
order of attributes / commands / events. Where the Go translation
forces a different surface, the Go code calls out the matter.js
function it mirrors in a comment + the contract it enforces.

### Workflow

1. **Before writing any Matter-side fix or feature, read the
   corresponding matter.js source.** Likely paths:
   - schema constant / cluster revision / attribute id →
     `../matter.js/packages/model/src/standard/elements/<name>.element.ts`
   - cluster behavior (defaults, mandatory attributes, conformance
     checks) → `../matter.js/packages/node/src/behaviors/<name>/`
   - device type (DeviceTypeList revision, mandatory cluster set) →
     `../matter.js/packages/node/src/devices/<name>.ts`
   - bridge composition pattern → `../matter.js/packages/node/src/devices/aggregator.ts`
     and `../matter.js/packages/node/src/devices/bridged-device.ts`
     (ha-bridge's `packages/backend/src/matter/` is a useful
     supplementary read but is not the gold standard)
   - wire codec, IM messages, sigma → `../matter.js/packages/types/src/tlv/`
     and `../matter.js/packages/protocol/src/`
2. **Cite the matter.js path + function in the Go code**
   (`// Mirrors matter.js packages/node/src/behaviors/.../FooBehavior.ts:bar`)
   so the provenance survives drift. PR descriptions quote it too.
3. **Every Matter-side change updates the parity tests** under
   `internal/north/matter/.../parity_matterjs_test.go`. The
   schema snapshot at
   `notes/parity/matter/matter-schema-snapshot.json` is the
   matter.js HEAD pin (regen via
   `notes/parity/matter/extract-from-matter-js.ts`); the wire-byte
   fixtures at `notes/parity/matter/tlv-wire-fixtures.json` lock the
   TlvCodec wire shape. New cluster-server tests add a parity case;
   PRs without parity coverage are rejected.
4. **Deliberate divergences are documented in
   `notes/parity/by_design.md` (matter.js section)** — and the same
   divergence on a non-trivial scale gets an ADR. Examples of valid
   divergences: a TypeScript-only optimisation that would fight Go's
   GC, a Decorator pattern that has no Go equivalent. Examples of
   invalid divergences: hand-coding cluster revisions, attribute IDs,
   constraint defaults, Apple-Home-required tag patterns — those go
   verbatim from matter.js.
5. **Behavioral-parity contract + standing guards.** Ongoing Matter
   parity is held by the build- and test-time guards catalogued in
   [`docs/matter-parity-contract.md`](../../../docs/matter-parity-contract.md)
   — schema parity tests, the behavioural negative-write parity table,
   wire-codec fixtures, wiring-capability pins, and the `by_design.md`
   divergence catalogue — not by periodically regenerated audit reports.
   Every Matter change reads matter.js / chip first, mirrors behaviour
   (not just schema), cites the source, and extends the relevant guard.

### Lockstep with aiohomematic

aiohomematic remains the gold standard for the **CCU side** —
transports, devices, paramsets, custom-DP composition. matter.js is
the gold standard for the **Matter side**. The two reference layers
do not overlap; CCU wire knowledge stays in aiohomematic, Matter wire
knowledge stays in matter.js. When a single bridge feature spans
both (e.g. a HmIP DataPoint surface that has to map onto a Matter
cluster) the boundary is the `internal/model/custom/<dp>/matter.go`
file — left side mirrors aiohomematic, right side mirrors matter.js.

---


## Regenerate Matter schema from matter.js HEAD

When matter.js HEAD ships cluster-revision or device-type-revision bumps,
update the codegen pipeline in one shot:

```sh
make generate-matter-schema
```

This runs four steps:
1. Extract the schema from the built matter.js checkout by running the
   TypeScript extractor `notes/parity/matter/extract-from-matter-js.ts`
   with `node` inside `../matter.js` (it is copied in so the
   `@matter/model` import resolves), writing
   `notes/parity/matter/matter-schema-snapshot.json`. (matter.js's
   `packages/model` must be built first — `npm run build`.)
2. Copy the snapshot to the parity embed at
   `internal/north/matter/parity/schema.json` (kept in sync with the
   snapshot; see `internal/north/matter/parity/parity.go`).
3. `go run ./script/generate_matter_schema.go` — reads the snapshot and
   regenerates `internal/north/matter/schema/clusters.go` and
   `internal/north/matter/schema/devicetypes.go`.
4. `gofumpt -w internal/north/matter/schema/` — formats the output.

After regeneration, run `go test ./internal/north/matter/schema/...` — the
`TestParityCodeMatchesGeneratedSchema` test will flag any cluster where the
hand-coded revision constant in the cluster source files has drifted from the
new schema. Update those constants to match, then re-run `make test`.

Callers that need a device-type revision at runtime should use
`schema.DeviceTypeRevision(id)` (from `internal/north/matter/schema`) rather
than hard-coding a switch — that way the next `make generate-matter-schema`
automatically propagates the update without requiring a second manual edit.

