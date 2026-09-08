# notes/parity/matter

Parity artefacts that lock OpenCCU-Loom's Matter-side surface against
matter.js HEAD. See `CLAUDE.md §matter.js as the Matter Gold Standard` for
the full workflow.

Two things live here now: the schema **pin**, and the **scenario corpus**. The
wire-fixture generators moved to
[go-fabric](https://github.com/SukramJ/go-fabric/tree/main/notes/parity/matter)
along with the Matter stack — the tests that consume their output are that
module's, and a generator whose output nothing here reads was a copy waiting to
go stale.

---

## Files

| File / directory | Purpose | Regen |
|---|---|---|
| `matter-schema-snapshot.json` | Cluster IDs, revisions, attribute IDs, device-type revisions from `@matter/model` | `make sync-matter-schema` (extracted in go-fabric) |
| `scenarios/` | Recorded end-to-end scenarios driven against a live bridge | `scenarios/_record.ts` — see `scenarios/README.md` |

**Which matter.js does the snapshot pin?** `matter-schema-snapshot.json`'s
top-level `matter` object records the provenance of each regeneration: the
matter.js source commit (`sourceCommit`) the schema was extracted from, plus
the Matter spec `revision` / `specificationVersion` /
`interactionModelRevision` / `dataModelRevision` reported by `@matter/model`.
The extractor captures these automatically, so the reference is always
traceable to an exact matter.js commit.

**The schema snapshot is a pin, not a source.** The extractor and the
generator that turns the extract into Go maps both live in the go-fabric
module, as its `script/extract-from-matter-js.ts` and
`script/generate_matter_schema.go`, next to the `schema` package they feed. The
copy here exists so a change to the Matter schema cannot arrive unnoticed
inside a dependency bump: `TestMatterSchemaSnapshotInSync`
(`tests/contract/`) compares it against the module's embedded copy, and
`make sync-matter-schema` refreshes it once the new bytes are deliberate.
Re-extracting from matter.js HEAD happens in go-fabric first — see that
module's `CLAUDE.md` — and only then is the pin here refreshed.

---

## Consumer tests

| Artefact | Go test |
|---|---|
| `matter-schema-snapshot.json` | `tests/contract/matter_schema_sync_test.go` (the pin) and `tests/chiptool/wire_truth_test.go`; the parity tests that read the same bytes live in the go-fabric module |
| `scenarios/` | `tests/scenario/` (the runner) and `tests/contract/matter_scenario_gate_test.go` (coverage gate) |

---

## Moved: TLV and IM wire fixtures

`generate-tlv-wire-fixtures.ts`, `generate-im-wire-fixtures.ts` and the two
JSON files they produced now live in go-fabric, with the `testdata/` copies
next to the tests as the masters. The by-design divergences those fixtures
record — fixed-width SubscriptionId / MaxInterval encoding, always-emitted
empty optional arrays, decode-only message types — are documented in
[go-fabric's `by_design.md`](https://github.com/SukramJ/go-fabric/blob/main/notes/parity/by_design.md)
together with the rest of the Matter divergence catalogue.
