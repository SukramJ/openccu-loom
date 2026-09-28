// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strings"

	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
)

// SBOMSource hands out the build-time SBOM archive. The production
// implementation is internal/sbom's embedded file; a build without one
// reports false and the route answers 404 — a property of the build, not
// a transient error.
type SBOMSource interface {
	SBOMArchive() (gzipped []byte, ok bool)
}

// SBOM serves the embedded CycloneDX document. The archive is stored
// gzipped; a client that accepts gzip gets it verbatim with
// Content-Encoding, anyone else gets it inflated. The ETag is the
// archive's SHA-256, computed once — the document cannot change without
// a new binary.
func SBOM(src SBOMSource) http.HandlerFunc {
	var (
		archive []byte
		etag    string
	)
	if src != nil {
		if b, ok := src.SBOMArchive(); ok {
			archive = b
			sum := sha256.Sum256(b)
			etag = `"` + hex.EncodeToString(sum[:8]) + `"`
		}
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if archive == nil {
			problem.Write(w, http.StatusNotFound,
				problem.New(problem.TypeNotFound, r, "No SBOM in this build", "this binary was built without `make sbom`"))
			return
		}
		if match := r.Header.Get("If-None-Match"); match != "" && match == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", etag)
		if r.URL.Query().Get("download") != "" {
			w.Header().Set("Content-Disposition", `attachment; filename="openccu-loom-sbom.cdx.json"`)
		}
		if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			w.Header().Set("Content-Encoding", "gzip")
			_, _ = w.Write(archive)
			return
		}
		gz, err := gzip.NewReader(bytes.NewReader(archive))
		if err != nil {
			problem.Write(w, http.StatusInternalServerError,
				problem.New(problem.TypeInternal, r, "SBOM archive unreadable", err.Error()))
			return
		}
		defer func() { _ = gz.Close() }()
		_, _ = io.Copy(w, gz) //nolint:gosec // bounded: the archive is a build artifact embedded in the binary, not caller input
	}
}
