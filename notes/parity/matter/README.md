# notes/parity/matter

Parity artefacts that lock OpenCCU-Loom's Matter-side wire shapes against
matter.js HEAD. See `CLAUDE.md §matter.js as the Matter Gold Standard` for
the full workflow.

**Which matter.js does the snapshot pin?** `matter-schema-snapshot.json`'s
top-level `matter` object records the provenance of each regeneration: the
matter.js source commit (`sourceCommit`) the schema was extracted from, plus
the Matter spec `revision` / `specificationVersion` /
`interactionModelRevision` / `dataModelRevision` reported by `@matter/model`.
The extractor captures these automatically, so the reference is always
traceable to an exact matter.js commit.

**The schema snapshot is a pin, not a source.** The extractor and the
generator that turns the extract into Go maps both live in the
[go-fabric](https://github.com/SukramJ/go-fabric) module, as its
`script/extract-from-matter-js.ts` and `script/generate_matter_schema.go`,
next to the `schema` package they feed. The copy here exists so a
change to the Matter schema cannot arrive unnoticed inside a dependency bump:
`TestMatterSchemaSnapshotInSync` (`tests/contract/`) compares it against the
module's embedded copy, and `make sync-matter-schema` refreshes it once the
new bytes are deliberate. The TLV and IM wire-fixture generators below are a
separate pipeline and still live here.

---

## Files

| File | Purpose | Regen |
|---|---|---|
| `matter-schema-snapshot.json` | Cluster IDs, revisions, attribute IDs, device-type revisions from `@matter/model` | `make sync-matter-schema` (extracted in go-fabric) |
| `tlv-wire-fixtures.json` | Low-level TLV primitive wire bytes (uint, bool, string, tags) | `generate-tlv-wire-fixtures.ts` |
| `im-wire-fixtures.json` | IM-message-level wire bytes (ReportData, StatusResponse, …) | `generate-im-wire-fixtures.ts` |

---

## Regen commands

### Cluster/device-type schema snapshot

```sh
make sync-matter-schema
```

Re-extracting from matter.js HEAD happens in go-fabric first — see that
module's `CLAUDE.md` — and only then is the pin here refreshed.

### TLV primitive wire fixtures

```sh
cd /Users/markus/Documents/GitHub/matter.js
node /Users/markus/Documents/GitHub/openccu-loom/notes/parity/matter/generate-tlv-wire-fixtures.ts \
    > /Users/markus/Documents/GitHub/openccu-loom/notes/parity/matter/tlv-wire-fixtures.json
```

### IM-message wire fixtures

```sh
# Run from any working directory:
node /Users/markus/Documents/GitHub/openccu-loom/notes/parity/matter/generate-im-wire-fixtures.ts \
    > /Users/markus/Documents/GitHub/openccu-loom/notes/parity/matter/im-wire-fixtures.json
```

The generator resolves `@matter/types` relative to the matter.js checkout
at `../matter.js` (two directories above the openccu-loom repo root). If
your matter.js checkout lives elsewhere, set the `MATTER_JS_ROOT` env var
or edit the `matterJsRoot` constant at the top of the generator.

---

## Consumer tests

| Fixture file | Go test |
|---|---|
| `matter-schema-snapshot.json` | `tests/contract/matter_schema_sync_test.go` (the pin) and `tests/chiptool/wire_truth_test.go`; the parity tests that read the same bytes live in the go-fabric module |
| `tlv-wire-fixtures.json` | go-fabric `tlv/parity_matterjs_test.go` (embedded from that module's `tlv/testdata/`) |
| `im-wire-fixtures.json` | go-fabric `im/wire_fixtures_parity_test.go` (embedded from that module's `im/testdata/`) |

---

## By-design divergences

Some fixtures carry `"byDesignDivergence": true`. These record cases where
OpenCCU-Loom intentionally encodes differently from matter.js. The Go test
skips byte-equality for these entries but still exercises the encoder
(verifies it does not panic). The full rationale is in
`notes/parity/by_design.md`.

Current by-design categories for IM messages:

- **SubscriptionId / MaxInterval fixed-width encoding**: Go uses
  `PutUint32` / `PutUint16` (always 4 / 2 bytes); matter.js `TlvUInt32` /
  `TlvUInt16` magnitude-encodes (small values shrink to 1 byte). chip-tool
  and Apple Home reject magnitude-encoded subscription IDs.

- **Empty optional arrays**: Go always emits `attributeReports: []` even on
  keepalives; matter.js uses `TlvOptionalField` and omits the field when
  undefined.

- **Decode-only message types**: `WriteRequest`, `InvokeRequest`,
  `TimedRequest`, `ReadRequest`, `SubscribeRequest` are received by the
  bridge (not sent). They have no `MarshalTLV` method; their fixtures are
  for documentation and Stage-3 round-trip tests only.
