---
name: release
description: Cut an OpenCCU-Loom release — bump the version everywhere it is carried, update all three changelogs, run the pre-release sweep, tag. Use when the user asks to release, tag, or ship a version.
---

# Releasing OpenCCU-Loom

A release touches five version carriers. `release.yml` guards **both** add-on
`config.yaml` versions against the tag, so a missed one fails the workflow
after the tag is already pushed — bump them all in one commit.

`github.com/SukramJ/go-fabric` is **not** one of the carriers. It is a
dependency with its own cadence — see [Following go-fabric](#following-go-fabric)
at the end.

## 1. Decide the number

Read `CHANGELOG.md`'s unreleased section and the commits since the last tag
(`git log --oneline $(git describe --abbrev=0 --tags)..main`). Patch for
fixes, minor for features. The REST `APIVersion` in
`internal/north/rest/handlers/info.go` is **independent** — bump it only when
the north-bound contract changed, and follow its own semver.

## 2. Bump the five carriers

| File | Shape |
|---|---|
| `internal/build/version.go` | `Version = "X.Y.Z"` |
| `packaging/ha-addon/openccu-loom/config.yaml` | `version: "X.Y.Z"` |
| `packaging/ha-addon/openccu-loom-remote/config.yaml` | `version: "X.Y.Z"` |

Verify with:

```sh
grep -rn 'X\.Y\.Z' internal/build/version.go packaging/ha-addon/*/config.yaml
```

## 3. Update all three changelogs

- `CHANGELOG.md` — the curated section the release workflow lifts into the
  GitHub release body.
- `packaging/ha-addon/openccu-loom/CHANGELOG.md`
- `packaging/ha-addon/openccu-loom-remote/CHANGELOG.md`

The add-on changelogs are what Home Assistant shows operators in the add-on
store and the Update view. They are operator-facing: describe the change, not
the PR number.

## 4. Run the pre-release comment-claims sweep

Invoke the `comment-claims-sweep` skill. Fix or reword every refuted claim
**before** tagging.

## 5. Gate and tag

```sh
make lint && make test && make contract
```

Then commit (`git commit -s`), open the PR, let CI go green, merge, and tag
`vX.Y.Z` on the merge commit. Never tag before the add-on versions match.

## 6. The release is not finished at the tag

`release.yml` dispatches `daemon-release` to `openccu-loom-client`, which opens
a regeneration PR and stops — deliberately: no version bump, no auto-merge.
That PR moves generated types only. Whether the hand-written layer needs to
follow (a response that gained a field, a caller that caches what it wrote) is
a separate judgement, and nothing fails while it is skipped.

The runbook lives in that repo, as its `daemon-update` skill:
<https://github.com/SukramJ/openccu-loom-client/blob/main/.claude/skills/daemon-update/SKILL.md>

Check `node-red-contrib-openccu-loom` too. It pins `SUPPORTED_API_MAJOR` in
`lib/client.js`; a major bump here makes every server node warn until it is
raised there.

## Following go-fabric

The Matter bridge stack lives in `github.com/SukramJ/go-fabric` and has an
independent SemVer lane — the same arrangement ADR 0050 set up for `go-mqtt`,
and the point of the extraction: a Matter fix reaches a consumer without a
daemon release.

**It is not a sixth version carrier.** Nothing in this repository states the
module's version except the `require` line in `go.mod`; there is no config
file, no changelog and no workflow guard tied to it. Bumping the require is
an ordinary dependency change and does not by itself oblige a loom release —
only the user-visible behaviour it brings does.

How a go-fabric change reaches loom:

1. Land the change in go-fabric and let its own CI go green
   (`.github/workflows/ci.yml` there: build, vet, race tests, lint).
2. Bump the require in loom and run the gate:

   ```sh
   go get github.com/SukramJ/go-fabric@<ref>   # <ref> is a tag or a commit SHA
   go mod tidy
   make lint && make test && make contract
   ```

3. If the bump changes user-visible behaviour, it gets a `CHANGELOG.md` entry
   here like any other change — describe the behaviour, not the module
   version.

**Tag or pseudo-version.** The module carries no tags yet, so every bump is a
pseudo-version. That is deliberate: the API has had one real caller, and a
`v0.x` tag would freeze port shapes designed against a single consumer. Take
a tag as soon as go-fabric publishes one — a tag is what makes the pin
readable and what lets Dependabot do the bump. Until then `@main` and
`@<commit-sha>` both work and both record the resolved pseudo-version in
`go.mod`; name the SHA when you need a specific commit rather than the tip.

There is no `go.work` and no filesystem `replace` in the committed tree — CI
checks out one repository and resolves the module from the proxy. A local
`go.work` is fine for development and must never reach a commit.

Dependabot's root `gomod` entry covers the module, but has nothing to propose
while it is untagged — see the comment in `.github/dependabot.yml`.
