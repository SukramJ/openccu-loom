<!--
Thanks for contributing to OpenCCU-Loom! Keep the title in
Conventional-Commit form: <type>(<scope>): <subject>
(scopes align with package boundaries — client, central, model, north,
store, rest, mqtt, ui, matter, docs, ci, …).
-->

## What & why

<!-- What does this change do, and why is it needed? Link issues with
"Closes #123" where applicable. -->

## How was it tested

<!-- Commands run (make test / contract / lint / integration), new
test cases, manual verification. -->

## Checklist

- [ ] Commits are signed off (`git commit -s` — DCO).
- [ ] `make lint && make test` pass locally.
- [ ] Added/updated a **contract test** if this touches a protocol,
      capability, or state machine (`tests/contract/`).
- [ ] No CGo introduced; the default build stays `CGO_ENABLED=0`.
- [ ] No GPL/LGPL/MPL/AGPL dependency added (`make licenses` clean).
- [ ] MIT SPDX header on every new `.go` file.
- [ ] `CHANGELOG.md` updated for user-visible changes.
- [ ] `SPECIFICATION.md` / a new ADR updated if a goal, non-goal, hard
      constraint, or resolved decision changed.

### Matter-side changes only

- [ ] Read the corresponding matter.js HEAD source first and cited the
      `path:function` in the Go comment.
- [ ] Added/updated the matter.js parity test; deliberate divergences
      recorded in `docs/parity/by_design.md`.

### Live-CCU writes only

- [ ] The write target device + channel was explicitly confirmed with
      the maintainer (reads are free; writes are not — see CLAUDE.md).
