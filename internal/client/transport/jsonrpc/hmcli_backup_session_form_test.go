// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package jsonrpc

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
)

// hmCliSessionURLSIDPattern is the CCU's OWN extractor for a session id in a
// query string, transcribed from OpenCCU-Base www/tcl/eq3_old/session.tcl
// (proc session_urlsid): `regexp "$sidname=(@[A-Za-z0-9]*@)"`. The @ signs are
// part of the captured value — a companion proc strips them again for the ReGa
// lookup — so a request that sends the bare id matches nothing and is rejected
// before any action runs.
//
// Anchoring the test on the firmware's regexp rather than on a substring check
// is the point: a substring assertion passes whether or not the delimiters are
// there, and would have confirmed whichever form this code happened to build.
var hmCliSessionURLSIDPattern = regexp.MustCompile(`(?:^|&)sid=(@[A-Za-z0-9]*@)(?:&|$)`)

// TestHmCliDownloadBackupSendsDelimitedSessionID pins that the backup download
// presents its session id in the form the CCU's CGI actually extracts.
func TestHmCliDownloadBackupSendsDelimitedSessionID(t *testing.T) {
	const wantSID = "testsession123"

	var gotQuery string
	mux := http.NewServeMux()
	mux.HandleFunc("/config/cp_security.cgi", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = io.WriteString(w, "sbk")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c, err := New(Config{Endpoint: srv.URL + "/api/homematic.cgi"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c.mu.Lock()
	c.sessionID = wantSID
	c.mu.Unlock()

	if _, err := c.DownloadBackup(context.Background()); err != nil {
		t.Fatalf("DownloadBackup: %v", err)
	}

	m := hmCliSessionURLSIDPattern.FindStringSubmatch(gotQuery)
	if m == nil {
		t.Fatalf("backup request query = %q; the CCU's session_urlsid regexp finds no @-delimited sid in it, so the CGI would reject the request", gotQuery)
	}
	if got, want := m[1], "@"+wantSID+"@"; got != want {
		t.Errorf("captured session id = %q, want %q", got, want)
	}
}
