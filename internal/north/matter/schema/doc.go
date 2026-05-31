// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package schema exposes typed Go lookups for Matter cluster and device-type
// metadata extracted from matter.js HEAD. The primary data lives in the
// generated files clusters.go and devicetypes.go (maps keyed by uint32 ID);
// this file adds the hand-written lookup helpers that wrap the maps.
//
// Source of truth: docs/parity/matter/matter-schema-snapshot.json,
// itself regenerable from matter.js HEAD via:
//
//	python3 script/extract_matterjs_head.py
//
// After updating the snapshot, regenerate the Go code:
//
//	make generate-matter-schema
//
// Mirrors matter.js HEAD `packages/model/src/standard/elements/*.element.ts`.
package schema
