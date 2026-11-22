// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// fakeCCUUpload emulates the firmware's two-step upload: fileupload.ccc
// stores the request body and echoes the stored path, cp_security.cgi
// accepts only a path fileupload.ccc wrote. The byte arithmetic, the raw
// query parsing and the gates are ported from OpenCCU-Base
// src/webui/www/config/fileupload.ccc and cp_security.cgi, so a request
// shape the CGI would mis-read fails here too.
type fakeCCUUpload struct {
	mu       sync.Mutex
	files    map[string][]byte // path written by fileupload.ccc
	stored   []byte            // what action_backup_upload renamed to new_config.tar
	uploadOK bool              // false: answer like a rejected admin session
	seq      int
}

var fakeTmpPath = regexp.MustCompile(`^/usr/local/tmp/tmp\.[A-Za-z0-9]+$`)

func (f *fakeCCUUpload) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/config/fileupload.ccc":
		f.fileupload(w, r)
	case "/config/cp_security.cgi":
		f.cpSecurity(w, r)
	default:
		http.NotFound(w, r)
	}
}

// fileupload ports fileupload.ccc:7-123.
func (f *fakeCCUUpload) fileupload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		_, _ = io.WriteString(w, "ERROR: no POST request")
		return
	}
	br := bufio.NewReader(r.Body)
	// `read -r` keeps the trailing \r of each CRLF line (:13-16).
	line := func() string {
		s, _ := br.ReadString('\n')
		return strings.TrimSuffix(s, "\n")
	}
	boundary, disposition, ctype, _ := line(), line(), line(), line()
	a := len(boundary)*2 + len(disposition) + len(ctype) + 10 // :19-26
	size := int(r.ContentLength) - a                          // :33, Content-Length required
	if r.ContentLength <= 0 || size <= 0 {
		_, _ = io.WriteString(w, "ERROR: POST size <= 0")
		return
	}
	// Parameters come from the raw QUERY_STRING, never URL-decoded (:41-51).
	params := map[string]string{}
	for _, kv := range strings.Split(r.URL.RawQuery, "&") {
		k, v, _ := strings.Cut(kv, "=")
		params[k] = v
	}
	switch params["url"] { // :60-63
	case "/config/cp_maintenance.cgi", "/config/cp_network.cgi", "/config/cp_security.cgi", "/config/cp_software.cgi":
	default:
		_, _ = io.WriteString(w, "ERROR: invalid url parameter")
		return
	}
	if !regexp.MustCompile(`^[a-z_]+$`).MatchString(params["action"]) { // :64-66
		_, _ = io.WriteString(w, "ERROR: invalid action parameter")
		return
	}
	sid := params["sid"]
	if !regexp.MustCompile(`^[@A-Za-z0-9]*$`).MatchString(sid) || len(sid) != 12 || !f.uploadOK { // :71-96
		_, _ = io.WriteString(w, "ERROR: no valid admin session id")
		return
	}
	data := make([]byte, size) // :101-102, `head -c SIZE`
	n, _ := io.ReadFull(br, data)
	f.mu.Lock()
	f.seq++
	path := fmt.Sprintf("/usr/local/tmp/tmp.Ab%04d", f.seq)
	f.files[path] = data[:n]
	f.mu.Unlock()
	_, _ = fmt.Fprintf(w, "<html><script>var url = '%s?sid=%s';\n dlgPopup.LoadFromFile(url, 'action=%s&filename=%s');</script></html>",
		params["url"], sid, params["action"], path) // :112, :123
}

// cpSecurity ports the filename gate (cp_security.cgi:1626-1630) and
// action_backup_upload (:1532-1549).
func (f *fakeCCUUpload) cpSecurity(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if r.Form.Get("action") != "backup_upload" {
		_, _ = io.WriteString(w, "unexpected action")
		return
	}
	filename := r.Form.Get("filename")
	f.mu.Lock()
	data, ok := f.files[filename]
	f.mu.Unlock()
	if !fakeTmpPath.MatchString(filename) || !ok {
		// `file rename -force -- "" …` raises; the CGI answers an error page.
		_, _ = io.WriteString(w, "<html>error renaming \"\"</html>")
		return
	}
	f.mu.Lock()
	f.stored = data
	delete(f.files, filename)
	f.mu.Unlock()
	_, _ = io.WriteString(w, `<script>dlgPopup.LoadFromFile(url, "action=backup_restore_check");</script>`)
}

func newFakeCCUUpload(t *testing.T, uploadOK bool) (*fakeCCUUpload, *HTTPBackupRestorer) {
	t.Helper()
	f := &fakeCCUUpload{files: map[string][]byte{}, uploadOK: uploadOK}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, &HTTPBackupRestorer{
		BaseURL:    srv.URL,
		Session:    fakeSession{id: "SESSIONID1"},
		HTTPClient: srv.Client(),
	}
}

// TestHTTPBackupRestorerStoresTheArchiveThroughFileUpload drives a restore
// upload through the firmware's two-step receiver and asserts the exact
// archive bytes end up where action_backup_upload puts them.
func TestHTTPBackupRestorerStoresTheArchiveThroughFileUpload(t *testing.T) {
	t.Parallel()
	f, restorer := newFakeCCUUpload(t, true)
	payload := bytes.Repeat([]byte("sbk\x00\xff\r\n--"), 4096)

	got, err := restorer.Restore(context.Background(), "0001ABCD", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if got != "0001ABCD" {
		t.Errorf("returned id = %q, want 0001ABCD", got)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !bytes.Equal(f.stored, payload) {
		t.Fatalf("stored archive differs from the payload: got %d bytes, want %d", len(f.stored), len(payload))
	}
}

// TestHTTPBackupRestorerReportsARejectedUpload pins that fileupload.ccc's
// "ERROR: …" answer — sent with HTTP 200 — is a failure, not a success.
func TestHTTPBackupRestorerReportsARejectedUpload(t *testing.T) {
	t.Parallel()
	f, restorer := newFakeCCUUpload(t, false)

	if _, err := restorer.Restore(context.Background(), "0001ABCD", strings.NewReader("archive")); err == nil {
		t.Fatal("Restore succeeded although the CCU rejected the session")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.stored != nil {
		t.Fatal("an archive was stored although the upload was rejected")
	}
}
