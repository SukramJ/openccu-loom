// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package hmapi

// EditorComponentManifest is the answer of GET /api/v1/ui/editor-component.
// It tells an embedding integration where the device editor's web
// component is, which version it is and which REST routes it calls. The
// integration fetches the module from ModuleURL, serves it from its own
// origin under the hash, and relays only calls whose path lies under one of
// Routes (ADR 0089 decisions 2 and 3).
type EditorComponentManifest struct {
	// ModuleURL is the daemon path of the module; its file name carries
	// the hash.
	ModuleURL string `json:"module_url"`
	// Hash is the hex SHA-256 of the module's bytes.
	Hash string `json:"hash"`
	// Routes are the path prefixes below /api/v1 the editor calls. A
	// prefix covers itself and every path below it.
	Routes []string `json:"routes"`
}
