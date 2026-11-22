// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"errors"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeSession is a minimal SessionProvider stub for use in restorer tests.
type fakeSession struct{ id string }

func (f fakeSession) SessionID() string { return f.id }

// TestHTTPBackupRestorerAppendsSbkSuffix verifies that .sbk is appended when
// missing and not doubled when the id already carries the suffix.
func TestHTTPBackupRestorerAppendsSbkSuffix(t *testing.T) {
	t.Parallel()

	cases := []struct {
		id       string
		wantFile string
	}{
		{id: "foo", wantFile: "foo.sbk"},
		{id: "bar.sbk", wantFile: "bar.sbk"},
	}
	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			t.Parallel()
			body, contentType, err := buildMultipart(tc.id, strings.NewReader("x"))
			if err != nil {
				t.Fatalf("buildMultipart: %v", err)
			}
			_, params, err := mime.ParseMediaType(contentType)
			if err != nil {
				t.Fatalf("ParseMediaType: %v", err)
			}
			part, err := multipart.NewReader(body, params["boundary"]).NextPart()
			if err != nil {
				t.Fatalf("NextPart: %v", err)
			}
			if got := part.FileName(); got != tc.wantFile {
				t.Errorf("filename = %q, want %q", got, tc.wantFile)
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
