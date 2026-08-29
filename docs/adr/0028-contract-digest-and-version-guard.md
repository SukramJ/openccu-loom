# ADR 0028 — Contract digest, version guard, and types-repo release coupling

- **Status**: accepted
- **Date**: 2026-06-11
- **Related**:
  [ADR 0020 — external-client wire contract](./0020-external-client-wire-contract.md),
  `assets/openapi.yaml`, `assets/wsapi.json`, `assets/schemas/`,
  the sister repos `openccu-loom-types-py` and `openccu-loom-client`

## Context

ADR 0020 gave external clients a contract-version axis
(`api_version`) and a capability set on `GET /api/v1/info`. Three
gaps remained in practice:

1. **No machine-checkable contract identity.** `api_version` is a
   hand-maintained constant. The real contract (openapi.yaml,
   wsapi.json, enums/types schemas) evolves with every build; nothing
   tied a running daemon to the exact contract state it was built
   from, so a client could not verify that its generated types match
   the daemon it talks to.
2. **No bump discipline.** Nothing forced an `api_version` bump when
   the contract assets changed; `1.0.0` had already accumulated
   additive field changes.
3. **Manual types regeneration.** `openccu-loom-types` is regenerated
   by hand from a local daemon checkout ("CI-rebuilt on every release"
   was aspirational), so the published types lag the daemon.

## Decision

### 1. Schema digest

A canonical digest identifies the contract state:

```
combined = "<path>\n<sha256-hex of file bytes>\n"  per asset,
           paths sorted lexicographically
digest   = "sha256:" + sha256-hex(combined)
```

over the closed asset list `assets/openapi.yaml`,
`assets/schemas/enums.json`, `assets/schemas/types.json`,
`assets/wsapi.json`. The definition is trivially reproducible in any
language.

`script/generate_schema_digest.go` (wired into `make export-schemas`)
writes the digest as the generated constant
`handlers.SchemaDigest`; `GET /api/v1/info` serves it as
`schema_digest`. The contract test `TestSchemaDigestFresh` recomputes
the digest from the repo assets, so a stale constant fails
`make test`. The types generator stamps the same digest into the
published package; a client compares the two values at connect time.
Equality means exact contract identity; inequality means "generated
from a different build" — clients fall back to `api_version` +
`capabilities` for compatibility reasoning (per ADR 0020).

The digest is a **generated constant**, not a runtime computation,
because the daemon deliberately reads `openapi.yaml` from disk (the
deployed spec location is configurable) — a runtime hash would
fingerprint whatever file happens to be deployed, not the contract
the binary was compiled against.

### 2. APIVersion bump guard

`script/check_api_version_bump.sh` (CI job `api-contract-guard`,
PR-only) fails a PR that changes any contract asset without
increasing `APIVersion`:

- any asset diff → `APIVersion` must increase (at least minor);
- oasdiff-classified breaking OpenAPI diff → the major version must
  increase;
- `TestOpenAPIInfoVersionMatchesAPIVersion` pins
  `openapi.yaml info.version` to `handlers.APIVersion` so the two
  declarations cannot diverge.

oasdiff (Apache-2.0) is pinned in the script and run via `go run`.
Diffs in wsapi.json / enums.json / types.json are not auto-classified;
any change there requires at least a minor bump and reviewer judgment.

Consequence: every PR window that touches the contract bumps the
version at least once; multiple contract PRs per release produce
multiple minor bumps. That is accepted — version numbers are cheap,
silent drift is not.

### 3. Release coupling to the types repo

The release workflow fires a `repository_dispatch` event
(`daemon-release`, payload: the released tag) at
`SukramJ/openccu-loom-types-py`. The receiving workflow checks out
the daemon at that tag, regenerates the Pydantic/enum modules, stamps
version + digest, and opens a PR in the types repo. The dispatch
needs the `TYPES_REPO_DISPATCH_TOKEN` secret (fine-grained PAT,
`contents:write` on the types repo); when absent the step warns and
the release proceeds — regeneration stays possible manually.

## Alternatives considered

- **Embed the assets and hash at runtime** — rejected; couples the
  served digest to deployed files instead of the built contract, and
  embeds ~0.5 MB of YAML the binary otherwise does not need.
- **Monorepo for daemon + types + client** — rejected in ADR 0020
  (asks.md C3); CI triggers couple versions, not the repo layout.
- **Hash only openapi.yaml** — rejected; enums.json drives the enum
  module and wsapi.json the WS command surface, so type drift can
  originate in all four files.

## Consequences

- Clients gain an exact, build-independent match check
  (`schema_digest`) and keep the semantic axis (`api_version`).
- Contract changes without a version bump no longer merge.
- Types releases follow daemon releases automatically once the
  dispatch token is configured.

## Amendment (2026-08-29) — the dispatch target moved

The generated bindings no longer ship as `openccu-loom-types`. They were
folded into `openccu-loom-client` as `openccu_loom_client/wire/`
([client #122](https://github.com/SukramJ/openccu-loom-client/issues/122)),
so this ADR's release-coupling section now reads: the `daemon-release`
dispatch fires at `SukramJ/openccu-loom-client`, whose workflow regenerates
`wire/` and opens a pull request. The secret name
(`TYPES_REPO_DISPATCH_TOKEN`) is unchanged.

The rest of the decision stands unchanged — the digest, the four hashed
assets, and the bump guard are unaffected by where the generated modules
live.

One consequence above is now wrong and stays on the record rather than
being edited: releases do **not** follow daemon releases automatically any
more. The receiving workflow opens a PR and stops, deliberately. Measured
over the automation's lifetime, 84 of 84 downstream releases were machine-
decided and none carried a hand-written line, which is what "automatically"
had come to mean.
