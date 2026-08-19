# notes — engineering working documents

This tree holds the documents the project works *from*: audits, design
concepts, parity fixtures, plans, and contributor procedures. None of it is
published.

The published documentation lives in [`docs/`](../docs/) and is built by
MkDocs into <https://sukramj.github.io/openccu-loom/>. The boundary is the
directory, not a list — **everything under `docs/` is published, and nothing
under `notes/` ever is.**

That rule is enforced, not merely stated:

| Guard | Where | What it catches |
|---|---|---|
| `mkdocs build --strict` | `.github/workflows/docs.yml` | a page under `docs/` with no nav entry; any link that leaves `docs_dir` |
| `TestEveryPublishedDocIsInTheNav` | `tests/contract/published_docs_test.go` | the same nav gap, locally in `make test` |
| `TestPublishedDocsLinksStayInsideDocsDir` | `tests/contract/published_docs_test.go` | a published page linking into `notes/` or the repo root |
| `TestMarkdownLinksValid` | `tests/contract/markdown_links_test.go` | any broken Markdown link, in either tree |

The split previously lived in an `exclude_docs` list in `mkdocs.yml`. It
rotted the way allowlists do: new pages were never added to it, and published
pages accumulated links into excluded trees — links that resolve on disk and
404 on the site.

## Which tree does my document belong in?

Ask who reads it.

- **An operator, administrator, or integrator** — someone using OpenCCU-Loom
  rather than changing it. That is `docs/`, and it needs a nav entry in
  `mkdocs.yml`.
- **A contributor or an AI agent changing the code** — a design rationale, an
  audit finding, a test brief, a fixture. That is `notes/`.

Two clarifications, because both have been got wrong before:

- **ADRs are published** (`docs/adr/`). They are durable, immutable once
  landed, and the rationale behind the code is legitimate developer
  documentation. Working documents *about* a decision are not — those are
  notes.
- **A working document does not become user documentation by being linked
  from one.** When a published page needs to cite a note, link it as an
  absolute repo URL:

  ```markdown
  [`by_design.md`](https://github.com/SukramJ/openccu-loom/blob/main/notes/parity/by_design.md)
  ```

  That resolves both on the site and when the file is read on GitHub.

## Layout

| Directory | Contents |
|---|---|
| [`audits/`](./audits/) | Subsystem deep-audit findings and the architecture analyses that back ADRs 0029–0039. |
| [`concepts/`](./concepts/) | Feature design records — alarm, Security & Safety, Matter UI, the SPA tile/widget concepts under `concepts/ui/`. |
| [`contributor/`](./contributor/) | Procedures a contributor runs: debugging setup, Matter smoke and chip-tool briefs, the SPA-E2E-against-godevccu harness — plus the long-form reasoning behind the compressed rules in `CLAUDE.md` ([`engineering-rules.md`](./contributor/engineering-rules.md), [`subagent-delegation.md`](./contributor/subagent-delegation.md)). |
| [`parity/`](./parity/) | The divergence catalogue `by_design.md`, the cross-stack snapshot schemas, the reachability baselines, and the matter.js parity fixtures + generators under `parity/matter/`. |
| [`plans/`](./plans/) | [`roadmap.md`](./plans/roadmap.md) — the canonical forward-looking plan — plus per-item implementation plans. |
| [`reference/`](./reference/) | Durable lookup material that is not a plan and not a concept: the CCU jpages wire contract, the CONTROL inventory, researched alarm assumptions. |
| [`testplans/`](./testplans/) | The E2E test plan and the historical test-migration record. |
| [`doc-backlog.md`](./doc-backlog.md) | Gaps in the *published* documentation. |

Several files here are consumed by tooling, not only by readers —
`parity/matter/matter-schema-snapshot.json` feeds
`make generate-matter-schema`, and `parity/dead-code-*.json` are the
reachability ratchet's baselines. Moving anything under `parity/` means
updating the `Makefile` and `script/` alongside it.
