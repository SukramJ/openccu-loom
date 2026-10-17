# Release process

How an OpenCCU-Loom release is cut: where the version is carried, which changelogs move with it, what runs before the tag, and what the release workflow does after it.

!!! info "Who this page is for"
    Maintainers cutting a release. The operational runbook the maintainers' tooling follows is [`.claude/skills/release/SKILL.md`](https://github.com/SukramJ/openccu-loom/blob/main/.claude/skills/release/SKILL.md); this page describes the same process for a human reader.

## 1. Decide the number

Read the unreleased section of [`CHANGELOG.md`](https://github.com/SukramJ/openccu-loom/blob/main/CHANGELOG.md) and the commits since the last tag:

```sh
git log --oneline $(git describe --abbrev=0 --tags)..main
```

Patch for fixes, minor for features. The REST `APIVersion` in `internal/north/rest/handlers/info.go` is independent of the daemon version: it moves only when the north-bound contract changes, on its own SemVer.

## 2. Bump the version carriers

Three files carry the daemon version and move together, in one commit:

| File | Shape |
| --- | --- |
| `internal/build/version.go` | `Version = "X.Y.Z"` |
| `packaging/ha-addon/openccu-loom/config.yaml` | `version: "X.Y.Z"` |
| `packaging/ha-addon/openccu-loom-remote/config.yaml` | `version: "X.Y.Z"` |

```sh
grep -rn 'X\.Y\.Z' internal/build/version.go packaging/ha-addon/*/config.yaml
```

The add-on `config.yaml` version is the add-on image tag. `release.yml` compares both against the tag and fails the add-on build when one differs — by then the tag is already pushed.

The go-fabric module is **not** a version carrier; see [Taking a go-fabric release](matter-and-go-fabric.md#taking-a-go-fabric-release).

## 3. Update the three changelogs

- `CHANGELOG.md` — the curated section. `release.yml` lifts the `## [X.Y.Z]` section into the GitHub release body and fails when it is missing.
- `packaging/ha-addon/openccu-loom/CHANGELOG.md`
- `packaging/ha-addon/openccu-loom-remote/CHANGELOG.md`

The two add-on changelogs are what Home Assistant shows operators in the add-on store. Describe the change for an operator, not the pull request.

## 4. Comment-claims sweep

Before every release, run the comment-claims sweep ([`.claude/skills/comment-claims-sweep/SKILL.md`](https://github.com/SukramJ/openccu-loom/blob/main/.claude/skills/comment-claims-sweep/SKILL.md)): read-only checks of code comments that name a consumer or caller, "not wired yet" notes, file-header inventories and ratchet justifications against the code. The mechanical guards cover only declared-silent events and ratchet texts; other prose is caught by this sweep alone. Fix or reword every refuted claim before tagging.

## 5. Gates and pinned artefacts

```sh
make lint && make test && make contract
```

The MQTT discovery golden for the security and combined entities carries the build version (`sw_version`) and fails on every bump. Refresh it and check that only the version moved:

```sh
go test -run TestSecurityAndCombinedDiscoveryPayloadsArePinned ./internal/north/mqtt/ -update-security-combined-golden
git diff internal/north/mqtt/testdata/ | grep '^[-+] '
```

The release runbook also refreshes the wire-fixture corpus under `assets/wire-fixtures/`:

```sh
go test -run TestWireFixtureCorpusMatchesHandlers ./tests/contract/ -update-wire-fixtures
```

Its `/info` fixture masks `version` and `api_version` as volatile, so a pure version bump should leave that corpus unchanged; a diff there means something other than the version moved.

The wider pre-release checklist is on the [Testing](testing.md#pre-release-checklist) page.

## 6. Pull request, merge, tag

Commit with a sign-off (`git commit -s`), open the pull request, let CI go green and merge. Then tag the merge commit on `main` with an annotated tag and push it:

```sh
git tag -a vX.Y.Z <merge-commit> -m "OpenCCU-Loom X.Y.Z"
git push origin vX.Y.Z
```

Never tag before both add-on versions match.

## 7. What happens after the tag

Pushing a `v*` tag runs [`.github/workflows/release.yml`](https://github.com/SukramJ/openccu-loom/blob/main/.github/workflows/release.yml):

- **goreleaser** — builds the SPA bundle and the SBOM first, then the binaries and the multi-arch images, and publishes the GitHub release with the changelog section as its body.
- **Home Assistant add-ons** — after the version check above, builds and publishes both add-on images.
- **Client regeneration** — the `notify-client-repo` job dispatches a `daemon-release` event to [openccu-loom-client](https://github.com/SukramJ/openccu-loom-client). It marks the release as breaking when the `APIVersion` major is higher than at the previous tag. The receiving workflow regenerates the wire bindings and opens a pull request there; it does not bump, merge or tag. Whether the hand-written layer of the client must follow is a separate decision — see that repository's [`daemon-update` skill](https://github.com/SukramJ/openccu-loom-client/blob/main/.claude/skills/daemon-update/SKILL.md). Without the `TYPES_REPO_DISPATCH_TOKEN` secret the job warns and skips the dispatch.
- **Node-RED** — not automated. [node-red-contrib-openccu-loom](https://github.com/SukramJ/node-red-contrib-openccu-loom) pins `SUPPORTED_API_MAJOR` in `lib/client.js`; after an `APIVersion` major bump every server node warns until it is raised there.

The release is finished when those follow-ups are handled, not at the tag.
