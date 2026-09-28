# Wire-fixture corpus

Recorded daemon responses, one file per operation — the cross-language
payload contract between this daemon and
[openccu-loom-client](https://github.com/SukramJ/openccu-loom-client).
The rule is the one occulited's conformance corpus states for its two
implementations: **if one side changes the semantics without changing a
fixture here, that is the bug.**

- Every file is the output of the daemon's own handlers, recorded by
  `TestWireFixtureCorpusMatchesHandlers` (`tests/contract/`) against the
  fully wired router with the production auth chain. Nothing here is
  hand-written; regenerate with
  `go test -run TestWireFixtureCorpusMatchesHandlers ./tests/contract/ -update-wire-fixtures`.
- On every `make test` the same test replays each operation, validates
  the raw response against `assets/openapi.yaml` (problem bodies against
  `components/schemas/Problem`) and compares it byte-for-byte with the
  committed file. A payload change therefore fails loudly here first —
  and the failure message says to tell openccu-loom-client, which runs
  these files through its wire parsers as its own corpus.
- Fields that change on every run (uptimes, timestamps) are masked to
  `"<volatile>"`; the corpus entry in the test names them, so what is
  masked is itself part of the contract.
- The corpus starts deliberately small (info, health, warnings, sbom,
  two problem shapes). Extending it is one table row per operation; the
  WebSocket envelope (`assets/wsapi.json`) is the named next step and
  is not covered yet.
