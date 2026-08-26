// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package parity provides the matter.js HEAD schema snapshot to all
// matter-side parity tests in one embed location. Mirrors matter.js
// HEAD `packages/model/src/standard/elements/*.element.ts`; regenerable
// via `notes/parity/matter/extract-from-matter-js.ts` (pipe stdout to
// notes/parity/matter/matter-schema-snapshot.json, then copy here).
//
// Sync note: internal/north/matter/parity/schema.json must be kept in
// sync with notes/parity/matter/matter-schema-snapshot.json. After
// regenerating the master file, copy it:
//
//	cp notes/parity/matter/matter-schema-snapshot.json internal/north/matter/parity/schema.json
package parity

import _ "embed"

//go:embed schema.json
var schemaJSON []byte

// SchemaJSON returns the matter.js HEAD schema snapshot as raw JSON
// bytes. Callers (parity_matterjs_test.go in cluster + model packages)
// unmarshal into their local cluster/devicetype struct shapes.
func SchemaJSON() []byte {
	return append([]byte(nil), schemaJSON...) // defensive copy
}
