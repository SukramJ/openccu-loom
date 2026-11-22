// SPDX-License-Identifier: Apache-2.0
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
	"regexp"
	"strings"
	"time"

	"github.com/SukramJ/openccu-loom/internal/backup/sbk"
)

// SessionProvider returns the currently-cached CCU session ID. The
// HTTP backup restorer wraps the JSON-RPC client through this narrow
// surface so the restorer stays testable without dragging the full
// transport layer into its tests.
type SessionProvider interface {
	SessionID() string
}

// HTTPBackupRestorer uploads a previously stored .sbk file to the CCU the way
// the WebUI's own restore form does — in two steps. The form posts the file to
// /config/fileupload.ccc with `action=backup_upload` and the receiving CGI as
// `url=` (OpenCCU-Base src/webui/www/config/cp_security.cgi:1045-1046).
// fileupload.ccc writes the request body to a temporary file and answers with
// a page that names it (fileupload.ccc:101-123); cp_security.cgi then accepts
// `action=backup_upload&filename=<that file>` and renames it to
// /usr/local/tmp/new_config.tar (:1532-1536). cp_security.cgi takes no upload
// itself: a filename fileupload.ccc did not write is discarded (:1627-1630).
//
// The restorer is intentionally narrow: it does not own the session,
// it does not log in. Callers wire it after the JSON-RPC client has
// established a session; if the session expired the upload returns
// [ErrRestoreNoSession].
//
// KNOWN INCOMPLETE, and the shape of the gap is worth stating precisely.
// Upload is step one of three in the firmware's restore flow:
// action_backup_upload only stores the archive as new_config.tar;
// action_backup_restore_check untars it and validates its signature (:373);
// action_backup_restore_go performs the restore and requires the system
// security key (:517). This type sends only the first, so a successful call
// means the archive reached the CCU, not that a restore ran. Completing the
// flow needs the operator's system security key, which is a credential this
// daemon does not hold today.
type HTTPBackupRestorer struct {
	// BaseURL is the CCU root, e.g. "https://ccu" — *not* the JSON-RPC
	// endpoint. The restorer suffixes "/config/fileupload.ccc" and
	// "/config/cp_security.cgi".
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

// ErrRestoreRejected is returned when the CCU answered the upload but did not
// store the archive. Both CGIs answer HTTP 200 on failure, so the status code
// alone cannot tell.
var ErrRestoreRejected = errors.New("backup_restorer: CCU rejected the upload")

const (
	// restoreUploadCGI is the receiver fileupload.ccc hands the stored file
	// to; it is one of the four values the script accepts
	// (fileupload.ccc:60-63).
	restoreUploadCGI = "/config/cp_security.cgi"
	// restoreUploadAction names the cp_security.cgi proc that stores the
	// archive (`action_$action`, cp_security.cgi:1632).
	restoreUploadAction = "backup_upload"
	// restoreStoredMarker is the next step action_backup_upload loads on
	// success (cp_security.cgi:1547); its absence means the archive was
	// not stored.
	restoreStoredMarker = "action=backup_restore_check"
)

// restoreTempFile matches the path fileupload.ccc reports, with the same
// pattern cp_security.cgi accepts (cp_security.cgi:1628).
var restoreTempFile = regexp.MustCompile(`filename=(/usr/local/tmp/tmp\.[A-Za-z0-9]+)`)

// Restore implements [BackupRestorer]: it posts payload to fileupload.ccc,
// then asks cp_security.cgi to store the file fileupload.ccc reported.
// Returns id unchanged so the SPA can keep polling under the same identifier.
//
// A nil error means the archive was stored, not that the CCU restored it —
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
	client := r.HTTPClient
	if client == nil {
		client = defaultRestoreClient(r.InsecureSkipTLSVerify)
	}

	tmpFile, err := r.upload(ctx, client, sid, id, payload)
	if err != nil {
		return "", err
	}
	if err := r.store(ctx, client, sid, tmpFile); err != nil {
		return "", err
	}
	return id, nil
}

// upload posts the archive to fileupload.ccc and returns the temporary file
// it was written to.
func (r *HTTPBackupRestorer) upload(ctx context.Context, client *http.Client, sid, id string, payload io.Reader) (string, error) {
	body, contentType, err := buildMultipart(id, payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.uploadURL(sid), body)
	if err != nil {
		return "", fmt.Errorf("backup_restorer: build upload request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)
	page, err := doRestoreRequest(client, req)
	if err != nil {
		return "", err
	}
	m := restoreTempFile.FindStringSubmatch(page)
	if m == nil {
		return "", fmt.Errorf("%w: fileupload.ccc: %s", ErrRestoreRejected, firstLine(page))
	}
	return m[1], nil
}

// store asks cp_security.cgi to take over the file fileupload.ccc wrote.
func (r *HTTPBackupRestorer) store(ctx context.Context, client *http.Client, sid, tmpFile string) error {
	endpoint, err := r.storeURL(sid)
	if err != nil {
		return err
	}
	form := url.Values{"action": {restoreUploadAction}, "filename": {tmpFile}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("backup_restorer: build store request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	page, err := doRestoreRequest(client, req)
	if err != nil {
		return err
	}
	if !strings.Contains(page, restoreStoredMarker) {
		return fmt.Errorf("%w: cp_security.cgi did not store %s", ErrRestoreRejected, tmpFile)
	}
	return nil
}

// uploadURL builds the fileupload.ccc address. The query string is written
// out verbatim: the script splits QUERY_STRING on `&` and `=` without
// URL-decoding it (fileupload.ccc:41-51), so an escaped `@` in the session id
// or `/` in the receiver path fails its checks (:60-75).
func (r *HTTPBackupRestorer) uploadURL(sid string) string {
	return strings.TrimRight(r.BaseURL, "/") + "/config/fileupload.ccc?sid=@" + sid +
		"@&action=" + restoreUploadAction + "&url=" + restoreUploadCGI
}

// storeURL builds the cp_security.cgi address; the Tcl CGI decodes its query.
func (r *HTTPBackupRestorer) storeURL(sid string) (string, error) {
	parsed, err := url.Parse(strings.TrimRight(r.BaseURL, "/") + restoreUploadCGI)
	if err != nil {
		return "", fmt.Errorf("backup_restorer: build url: %w", err)
	}
	q := parsed.Query()
	q.Set("sid", "@"+sid+"@")
	parsed.RawQuery = q.Encode()
	return parsed.String(), nil
}

// doRestoreRequest sends req and returns the body of a 200 answer.
func doRestoreRequest(client *http.Client, req *http.Request) (string, error) {
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("backup_restorer: post: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	page, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return "", fmt.Errorf("backup_restorer: read answer: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("backup_restorer: CCU returned HTTP %d", resp.StatusCode)
	}
	return string(page), nil
}

// firstLine trims an answer to its first line for an error message.
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	const maxLen = 120
	if len(s) > maxLen {
		s = s[:maxLen]
	}
	return s
}

// buildMultipart encodes the archive as the single file part fileupload.ccc
// expects. The script does not parse multipart: it skips exactly four lines
// (boundary, Content-Disposition, Content-Type, blank) and derives the file
// size from Content-Length minus those lines and the closing boundary
// (fileupload.ccc:12-33). Any further part — a form field included — would be
// written into the archive. The body is a *bytes.Buffer so the request
// carries the Content-Length the arithmetic needs.
func buildMultipart(id string, payload io.Reader) (*bytes.Buffer, string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	filename := id
	if !strings.HasSuffix(filename, sbk.Extension) {
		filename += sbk.Extension
	}
	field, err := w.CreateFormFile("backup_file", filename)
	if err != nil {
		return nil, "", fmt.Errorf("backup_restorer: form file: %w", err)
	}
	if _, err := io.Copy(field, payload); err != nil {
		return nil, "", fmt.Errorf("backup_restorer: copy payload: %w", err)
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
