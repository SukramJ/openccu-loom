<!-- Written 2026-11-09 by the owner's session; §7 holds the owner's
     decisions of 2026-11-09. Model: Opus. -->

# Brief — go-fabric v0.16.0 → v0.19.1 and go-fabric-ui v0.3.0 → v0.8.0 (OpenCCU-Loom 0.98.0)

Date 2026-11-09. Issues SukramJ/openccu-loom#1027, #1051. Repo
`~/Dokumente/GitHub/openccu-loom`, main at `9ce33aa3` (0.97.3), branch
`matter/gofabric-0.19.1`, worked in place. Read `CLAUDE.md` first — it is
long and binding (guards, `TestDocPurity`, the add-on config versions, the
release rules at `:505-560`); then this brief. Pins: go-fabric **v0.19.1**
(`~/Dokumente/GitHub/go-fabric` at `be663f27`: `CHANGELOG.md` 0.17.0 through
0.19.1, `docs/guides/migrating-to-the-facade.md` — §"Removed or changed in
the next release: the mDNS responder" (`:86`), §"Changed after v0.17.0: …"
(`:324-387`), §"Changed after v0.18.0: …" (`:250-323`)), go-fabric-ui
**v0.8.0** (`~/Dokumente/GitHub/go-fabric-ui`, `changelog.md` 0.4.0 … 0.8.0,
`fabricui/options.go`). Version **0.98.0** (CHANGELOG section, both
`packaging/ha-addon/*/config.yaml`, whatever else `grep -rn "0.97.3"` finds
outside goldens — and the goldens that carry `sw_version`, regenerated the
way the repo regenerates them). English; no attribution lines.

## 1. go-fabric v0.19.1

The probe build (owner's session) showed two breaks: `*Store` of
`internal/store/matterendpoint` lacks `EraseEndpoints` (#1051 — rows and
counter back to the first bridged number in one transaction, plus the fake
store in `internal/north/matteradapter/fake_store_test.go`, plus the
assign/erase/assign test the issue names) and
`internal/netutil/gofabric_parity_test.go:69` `mdns.NewZeroconf` is gone
(route per the migration guide §mDNS responder; the oracle is now
`mdns.DefaultInterfaceFilter` per the 0.19.0 CHANGELOG "node, mdns" item —
confirm in the guide). Then read every "Changed after v0.17.0/v0.18.0"
section and the 0.17.0/0.18.0 `### Changed` **Break** lines against loom's
imports (`grep -rhn "SukramJ/go-fabric/[a-z/]*" -o --include=*.go . | sort -u`)
— the attestation checks, CASE options, TCP limit, CATs, `DatastoreSync`,
the BasicInformation restores (`SetNodeLabel(string)` → `RestoreNodeLabel`
if loom calls it), the exchange notifier. `go vet ./...` and the test suite
decide. #1027: the owner's grep found none of its removed symbols left;
verify its acceptance list (`gh issue view -R SukramJ/openccu-loom 1027`)
and state each item's state so the session can close it.

## 2. go-fabric-ui v0.8.0

Loom wires `fabricui.Options{Node, Exposure, …}` in
`cmd/openccu-loom/daemon_matter.go:957`. Bump, compile, read the 0.4.0 …
0.8.0 changelog for options that fit what loom already has: `Warnings`
(loom's own findings about its Matter configuration, if a list exists),
`Events` (an exposure change made outside the page), `Erase` + `NodeSwitch`
(only if loom has a factory-reset path today), `Admin`, `Operations`
(version, uptime, log ring — if loom has a log ring the Operation page can
read). Adopt what has data today; list the rest in the hand-back as
follow-up candidates, not as issues (the session files them). The embedded
UI's assets: whatever the repo's `make` does for the bundle.

## 3. New go-fabric features (owner decision: adopt)

- `node.MDNS.Interfaces`: a loom configuration option for the Matter
  node's mDNS interface allow-list (follow loom's config conventions and
  its documented add-on options; empty = every interface), validated;
  `Node.DiagnoseMDNS`'s findings for a misspelt name surfaced where loom
  surfaces mDNS diagnostics today. Document it where the Matter node's
  options are documented.
- `Node.SetNodeLabel(ctx, label)`: if loom changes the Matter node's label
  at run time anywhere (a settings write, the UI), route it there so
  controllers see the rename without a restart; if loom only sets the
  label at start, say so and leave it.
- `Node.FactoryReset` numbers endpoints from the first bridged number
  again: if loom's documents state the old behaviour, correct them.

## 4. Documents

`CHANGELOG.md` `## [0.98.0] - 2026-11-09`: `### Changed` (both bumps,
one sentence each on what they carry), `### Added` (the mDNS option),
`### Fixed` if any. The add-on docs for the new option. `SPECIFICATION.md`
only if a resolved decision changes.

## 5. Guards

Loom's Makefile targets as `CLAUDE.md` names them (`make ci` or its
equivalents, lint, the Matter-side tests, the doc-purity test, the release
checks the CLAUDE.md lists before a release — including the comment-claims
sweep if it is a make target or a documented command; if it is a
delegated-agent procedure, say so and leave it to the session).
`export PATH=$PATH:$HOME/go/bin`. Background with summary logs.

## 6. Hand-back

Commits on the branch, no push/PR/tag. Report in the fixed shape: per
section the commits, guards with HEAD, #1027 acceptance item by item,
#1051, differences, decisions, left out, follow-up candidates, questions.

## 7. Owner decisions 2026-11-09

1. Loom takes the bump and the new features (mDNS allow-list, live rename
   where a run-time label change exists), not the bump alone.
2. #1027 and #1051 close with this wave when the acceptance holds.
