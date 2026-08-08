# ADR 0038 — Enforce the cross-stack model-snapshot gate in nightly CI

- **Status**: accepted
- **Date**: 2026-06-15
- **Related**:
  `.github/workflows/cross-stack-parity.yml`,
  `.github/workflows/integration.yml`,
  `Makefile` (`datasource-diff`, `snapshot-go`, `snapshot-py`,
  `snapshot-diff`), `script/model_snapshot_diff.py`,
  `script/model_snapshot_drift_check.py`,
  the analysis item Area 9 [W6]/[P2] in
  `notes/audits/architecture-analysis-2026-06-15.md`

## Context

CLAUDE.md names the four-step cross-stack snapshot pipeline as the
release gate: `datasource-diff` (pydevccu vs godevccu wire data) →
`snapshot-go` (openccu-loom model vs godevccu) → `snapshot-py`
(aiohomematic model vs pydevccu) → `snapshot-diff` (per-field diff,
baseline-checked). The analysis (Area 9 [W6]) observed it is only
half-enforced: `integration.yml` runs steps 1–2 but **skips 3–4** with an
inline note — *"the aiohomematic + pydevccu + openccu_data Python stack
is not provisioned on the runner."* So the ~270-drift baseline could
regress undetected between manual local runs.

The sibling reference projects are **public** (aiohomematic and pydevccu
on PyPI, openccu-data as a `pyproject.toml` package), so the stack *can*
be provisioned in CI — the gap was provisioning effort, not feasibility.

## Decision

Add `.github/workflows/cross-stack-parity.yml`: a **nightly +
`workflow_dispatch`** job (not on pull requests) that provisions the
Python reference stack into a venv, exposes it as the `python3` the
Makefile targets invoke (and via `AIOHOMEMATIC_VENV_PYTHON` for
`aiohomematic_snapshot.py`'s re-exec), and runs all four steps. Step 4
(`snapshot-diff` → `model_snapshot_drift_check.py`) owns pass/fail
against the accepted-drift baseline, so the workflow does not re-encode
the threshold — it just runs the documented gate.

**Nightly, not per-PR, is deliberate.** The job clones three repos, sets
up a venv, and dumps ~80k + ~8k data points — minutes of work — and its
correctness depends on the reference-package versions, which drift
independently of any single PR. Running it nightly enforces the baseline
continuously without adding that cost (or that external-version coupling)
to every PR, and a failure is a visible nightly signal rather than a
merge blocker.

## Validation caveat (explicit)

A GitHub Actions workflow cannot be executed locally, and the full
pipeline needs the provisioned Python stack plus the ~60 MB Go snapshot.
This workflow is therefore authored against the documented Makefile
targets and script env-overrides, but its **first real run** (trigger it
via `workflow_dispatch`) is what validates the provisioning and may
require one calibration pass:

- **Reference version pinning.** The provision step installs
  aiohomematic/pydevccu from PyPI latest and openccu-data from `main`.
  If the latest reference version diverges from the version the Go port
  embeds, step 4 will report version-skew drift; the fix is to pin the
  versions in the workflow (commented at the install step) to match the
  embedded data. Because the job is nightly, this calibration does not
  block anything.

## Alternatives considered

- **Run steps 3–4 on every PR.** Rejected — heavy (three clones + venv +
  full snapshot) and couples PR CI to external package versions; the
  drift it guards against accrues over time, which a nightly cadence
  catches.
- **Keep it local-only (status quo).** Rejected — that is exactly the
  unenforced-gate gap the analysis flagged; the baseline can regress
  silently.
- **Vendor the reference snapshot JSON into the repo.** Rejected — the
  ~70 MB snapshots are gitignored by design (produced on demand); a
  stale vendored copy would mask real drift.

## Consequences

- The cross-stack release gate is enforced nightly, closing the Area 9
  [W6] gap; `integration.yml`'s steps 1–2 stay as the fast per-PR signal.
- The reference-stack version pinning is a documented calibration knob;
  the first `workflow_dispatch` run validates provisioning end-to-end.
- A drift regression beyond the accepted baseline now fails a visible
  nightly job instead of going unnoticed until a manual local run.
