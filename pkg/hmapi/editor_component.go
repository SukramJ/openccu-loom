// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package hmapi

// EditorComponentManifest is the answer of GET /api/v1/ui/editor-component.
// It tells an embedding integration where the device editor's web
// component is, which version it is and which REST routes it calls. The
// integration fetches the module from ModuleURL, serves it from its own
// origin under the hash, and relays only calls one of Routes covers
// (ADR 0089 decisions 2 and 3).
type EditorComponentManifest struct {
	// ModuleURL is the daemon path of the module; its file name carries
	// the hash.
	ModuleURL string `json:"module_url"`
	// Hash is the hex SHA-256 of the module's bytes.
	Hash string `json:"hash"`
	// Routes are the calls the editor makes: a method and a path prefix
	// below /api/v1 each.
	Routes []EditorRoute `json:"routes"`
}

// EditorRoute is one call class of the device editor. Prefix covers itself
// and every path below it, segment by segment; a `*` segment matches exactly
// one non-empty segment. Method is part of the route because a path alone
// cannot tell an edit from a device's lifecycle: `POST /devices/{addr}/
// paramsets/…` edits a configuration, `POST /devices/{addr}/release` unpairs
// the device.
type EditorRoute struct {
	// Method is the upper-case HTTP method.
	Method string `json:"method"`
	// Prefix is the absolute path prefix below /api/v1, without a trailing
	// slash.
	Prefix string `json:"prefix"`
}
