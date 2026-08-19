---
name: release
description: Cut an OpenCCU-Loom release — bump the version everywhere it is carried, update all three changelogs, run the pre-release sweep, tag. Use when the user asks to release, tag, or ship a version.
---

# Releasing OpenCCU-Loom

A release touches five version carriers. `release.yml` guards **both** add-on
`config.yaml` versions against the tag, so a missed one fails the workflow
after the tag is already pushed — bump them all in one commit.

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
