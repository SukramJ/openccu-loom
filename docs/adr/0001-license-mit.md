# ADR 0001 — License: MIT

- **Status**: accepted
- **Date**: 2026-04-24
- **Related**: [ADR 0003 — Embed openccu-data metadata artifacts](./0003-embed-occu-extracts.md)

## Decision

The OpenCCU-Loom **source tree** is licensed under the MIT License
(see [`LICENSE`](https://github.com/SukramJ/openccu-loom/blob/main/LICENSE)).

The binary additionally ships CCU metadata archives under the
eQ-3 HomeMatic Software License (non-commercial). That aggregation
layer is a separate decision and is documented in ADR 0003; it does
**not** bind the project's source license.

## Rationale

The code's license is a pure-play permissive one because every
structural driver points there:

- **No GPL in the dependency graph.** All direct and indirect Go
  dependencies (`chi`, `kin-openapi`, `google/uuid`, `goose`,
  `golang.org/x/*`, `modernc/sqlite`, `yaml.v3`, …) are permissive
  (MIT / BSD-3 / Apache-2.0). There is no GPL dependency we have to
  be compatible with.
- **Ecosystem alignment.** The sibling projects maintained by the
  same author — aiohomematic, aiohomematic-config, openccu-data —
  are all MIT. Staying on MIT makes code, fragments, and test
  fixtures move across repo boundaries without licence friction.
- **Go-ecosystem convention.** MIT / Apache-2.0 is the default in the
  Go world. Copyleft daemons are rare and raise an eyebrow when
  there's no technical reason.
- **Downstream freedom.** MIT enables forks, embedding, and
  commercial reuse of the *code* without forcing derivative works
  open. The non-commercial restriction on the embedded CCU data (see
  ADR 0003) is orthogonal — it stands on its own and can be
  neutralised by overriding the embedded archives via
  `cfg.CCUData.*_path`.

## Alternatives considered

**Apache-2.0.** Functionally equivalent for redistribution. Adds an
explicit patent grant; trade-off against ecosystem-consistency with
the sibling MIT repos. We pick MIT for consistency and may revisit
if patents become a concern.

**GPL-3.0-or-later.** Was originally chosen to pair with a planned
fork of `mdzio/go-hmccu`. The fork never happened — the XML-RPC,
BIN-RPC and JSON-RPC transports live natively under
`internal/client/transport/` / `internal/central/rpcserver/`. A
fresh grep across `go.mod` + `go.sum` + the module tree confirms
there is no `go-hmccu` import. With no copyleft dependency pulling
us along, GPL-3 would have diverged from the sibling ecosystem
without cause.

**Dual-licensing (e.g. AGPL + Commercial).** Rejected — disproportionate
governance overhead for a solo project. Customers with a commercial
need go through the eQ-3 data override path instead.

## Copyright chain

Verified 2026-04-24 via `git log --format='%an <%ae>' | sort -u` on
the project root: a single contributor (SukramJ). No external
authors whose consent would be needed. Future PRs use `git commit -s`
so the DCO chain stays explicit as the contributor list grows.

## Implementation notes

- `LICENSE` carries the MIT text plus a short pointer to
  `internal/ccudata/embedded/NOTICE` for the eQ-3 data layer.
- Every Go, Markdown, YAML, HTML, and script file with an SPDX
  header uses `SPDX-License-Identifier: MIT`.
- `go.mod` and module-level docs reflect MIT.
- The `/api/v1/info` payload and the UI About page surface both
  licenses so users of commercial binaries see both.

## History

This ADR replaces an earlier GPL-3.0-or-later decision from project
inception. The historical rationale is preserved in the git log of
this file.
