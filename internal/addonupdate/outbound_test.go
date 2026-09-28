// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package addonupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

// recordingHandler wraps h and appends one normalised line per request:
// "METHOD path" followed by every header (sorted by name, Host excluded
// because httptest makes it a random port). The lines are what
// docs/privacy.md documents as the update check's outbound requests —
// the test pins them verbatim so a change to what leaves the daemon is
// a red test that names the document to update.
type recordingHandler struct {
	mu       sync.Mutex
	requests []string
	next     http.Handler
}

func (rec *recordingHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s", r.Method, r.URL.Path)
	names := make([]string, 0, len(r.Header))
	for name := range r.Header {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(&b, "\n%s: %s", name, strings.Join(r.Header[name], ", "))
	}
	rec.mu.Lock()
	rec.requests = append(rec.requests, b.String())
	rec.mu.Unlock()
	rec.next.ServeHTTP(w, r)
}

func (rec *recordingHandler) Requests() []string {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return append([]string(nil), rec.requests...)
}

// tinyTarGz returns a minimal gzipped tarball so DownloadAndStage has a
// real body to hash.
func tinyTarGz(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	content := []byte("addon")
	if err := tw.WriteHeader(&tar.Header{Name: "addon/VERSION", Mode: 0o644, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestOutboundRequestsArePinned drives the whole self-update path — the
// release check, then checksums.txt, then the asset — against a
// recording server and compares every request verbatim: method, path
// and each header. No version, serial, address or other identifier of
// the running system is in any of them; the User-Agent is Go's own.
// docs/privacy.md documents exactly this set, so the two change
// together or this test fails.
func TestOutboundRequestsArePinned(t *testing.T) {
	asset := tinyTarGz(t)
	sum := sha256.Sum256(asset)
	assetName := ccuAddonAssetPrefix + "1.2.3" + ccuAddonAssetSuffix

	var srv *httptest.Server
	rec := &recordingHandler{next: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases/latest":
			fmt.Fprintf(w, `{"tag_name":"v1.2.3","html_url":"%s/notes","assets":[
{"name":"%s","browser_download_url":"%s/assets/%s"},
{"name":"checksums.txt","browser_download_url":"%s/assets/checksums.txt"}]}`,
				srv.URL, assetName, srv.URL, assetName, srv.URL)
		case "/assets/checksums.txt":
			fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(sum[:]), assetName)
		case "/assets/" + assetName:
			_, _ = w.Write(asset)
		default:
			http.NotFound(w, r)
		}
	})}
	srv = httptest.NewServer(rec)
	t.Cleanup(srv.Close)

	ctx := context.Background()
	checker := &Checker{HTTPClient: srv.Client(), BaseURL: srv.URL}
	info, err := checker.LatestRelease(ctx)
	if err != nil {
		t.Fatal(err)
	}
	dl := &Downloader{HTTPClient: srv.Client(), StagePath: filepath.Join(t.TempDir(), "new_addon.tar.gz")}
	if err := dl.DownloadAndStage(ctx, info); err != nil {
		t.Fatal(err)
	}

	want := []string{
		"GET /releases/latest\nAccept: application/vnd.github+json\nAccept-Encoding: gzip\nUser-Agent: Go-http-client/1.1",
		"GET /assets/checksums.txt\nAccept-Encoding: gzip\nUser-Agent: Go-http-client/1.1",
		"GET /assets/" + assetName + "\nAccept-Encoding: gzip\nUser-Agent: Go-http-client/1.1",
	}
	if got := rec.Requests(); strings.Join(got, "\n\n") != strings.Join(want, "\n\n") {
		t.Errorf("the outbound requests changed — update docs/privacy.md together with this test:\n%s", strings.Join(got, "\n\n"))
	}
}
