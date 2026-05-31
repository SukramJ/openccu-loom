// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// SessionProvider returns the currently-cached CCU session ID. The
// HTTP backup restorer wraps the JSON-RPC client through this narrow
// surface so the restorer stays testable without dragging the full
// transport layer into its tests.
type SessionProvider interface {
	SessionID() string
}

// HTTPBackupRestorer uploads a previously stored .sbk file back to the
// CCU via HTTP-multipart against `/config/cp_security.cgi`. The CGI
// endpoint accepts the session id wrapped in `@…@`.
//
// The restorer is intentionally narrow: it does not own the session,
// it does not log in. Callers wire it after the JSON-RPC client has
// established a session; if the session expired the upload returns
// [ErrRestoreNoSession].
//
// The CCU runs the restore asynchronously and reboots when done; the
// HTTP response only confirms that the upload was accepted. Callers
// poll the device list / health tracker afterwards to confirm the
// reboot completed.
type HTTPBackupRestorer struct {
	// BaseURL is the CCU root, e.g. "https://ccu" — *not* the JSON-RPC
	// endpoint. The restorer suffixes "/config/cp_security.cgi".
	BaseURL string

	// Session supplies the active JSON-RPC session id. Required.
	Session SessionProvider

	// HTTPClient is the transport. nil falls back to a 5-minute-timeout
	// http.Client with TLS-skip when the daemon's CCU config disables
	// verify.
	HTTPClient *http.Client

	// InsecureSkipTLSVerify is consulted when HTTPClient is nil so the
	// restorer's default client matches the operator's CCU config.
	InsecureSkipTLSVerify bool
}

// ErrRestoreNoSession is returned when the wrapped JSON-RPC client
// has no active session id at restore time.
var ErrRestoreNoSession = errors.New("backup_restorer: no active CCU session")

// ErrRestoreNoBaseURL is returned when the restorer was constructed
// without a BaseURL.
var ErrRestoreNoBaseURL = errors.New("backup_restorer: BaseURL is required")

// Restore implements [BackupRestorer]. Streams payload as a multipart
// "file" field plus the CCU's expected `action`/`sid` form fields.
// Returns id unchanged so the SPA can keep polling under the same
// identifier.
func (r *HTTPBackupRestorer) Restore(ctx context.Context, id string, payload io.Reader) (string, error) {
	if r == nil || r.BaseURL == "" {
		return "", ErrRestoreNoBaseURL
	}
	if r.Session == nil {
		return "", ErrRestoreNoSession
	}
	sid := r.Session.SessionID()
	if sid == "" {
		return "", ErrRestoreNoSession
	}

	endpoint, err := r.buildURL(sid)
	if err != nil {
		return "", err
	}

	body, contentType, err := buildMultipart(id, payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return "", fmt.Errorf("backup_restorer: build request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)

	client := r.HTTPClient
	if client == nil {
		client = defaultRestoreClient(r.InsecureSkipTLSVerify)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("backup_restorer: post: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		// Drain so the connection can be reused.
		_, _ = io.Copy(io.Discard, resp.Body)
		return "", fmt.Errorf("backup_restorer: CCU returned HTTP %d", resp.StatusCode)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return id, nil
}

func (r *HTTPBackupRestorer) buildURL(sid string) (string, error) {
	base := strings.TrimRight(r.BaseURL, "/")
	parsed, err := url.Parse(base + "/config/cp_security.cgi")
	if err != nil {
		return "", fmt.Errorf("backup_restorer: build url: %w", err)
	}
	q := parsed.Query()
	q.Set("sid", "@"+sid+"@")
	q.Set("action", "restore_backup")
	parsed.RawQuery = q.Encode()
	return parsed.String(), nil
}

func buildMultipart(id string, payload io.Reader) (io.Reader, string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	filename := id
	if !strings.HasSuffix(filename, ".sbk") {
		filename += ".sbk"
	}
	field, err := w.CreateFormFile("file", filename)
	if err != nil {
		return nil, "", fmt.Errorf("backup_restorer: form file: %w", err)
	}
	if _, err := io.Copy(field, payload); err != nil {
		return nil, "", fmt.Errorf("backup_restorer: copy payload: %w", err)
	}
	if err := w.WriteField("action", "restore_backup"); err != nil {
		return nil, "", fmt.Errorf("backup_restorer: write action: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, "", fmt.Errorf("backup_restorer: close multipart: %w", err)
	}
	return &buf, w.FormDataContentType(), nil
}

func defaultRestoreClient(insecure bool) *http.Client {
	tr := &http.Transport{}
	if insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // operator opt-in
	}
	return &http.Client{Timeout: 5 * time.Minute, Transport: tr}
}
