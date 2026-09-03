// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

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

// HTTPBackupRestorer uploads a previously stored .sbk file to the CCU via
// HTTP-multipart against `/config/cp_security.cgi`, as the CGI's own upload
// form does (www/config/cp_security.cgi:1026-1028): action `backup_upload`,
// payload in the field `backup_file`, session id wrapped in `@…@` — the
// canonical session id literally carries the delimiters
// (www/tcl/eq3_old/session.tcl:464) and the query parser accepts nothing else
// (:312).
//
// The restorer is intentionally narrow: it does not own the session,
// it does not log in. Callers wire it after the JSON-RPC client has
// established a session; if the session expired the upload returns
// [ErrRestoreNoSession].
//
// KNOWN INCOMPLETE, and the shape of the gap is worth stating precisely.
// Upload is step one of three in the firmware's restore flow:
// action_backup_upload only stores the archive as /tmp/new_config.tar
// (cp_security.cgi:1510-1516); action_backup_restore_check untars it and
// validates its signature (:373); action_backup_restore_go performs the
// restore and requires the system security key (:511, reached from :505 as
// `action=backup_restore_go&key=…`). This type sends only the first, so a
// successful call means the archive reached the CCU, not that a restore ran.
// Completing the flow needs the operator's system security key, which is a
// credential this daemon does not hold today.
//
// Endpoint caveat: cp_security.cgi is the receiver on the OpenCCU-Base stand.
// A newer stand posts the same upload to /config/fileupload.ccc with the CGI
// passed back as `url=`; that receiver is not present in the readable source,
// so which stand a given CCU runs is not established here.
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

// Restore implements [BackupRestorer]. Streams payload as the multipart
// `backup_file` field plus the CGI's `action=backup_upload` and the
// `@`-wrapped `sid`. Returns id unchanged so the SPA can keep polling under
// the same identifier.
//
// A nil error means the archive was accepted, not that the CCU restored it —
// see the note on [HTTPBackupRestorer] for the two firmware steps this call
// does not send.
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
	q.Set("action", "backup_upload")
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
	field, err := w.CreateFormFile("backup_file", filename)
	if err != nil {
		return nil, "", fmt.Errorf("backup_restorer: form file: %w", err)
	}
	if _, err := io.Copy(field, payload); err != nil {
		return nil, "", fmt.Errorf("backup_restorer: copy payload: %w", err)
	}
	if err := w.WriteField("action", "backup_upload"); err != nil {
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
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // operator opt-in; see #20
	}
	return &http.Client{Timeout: 5 * time.Minute, Transport: tr}
}
