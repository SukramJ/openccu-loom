# CCU-WebUI-Gap Wave Runbook & Continuation Guide

Last updated: 2026-07-22

Operational guide for executing the CCU-WebUI-replacement waves from
[`ccu-webui-gap-analysis.md`](./ccu-webui-gap-analysis.md). This is the
portable, in-repo companion to that plan: it records **where the work
stands**, **how a wave is executed end-to-end**, the **CI traps** already
hit, and **how to resume on a different machine**.

Read this together with:
[gap analysis](./ccu-webui-gap-analysis.md) (the 101 decidable items +
§7 wave plan),
[groups wave status](./ccu-webui-groups-wave-status.md) (the active
Welle-2 thread, deferred),
[ADR 0055](./adr/0055-groups-jpages-proxy.md),
and the repo root `CLAUDE.md` / `SPECIFICATION.md`.

---

## 1. Where the work stands (2026-07-22)

Release **0.46.0 (unreleased)**. `internal/build/version.go` = `0.46.0`;
both `packaging/ha-addon/*/config.yaml` = `0.46.0`. REST/WS API version
in `internal/north/rest/handlers/info.go`.

**The tag `v0.46.0` is intentionally NOT set yet** — it is set only after
the last desired wave of this release train. `release.yml` checks the
add-on versions against the tag.

### Master decision doc

`docs/ccu-webui-gap-analysis.md` holds 101 decidable items with IDs
(K/PR/GR/G/V/O/SV/SY/D/E/W + ADRs A1–A4), each carrying
`**Entscheidung:** ` `umsetzen | offen | abgelehnt`. 51 `umsetzen`,
43 `offen`, 3 `abgelehnt` (E01/E03/E04). §7 is the wave plan.

**PR01–PR06 (Programmeditor) are explicitly left `offen` — do NOT
implement** without a fresh decision.

### Completed

- **Welle 1 — COMPLETE** (8 packages 1a–1h, PRs #380–#387, all merged,
  all titled "(release 0.46.0)"). Device-admin, sysvars, messages, CCU
  reboot, programs, links, central-links, diagnostics. Landed API up to
  2.41.0.
- **Welle 2 slice 1 — COMPLETE** (PR #388, merged, API **2.42.0**):
  - A3 architecture decision → [ADR 0055](./adr/0055-groups-jpages-proxy.md)
    (heating groups via CCU jpages proxy; session model verified).
  - GR01 read-only heating-group listing (REST `GET /api/v1/groups`,
    WS `groups.list`, `internal/model/group` parser, SPA view).

### Deferred / next

- **Welle 2 GR02–GR05 — DEFERRED** (decision 2026-07-22). Full
  reconnaissance + plan preserved in
  [groups wave status](./ccu-webui-groups-wave-status.md). Order:
  GR02 (XL configurator via jpages proxy) → GR03 (rename) → GR04
  (`setOperateGroupOnly`) → GR05 (assign on pairing).
- **Wellen 3–6** — gap analysis §7, only the `umsetzen`-marked items:
  - Welle 3: G01, G05, G06, G04, G03 (device workflows).
  - Welle 4: G08, G09, G11, G12, G14, G16 (device remainder).
  - Welle 5: V01, V02, V03 (links overview / role matching / link test).
  - Welle 6: SV03, SV07, SV10, W01, W02 (diagram defs, sysvar-usage,
    protocol toggle, week-profile gaps).

---

## 2. Wave-execution runbook (established, works)

Per wave, one PR (or a small set), titled with the release tag suffix
(currently `(release 0.46.0)`).

### Per-item loop

1. Branch (or a scratch git worktree) `claude/welle-<n>-<name>`.
2. For each item: implement the production code, then add tests
   (the test-heavy, mechanical work can be delegated to a Sonnet
   sub-agent per `CLAUDE.md`).
3. One commit per item: `git commit -s` (**DCO sign-off required**),
   **no `Co-Authored-By` trailer**, commit body ends with
   `Ref: docs/ccu-webui-gap-analysis.md <ID>`.
4. Stage with node_modules excluded:
   `git add -A -- ':(exclude)assets/ui/node_modules'`.

### Per-wave close-out

1. Rebase on `origin/main`. Auto-resolution policy:
   - CHANGELOGs → keep both.
   - `info.go` API-version line / openapi `version:` line → take theirs,
     then re-bump to the next free number.
   - `schema_digest_gen.go`, `types.generated.ts`, dead-code inventory →
     take theirs, then **regenerate** (see below).
   - **All other Go files → resolve MANUALLY.** Blind take-theirs has
     eaten imports/tests before.
2. Bump `APIVersion` (`info.go`) **and** openapi `version:` to the next
   free number — they must match (`TestOpenAPIInfoVersionMatchesAPIVersion`).
3. Regenerate, in order:
   - `go run ./script/generate_schema_digest.go`
   - `cd assets/ui && npm run gen:types`
   - `go run ./script/reachability` (or `make reachability`) — commit the
     refreshed `docs/parity/dead-code-*.{json,md}` baseline (the contract
     test only checks format, not head==HEAD, so committing is safe).
4. Update `CHANGELOG.md` **and** the HA add-on changelog(s)
   (`packaging/ha-addon/openccu-loom/CHANGELOG.md`; the remote add-on
   rides along).
5. Build + targeted tests + lint (see §4), push, `gh pr create` with the
   release suffix in the title.
6. Arm a persistent monitor on `gh pr checks <n>` + merge state; fix CI
   failures autonomously. User merges (or approves per PR).

### New-view (SPA) requirements

Every new SPA view needs: both locales in `assets/ui/src/lib/i18n.ts`,
theme-aware styling, shared design-system components
(`LoadingState`/`EmptyState`/`ErrorState`/`Card`/`Badge`/`Button`/…),
and **light + dark Playwright visual baselines**. Config-schema fields
additionally need `config.field.*` + `config.help.*` in both locales
(guarded by `TestConfigFieldsHaveLabelsAndHelp`).

Playwright runs **only** via the CI Docker image locally. Recipe that
works:

```sh
docker run --rm --user $(id -u):$(id -g) -e HOME=/tmp \
  -v "$PWD":/work -w /work/assets/ui \
  mcr.microsoft.com/playwright:v1.61.0-noble \
  npx playwright test --update-snapshots -g "<ViewName>"
```

Scope with `-g` so only the new view's baselines are (re)written. Commit
the `*-chromium-linux.png` files; macOS `-darwin` baselines coexist for
local mac runs.

---

## 3. CI traps (all previously hit)

- **oasdiff guard** allows **only additive** openapi changes (no removed
  response status, no body field promoted to `required`).
- New JSON-RPC methods must be registered in
  `tests/contract/wire_methods_canonical_test.go`.
- New WS command categories must be added to `knownWSCategories` in
  `tests/contract/wsapi_schema_test.go`; the command itself goes in
  `assets/wsapi.json` and (if read-only) the `readOnlyCommands` allowlist
  in `internal/north/rest/ws/commands.go`.
- ReGa- or jpages-dependent WS commands that godevccu cannot serve must be
  listed in `tests/e2e/wsapi_skip.txt` (godevccu implements neither the
  ReGa `runScript` surface nor jpages).
- `RegisterDefaultCommands` is at the `funlen` limit — register new
  commands via the `register*` helpers or in `RegisterExtendedCommands`.
- ReGa-script count is pinned in `pkg/hmenum/rega_script_test.go`.
- npm-audit advisories: `npm audit fix --package-lock-only`.
- The pre-commit hook runs go vet/build/test/contract and **times out
  under cold cache**. Run the checks independently first, then commit
  once with `git commit --no-verify`.
- golangci cache can be poisoned after a worktree is deleted →
  `golangci-lint cache clean`.
- Local vitest can flake under parallel load (timeouts) — retry in
  isolation; CI is the arbiter.

### Known flaky CI tests (rerun, don't debug)

- rpcserver port-range test (Windows), hmcli httptest, central
  health-heartbeat. See the wider flaky list if one recurs.

---

## 4. Local verification chain

```sh
go build ./...
go vet ./<touched>/...
gofumpt -l <touched files>            # must print nothing
go test ./<touched>/... ./tests/contract/...
~/go/bin/golangci-lint run ./<touched>/...   # ~3 min cold; local at GOPATH/bin
# SPA:
cd assets/ui && npx svelte-check --tsconfig ./tsconfig.json
cd assets/ui && npx vitest run src/routes/<View>.test.ts
```

---

## 5. Resuming on another device

1. `git fetch && git checkout main && git pull` — `main` carries all
   merged work (Welle 1, Welle 2 GR01).
2. Check out this branch (or read these docs on `main` once this PR is
   merged): `docs/ccu-webui-wave-runbook.md` (this file),
   `docs/ccu-webui-groups-wave-status.md`,
   `docs/ccu-webui-gap-analysis.md`, `docs/adr/0055-groups-jpages-proxy.md`.
3. The Python/Matter reference repos are sibling checkouts under `../`
   (`../occu`, `../OpenCCU`, `../aiohomematic*`, `../matter.js`,
   `../godevccu`). On a new device these must be cloned alongside the
   repo for the reconnaissance/reference workflows; the CCU firmware
   source (`../occu`, `../OpenCCU`) is needed for the jpages/groups work
   specifically.
4. The developer's live CCU is at `172.18.4.29`. **Reads are free;
   writes need explicit approval and a named target device/group**
   (see `CLAUDE.md` → Critical Rules). godevccu (`../godevccu`) is the
   hermetic in-process simulator for everything that does not need real
   wire validation — but it does **not** implement `CCU.getHeatingGroupList`
   or any jpages surface, so the group write path (GR02+) has no
   simulator and needs a live, approved test.
5. Next actionable step: **GR02** — see
   [groups wave status](./ccu-webui-groups-wave-status.md) §2 for the
   verified jpages endpoints, request/response shapes, the encoding
   gotcha, the implementation plan, and the open decisions.

### Release close-out (when the last wave lands)

Set the git tag `vX.Y.Z` after the final wave. Before tagging, confirm
`internal/build/version.go`, both `packaging/ha-addon/*/config.yaml`, and
the changelogs (root + both add-ons) all carry the release version;
`release.yml` guards the add-on versions against the tag.
