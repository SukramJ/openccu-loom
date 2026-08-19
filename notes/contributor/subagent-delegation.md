# Sub-agent delegation — the long form

The root [`CLAUDE.md`](../../CLAUDE.md) carries the decision rule, the
agent table, and the fan-out formula. This document keeps the reasoning
behind the numbers and the full briefing and verification contracts.

### What may be delegated: the verifiability test

Hand a task to a sub-agent when the result has a **machine acceptance
criterion** — a test, a lint run, a guard that bites. Keep it when the
acceptance criterion is judgement, because a report cannot carry
judgement back.

Delegate freely (a gate proves the result):

- table cases, fakes, race / parallel scaffolding, vitest cases
- `config.field.*` / `config.help.*` catalogue work
  (`TestConfigFieldsHaveLabelsAndHelp` bites)
- doc-purity and markdown-link cleanups
- a version bump across `internal/build/version.go`, the root
  `CHANGELOG.md`, and both add-on `CHANGELOG.md` / `config.yaml` pairs
- inventories, grep sweeps, cross-file consistency checks

Never delegate (only a reader can accept the result):

- the daemon composition root and any new wiring seam
- `assets/openapi.yaml` / `assets/wsapi.json` semantics and `pkg/hmapi` DTOs
- Matter cluster IDs, revisions, attribute IDs, constraints, defaults —
  these are mirrored from matter.js by hand, with the citation
- auth, session, and secret handling
- *which* guard gets built, and what its bite line is

Locating is delegable; reading is not. Hand out "which files touch Y",
then read the three to five files the decision actually hangs on
yourself. An architecture call made from a sub-agent's summary is the
same defect as a doc claim made without checking the source.

### The four agents

Defined in `.claude/agents/`, so the model follows from the agent rather
than from a per-call decision:

| Agent | Model | Use for |
|---|---|---|
| `impl` | Sonnet | scoped implementation with a stated acceptance command |
| `guard` | Sonnet | tests from a caller-written guard spec, plus the bite proof |
| `sweep` | Haiku | read-only inventories and grep sweeps, high fan-out |
| `hunt` | Fable | adversarial read-only defect hunt, returns ranked candidates |

### Parallelism is bounded by the machine, and the machine is measured

The target is throughput, not agent count. A fan-out that pushes the
host past saturation finishes *later* than a smaller one, and it drags
every other build in the session down with it — including the main
conversation's. So the numbers below are derived at fan-out time and
never hard-coded: this project is worked on from a 4-core box and a
14-core box, and the same constant cannot be right on both.

**Read-only agents** (`sweep`, `hunt`, any search that never builds)
cost no core worth counting. Their real limit is how many reports you
can read and reconcile; 6–8 is a practical ceiling.

**CPU-bound agents** (anything running `go build`, `go test`,
`golangci-lint`, `vite`, `playwright`) each try to take the *whole*
machine by default — Go derives `-p` and `GOMAXPROCS` from the core
count, golangci-lint and vitest size their worker pools the same way.
Three unconstrained agents are a threefold oversubscription, not
threefold throughput. Derive the budget instead:

```sh
cores=$(nproc 2>/dev/null || sysctl -n hw.ncpu)
# share  = cores one CPU-bound agent may use (1 minimum; 2-3 keeps its
#          own latency sane on a big host)
# agents = concurrent CPU-bound agents, leaving one core for the main
#          conversation
agents=$(( (cores - 1) / share ))
```

Then give every CPU-bound agent its share **and** require it to pin the
share in the command it runs: `GOMAXPROCS=<share> go test -p <share>
./internal/x/...`, `golangci-lint run --concurrency=<share>`,
`vitest --maxWorkers=<share>`. Handing out a number without pinning it
changes nothing — the tools ignore the intent and read the core count.

| cores | share | concurrent CPU-bound agents |
|---|---|---|
| 4 | 1 | 3 |
| 14 | 2 | 6 |
| 14 | 3 | 4 |

Two adjustments on top:

- **Look at the load before fanning out, not only at the core count.**
  A 14-core host already running a Docker build has no free capacity.
  If the 1-minute load average is at or above the core count, drop to a
  single CPU-bound agent and queue the rest.
- **Eight concurrent agents is a practical ceiling** whatever the core
  count — a chosen bound, not a measured one: past it, briefing and
  reading reports cost more than the parallelism returns.

Two rules follow:

1. **No sub-agent runs `make test` or a repo-wide lint.** Each gets one
   narrow command (`go test ./internal/central/...`). The full gate runs
   **once**, in the main conversation, at the end of the slice.
2. **No two writing agents in the same package.** Give each a disjoint
   file set, or run them with `isolation: "worktree"`. Overlapping
   writers overwrite each other silently.

### The briefing contract

A delegated task carries five things. Without them the result is a
lottery:

1. the files and the public surface, plus one existing file as the style
   reference
2. the acceptance command the agent runs itself, reporting its output
3. the invariants from this document that the task touches — the
   relevant ones, not all of them
4. **stop conditions**: new dependency, DTO or API change, Matter
   constant, composition-root wiring, ADR-shaped decision. The agent
   reports these; it does not decide them. A stop is a successful
   outcome.
5. a report format, ≤ 250 words. Large artefacts go to the scratchpad
   directory and come back as a path.

### Verification: read the diff, not the report

A sub-agent report is a claim. Accept it the way any other claim in this
project is accepted — against the source. The main conversation reads
the diff and runs the gate once itself.

For `guard`, the acceptance artefact is not a green test; it is the
**bite proof**: the named production line removed, the test observed
red with its failure message, the line restored, green again. A test
delivered without that proof is not delivered. Sonnet reliably writes
bracketing tests when asked for "tests for X" — the guard specification
and the bite proof exist to make that failure mode visible instead of
green. See §[A test that constructs the collaboration proves nothing about the
wiring](./engineering-rules.md#a-test-that-constructs-the-collaboration-proves-nothing-about-the-wiring).

---

