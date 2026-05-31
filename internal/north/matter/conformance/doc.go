// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package conformance houses the Matter conformance suite for
// openccu-loom — golden-vector regression tests, load tests, and the
// chip-tool subprocess smoke harness.
//
// Three layers:
//
//   - **Golden vectors** (vectors.go + *_vectors_test.go): fixed
//     byte-arrays the bridge MUST round-trip. These act as
//     spec-anchored regression detectors — drift in a wire codec
//     fails a known-good payload deterministically. Run on every PR.
//
//   - **Load tests** (load_test.go): drive the endpoint assembler /
//     subscription engine at the largest fleet shapes the bridge
//     supports (~600 bridged endpoints across multiple centrals).
//     Run nightly; failure indicates an O(n^2) regression worth
//     investigating before release.
//
//   - **chip-tool smoke** (chiptool_smoke_test.go, build-tagged
//     `chiptool`): launches `chip-tool pairing onnetwork-long` as a
//     subprocess and verifies the bridge accepts the commissioning
//     flow end-to-end. Requires chip-tool installed; skipped on
//     systems without it.
//
// The HA Matter Server / Apple Home / Google Home smoke tests are
// manual — they require dedicated hardware (an iOS / Android device,
// a Home Hub) and live in [docs/matter-conformance.md] as a release
// checklist rather than executable Go tests.
package conformance
