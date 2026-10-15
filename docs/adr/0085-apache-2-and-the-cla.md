# ADR 0085 — Apache License 2.0 and a contributor licence agreement

- **Status**: accepted (2026-10-15)
- **Date**: 2026-10-15
- **Supersedes**: [ADR 0001 — License: MIT](./0001-license-mit.md)
- **Revisits**: [ADR 0066 — Relicense to AGPL-3.0-only](./0066-relicense-agpl-commercial-exception.md)
  (rejected 2026-08-28) and the working note
  [`notes/reference/contributor-agreements-declined.md`](https://github.com/SukramJ/openccu-loom/blob/main/notes/reference/contributor-agreements-declined.md)
  (DCO only, no CLA — August 2026)
- **Related**: [ADR 0003 — Embed openccu-data metadata artifacts](./0003-embed-occu-extracts.md),
  [ADR 0084 — the Matter profiles on go-fabric's device layer](./0084-matter-device-layer-and-node-facade.md),
  go-fabric [ADR 0023 — Apache License 2.0 and a contributor licence agreement](https://github.com/SukramJ/go-fabric/blob/main/docs/adr/0023-apache-2-and-the-cla.md)

## Context

OpenCCU-Loom was released under MIT from its first tag to 0.91.0
(ADR 0001). ADR 0001 chose MIT over Apache-2.0 for consistency with the
MIT siblings and said it "may revisit if patents become a concern". Two
things changed since.

**The Matter stack it embeds moved to Apache-2.0.** go-fabric, the
module that carries the Matter protocol substrate and cluster servers
since 0.74.0, adopts Apache-2.0 from its next release (go-fabric
ADR 0023, 2026-10-15), for the reason matter.js and connectedhomeip chose
it: the explicit patent grant of §3 in a protocol standardised by a
consortium with patent pools. OpenCCU-Loom carries Matter-side code of
its own with the same exposure — 66 Go files cite the matter.js behaviour
they mirror, the device profiles are ports of the cluster semantics, and
the topology assembler, the commissioning surface and the REST operator
surface sit on top of go-fabric's node facade (ADR 0084). The concern
ADR 0001 deferred is here.

**The copyright chain is still a single person.** `git shortlog -sne --all`
on 2026-10-15: 932 commits by SukramJ, 52 by dependabot, 4 by
github-actions. No outside author's consent is needed; the window ADR 0066
and the declined-CLA note describe — "the first outside contribution
merged without an agreement makes relicensing impossible for that code,
permanently" — is still open.

Two earlier decisions point the other way and are revisited here, not
silently overridden:

- ADR 0066 (rejected) weighed AGPL with a commercial exception and
  decided the project stays MIT, keeping the daemon a possible
  destination for code moved out of the MIT siblings. That consequence
  holds under Apache-2.0: MIT material may enter an Apache project, and
  Apache material may enter an MIT project with its notices.
- The declined-CLA note dropped the CLA because "the thing it protects
  was given up" — no plan to sell commercial exceptions — and because a
  CLA is a contribution barrier. What the owner wants to keep now is the
  option go-fabric keeps: to license the whole project under other terms
  later, which is lost with the first outside contribution that arrives
  without a rights grant. The barrier is accepted as the price of that
  option; the agreement is one line in the first pull request.

## Decision

1. **Apache License 2.0** from the next release: `LICENSE` is the Apache
   text, `NOTICE` names the copyright, the matter.js lineage shared with
   go-fabric, and the eQ-3 data layer (ADR 0003), every Go file's SPDX
   header reads `Apache-2.0`, and `TestLicenseHeaderOnEveryGoFile`
   (`tests/contract/`) enforces both header lines — the header was
   reviewed, not guarded, before. Generated files carry the same header
   from their generators. The SPA's TypeScript, the scripts, the SQL
   migrations and the packaging files carry the same SPDX identifier.
2. **The released MIT versions stay MIT.** A licence already granted is
   not revoked; 0.91.0 and earlier remain available under MIT.
3. **A contributor licence agreement** (`CLA.md`, the same text go-fabric
   uses with this project's upstream list): every contribution grants the
   owner a copyright licence that allows sublicensing under any terms, a
   patent licence with a litigation termination clause, and the usual
   representations. Agreement is a line in the first pull request
   (`.github/PULL_REQUEST_TEMPLATE.md`); pull requests without it are not
   merged. The DCO sign-off (`git commit -s`) stays as the authorship
   chain. Dependabot's changes are mechanical and carry no authorship.
4. **Dependencies stay permissive** (MIT, Apache-2.0, BSD): nothing in
   this decision opens the door to copyleft. The eQ-3 data layer is
   unchanged — it was never under the source licence (ADR 0003).

## Consequences

- Apache-2.0 is MIT's superset for consumers: the notices travel
  (`NOTICE`, `THIRD-PARTY-NOTICES.md`, now packed into every release
  archive beside `LICENSE`), modified files need no change marks beyond
  the header, and the patent grant protects users of the daemon's own
  Matter code as it does users of matter.js and go-fabric.
- The go-fabric notice obligation that starts with its first Apache
  release (Apache §4(d)) is met by the same path: the Go module's NOTICE
  reaches the archives through `THIRD-PARTY-NOTICES.md`, which names it
  and the reproduced matter.js NOTICE it carries forward.
- Apache-2.0 is incompatible with GPLv2-only. Nothing in the dependency
  graph or the sibling projects is GPLv2, and a daemon is not embedded in
  one; the constraint is recorded, not expected to bite.
- Relicensing later stays possible for the whole project as long as every
  contribution came under the CLA.
- The OCI image label, the CCU add-on manifest, the About page (en/de),
  README, CONTRIBUTING, CLAUDE.md and SPECIFICATION name the licence and
  the agreement; the changelog announces the change as a user-visible one.
- The working note on declined contributor agreements stays as the record
  of the August decision, with a dated line pointing here.
