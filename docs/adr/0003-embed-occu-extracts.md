# ADR 0003 — Embed openccu-data metadata artifacts

- **Status**: accepted (revised 2026-04-24)
- **Date**: 2026-04-24
- **Supersedes**: original revision from 2026-04-23 (wrongly assumed
  GPL-2.0-or-later for the upstream data)
- **Related**: the `NOTICE.md` of the `github.com/SukramJ/go-openccu-data`
  module (see [ADR 0053](./0053-go-openccu-data-module.md)),
  `notes/plans/roadmap.md`,
  [ADR 0001 — License: MIT](./0001-license-mit.md)

## Context

The UI and REST layers need rich CCU metadata to render useful views:

- **Translations** — localised labels for device models, channel types,
  parameters, value lists, help texts, and icons.
- **Easymode metadata** — per-channel parameter groupings, parameter
  order, option presets, and cross-validation rules.
- **Receiver profiles** — per-receiver-type paramset constraints and
  localised profile names.

The operator must not need to know any of this on day one. Running
`openccu-loom run --config config.yaml` should yield a usable UI
without a manual extraction step.

## Decision

Ship the archives produced by
[openccu-data](https://github.com/SukramJ/openccu-data) **embedded in
the binary** via `go:embed`. openccu-data is the authoritative
extractor and is also consumed by aiohomematic + aiohomematic-config,
so the whole ecosystem sees the same data.

File layout under `internal/ccudata/embedded/` *(as decided here; the data
later moved into a module of its own — see
[ADR 0053](./0053-go-openccu-data-module.md))*:

- `translation_extract.json.gz` — raw CCU stringtable
- `easymode_extract.json.gz` — TCL easymode
- `profiles/<RECEIVER>.json.gz` — per-receiver profile (≈ 65 files)
- `profiles/_receiver_type_aliases.json` — alias map
- `translation_custom/*.json` — curated translation overrides

Combined binary-size impact: ≈ 900 kB — negligible next to the Go
runtime and the SQLite driver.

The daemon's load order is:

1. Operator-supplied file path
   (`cfg.CCUData.{translations_path,easymode_path}`).
2. Embedded archive.
3. Empty fallback (raw CCU strings in the UI).

Every transition is logged at INFO
(`ccudata.translations.ok source=file|embedded`) so the operator can
tell at a glance which source is active.

## Licensing

The upstream legal situation has two layers:

### Layer 1 — MIT content

The curated files inside the bundle are authored by the openccu-data
maintainers and released under MIT:

- `profiles/_receiver_type_aliases.json`
- `translation_custom/*.json`

Matches OpenCCU-Loom's own source license (MIT, see ADR 0001) — no
additional concerns.

### Layer 2 — eQ-3 HomeMatic Software License

The remaining archives are derivative works of OCCU / OpenCCU /
RaspberryMatic source material. They inherit the upstream
**eQ-3 HomeMatic Software License** (LicenseDE.txt). Headline:

- Free for **private, non-commercial** use.
- Redistribution is permitted as long as the upstream notice travels
  along (the `go-openccu-data` module carries its `NOTICE.md`, and
  `THIRD-PARTY-NOTICES.md` reproduces the terms here).
- **Commercial redistribution requires written eQ-3 permission.**

### Aggregation model

The OpenCCU-Loom binary aggregates two separately-licensed works:

- Source + compiled code → MIT (liberal, no copyleft).
- Embedded CCU data → eQ-3 non-commercial.

Each license stands on its own. The project license file (`LICENSE`)
covers only the code; the embedded archives ship with their own
`NOTICE` that travels with every binary. The aggregation is
permissible because:

- MIT explicitly allows redistribution with additional terms, as long
  as the MIT notice is preserved for the MIT-covered portion.
- The eQ-3 license is preserved verbatim in `NOTICE`.
- The daemon's `/api/v1/info` endpoint and the UI About page surface
  both notices so commercial users cannot miss them.

Operators with commercial use-cases can override the embedded
archives via `cfg.CCUData.{translations_path,easymode_path}` and
supply their own eQ-3-licensed data.

## Alternatives considered

**A. Re-implement the extractors in Go.**
Rejected. Would duplicate ~3500 LoC of curated heuristics that
openccu-data already maintains for the whole aiohomematic ecosystem;
maintenance would fall out of sync on every OCCU release.

**B. Runtime download on first boot.**
Rejected. Adds a network dependency, breaks offline/air-gapped
installs, and hides the license transfer.

**C. Make operators supply the archives.**
Rejected as the default — friction too high. The `cfg.CCUData.*_path`
override preserves this as an opt-out.

**D. Separate Go module `openccu-data-go`.**
Not done yet. Would make sense if a second Go consumer emerges
(externalised `hmcli`, third-party tools). For one consumer the
current `go:embed` directly against an openccu-data checkout is
simpler.

## Consequences

- Binary grows by ≈ 900 kB. Accepted.
- Archive refresh is a single `make update-ccu-data` against a local
  openccu-data checkout.
- Commercial redistribution story is explicit: operators must
  override the embedded archives with their own licensed data.
- The originally planned "native Go extractor" (Phase 2 in the old
  roadmap) is dropped; see `notes/plans/roadmap.md`.
