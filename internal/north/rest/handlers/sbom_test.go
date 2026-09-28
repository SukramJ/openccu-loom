// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type stubSBOM struct {
	archive []byte
	ok      bool
}

func (s stubSBOM) SBOMArchive() ([]byte, bool) { return s.archive, s.ok }

func gzipped(t *testing.T, doc string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write([]byte(doc)); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

const sbomDoc = `{"bomFormat":"CycloneDX","specVersion":"1.6","components":[]}`

func TestSBOMAnswers404WithoutAnArchive(t *testing.T) {
	for name, src := range map[string]SBOMSource{
		"nil source":   nil,
		"empty source": stubSBOM{},
	} {
		rec := httptest.NewRecorder()
		SBOM(src)(rec, httptest.NewRequest(http.MethodGet, "/sbom", http.NoBody))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: got %d, want 404", name, rec.Code)
		}
	}
}

func TestSBOMServesGzipPassthroughAndInflates(t *testing.T) {
	h := SBOM(stubSBOM{archive: gzipped(t, sbomDoc), ok: true})

	// A gzip-capable client gets the archive verbatim.
	req := httptest.NewRequest(http.MethodGet, "/sbom", http.NoBody)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("gzip path: code %d, encoding %q", rec.Code, rec.Header().Get("Content-Encoding"))
	}
	gz, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.NewDecoder(gz).Decode(&doc); err != nil {
		t.Fatal(err)
	}
	if doc["bomFormat"] != "CycloneDX" {
		t.Errorf("gzip path: decoded %v", doc)
	}

	// A plain client gets it inflated.
	rec = httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/sbom", http.NoBody))
	if rec.Header().Get("Content-Encoding") != "" || !strings.Contains(rec.Body.String(), `"CycloneDX"`) {
		t.Errorf("plain path: encoding %q body %q", rec.Header().Get("Content-Encoding"), rec.Body.String())
	}
}

func TestSBOMETagAndDownload(t *testing.T) {
	h := SBOM(stubSBOM{archive: gzipped(t, sbomDoc), ok: true})

	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/sbom", http.NoBody))
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag on 200")
	}

	req := httptest.NewRequest(http.MethodGet, "/sbom", http.NoBody)
	req.Header.Set("If-None-Match", etag)
	rec = httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusNotModified {
		t.Errorf("If-None-Match: got %d, want 304", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/sbom?download=1", http.NoBody)
	rec = httptest.NewRecorder()
	h(rec, req)
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Errorf("download=1: Content-Disposition %q", cd)
	}
}
