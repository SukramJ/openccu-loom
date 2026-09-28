// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package sbom carries the software bill of materials the release build
// embeds into the binary. `make sbom` writes gen/sbom.cdx.json.gz (the Go
// module graph merged with the Config UI's npm tree, CycloneDX 1.6); the
// release workflow runs it before the build, exactly as the SPA bundle is
// produced. A development build has no archive and [Embedded.SBOMArchive]
// reports that, so the REST endpoint answers 404 rather than serving a
// stale or partial document.
package sbom

import "embed"

//go:embed all:gen
var genFS embed.FS

// archivePath is where `make sbom` puts the merged, gzipped document.
const archivePath = "gen/sbom.cdx.json.gz"

// Embedded serves the build-time SBOM archive.
type Embedded struct{}

// SBOMArchive returns the gzipped CycloneDX document and whether this
// build carries one.
func (Embedded) SBOMArchive() ([]byte, bool) {
	b, err := genFS.ReadFile(archivePath)
	if err != nil || len(b) == 0 {
		return nil, false
	}
	return b, true
}
