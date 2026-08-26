// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeSession is a minimal SessionProvider stub for use in restorer tests.
type fakeSession struct{ id string }

func (f fakeSession) SessionID() string { return f.id }

// TestHTTPBackupRestorerHappyPath verifies that a well-formed Restore call
// produces the correct URL, multipart body, and returns the unmodified id.
func TestHTTPBackupRestorerHappyPath(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify path.
		if r.URL.Path != "/config/cp_security.cgi" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		// Verify query parameters.
		if got := r.URL.Query().Get("sid"); got != "@SESS-1@" {
			t.Errorf("sid = %q, want @SESS-1@", got)
		}
		if got := r.URL.Query().Get("action"); got != "restore_backup" {
			t.Errorf("action query = %q, want restore_backup", got)
		}

		// Parse the multipart body.
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			t.Errorf("ParseMultipartForm: %v", err)
			http.Error(w, "bad multipart", http.StatusBadRequest)
			return
		}

		// Check file field.
		f, fh, err := r.FormFile("file")
		if err != nil {
			t.Errorf("FormFile: %v", err)
			http.Error(w, "no file", http.StatusBadRequest)
			return
		}
		defer f.Close()

		if fh.Filename != "0001ABCD.sbk" {
			t.Errorf("filename = %q, want 0001ABCD.sbk", fh.Filename)
		}

		data, _ := io.ReadAll(f)
		if string(data) != "backup-bytes" {
			t.Errorf("file content = %q, want backup-bytes", string(data))
		}

		// Check action form field.
		if got := r.FormValue("action"); got != "restore_backup" {
			t.Errorf("action form field = %q, want restore_backup", got)
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	restorer := &HTTPBackupRestorer{
		BaseURL:    srv.URL,
		Session:    fakeSession{id: "SESS-1"},
		HTTPClient: srv.Client(),
	}

	got, err := restorer.Restore(context.Background(), "0001ABCD", strings.NewReader("backup-bytes"))
	if err != nil {
		t.Fatalf("Restore: unexpected error: %v", err)
	}
	if got != "0001ABCD" {
		t.Errorf("returned id = %q, want 0001ABCD", got)
	}
}

// TestHTTPBackupRestorerAppendsSbkSuffix verifies that .sbk is appended when
// missing and not doubled when the id already carries the suffix.
func TestHTTPBackupRestorerAppendsSbkSuffix(t *testing.T) {
	t.Parallel()

	type testCase struct {
		id       string
		wantFile string
	}
	cases := []testCase{
		{id: "foo", wantFile: "foo.sbk"},
		{id: "bar.sbk", wantFile: "bar.sbk"},
	}

	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := r.ParseMultipartForm(10 << 20); err != nil {
					t.Errorf("ParseMultipartForm: %v", err)
					http.Error(w, "bad multipart", http.StatusBadRequest)
					return
				}
				_, fh, err := r.FormFile("file")
				if err != nil {
					t.Errorf("FormFile: %v", err)
					http.Error(w, "no file", http.StatusBadRequest)
					return
				}
				if fh.Filename != tc.wantFile {
					t.Errorf("filename = %q, want %q", fh.Filename, tc.wantFile)
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer srv.Close()

			restorer := &HTTPBackupRestorer{
				BaseURL:    srv.URL,
				Session:    fakeSession{id: "S"},
				HTTPClient: srv.Client(),
			}

			if _, err := restorer.Restore(context.Background(), tc.id, strings.NewReader("x")); err != nil {
				t.Fatalf("Restore: %v", err)
			}
		})
	}
}

// TestHTTPBackupRestorerNoBaseURLErrors verifies ErrRestoreNoBaseURL is
// returned when the restorer has an empty BaseURL.
func TestHTTPBackupRestorerNoBaseURLErrors(t *testing.T) {
	t.Parallel()

	restorer := &HTTPBackupRestorer{}
	_, err := restorer.Restore(context.Background(), "id", strings.NewReader(""))
	if !errors.Is(err, ErrRestoreNoBaseURL) {
		t.Fatalf("err = %v, want ErrRestoreNoBaseURL", err)
	}
}

// TestHTTPBackupRestorerNoSessionErrors verifies ErrRestoreNoSession is
// returned both when Session is nil and when the provider returns "".
func TestHTTPBackupRestorerNoSessionErrors(t *testing.T) {
	t.Parallel()

	t.Run("nil session", func(t *testing.T) {
		t.Parallel()
		restorer := &HTTPBackupRestorer{BaseURL: "http://ccu"}
		_, err := restorer.Restore(context.Background(), "id", strings.NewReader(""))
		if !errors.Is(err, ErrRestoreNoSession) {
			t.Fatalf("err = %v, want ErrRestoreNoSession", err)
		}
	})

	t.Run("empty session id", func(t *testing.T) {
		t.Parallel()
		restorer := &HTTPBackupRestorer{
			BaseURL: "http://ccu",
			Session: fakeSession{id: ""},
		}
		_, err := restorer.Restore(context.Background(), "id", strings.NewReader(""))
		if !errors.Is(err, ErrRestoreNoSession) {
			t.Fatalf("err = %v, want ErrRestoreNoSession", err)
		}
	})
}

// TestHTTPBackupRestorerNon200Errors verifies that a non-200 response from the
// CCU produces an error whose message includes the HTTP status code.
func TestHTTPBackupRestorerNon200Errors(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway) // 502
	}))
	defer srv.Close()

	restorer := &HTTPBackupRestorer{
		BaseURL:    srv.URL,
		Session:    fakeSession{id: "S"},
		HTTPClient: srv.Client(),
	}

	_, err := restorer.Restore(context.Background(), "id", strings.NewReader("x"))
	if err == nil {
		t.Fatal("expected error for 502 response")
	}
	if !strings.Contains(err.Error(), "HTTP 502") {
		t.Errorf("error = %q, want it to contain HTTP 502", err.Error())
	}
}

// TestHTTPBackupRestorerHonorsContext verifies that a context deadline
// cancels an in-flight Restore call.
func TestHTTPBackupRestorerHonorsContext(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			// server-side context cancelled by client disconnect
		case <-time.After(500 * time.Millisecond):
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	restorer := &HTTPBackupRestorer{
		BaseURL:    srv.URL,
		Session:    fakeSession{id: "S"},
		HTTPClient: srv.Client(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := restorer.Restore(ctx, "id", strings.NewReader("x"))
	if err == nil {
		t.Fatal("expected error from context timeout")
	}
}
