// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
)

// stubBackupService is an inline stub for BackupService.
type stubBackupService struct {
	triggerID  string
	triggerErr error
	listResult []BackupEntry
	listErr    error
	restoreID  string
	restoreErr error
	streamData string
	streamErr  error

	// unscopedTriggerCalls / forCentralCalls record which trigger method
	// the handler invoked and, for the scoped call, which central name it
	// was given — so tests can assert the handler routed to the right one.
	unscopedTriggerCalls int
	forCentralCalls      []string
}

func (s *stubBackupService) TriggerBackup(_ context.Context) (string, error) {
	s.unscopedTriggerCalls++
	return s.triggerID, s.triggerErr
}

func (s *stubBackupService) List(_ context.Context) ([]BackupEntry, error) {
	return s.listResult, s.listErr
}

func (s *stubBackupService) Restore(_ context.Context, _ string) (string, error) {
	return s.restoreID, s.restoreErr
}

func (s *stubBackupService) TriggerBackupForCentral(_ context.Context, centralName string) (string, error) {
	s.forCentralCalls = append(s.forCentralCalls, centralName)
	return s.triggerID, s.triggerErr
}

func (s *stubBackupService) Prune(_ context.Context, _ string, _ int) error { return nil }

// Stream writes streamData (if any) before evaluating streamErr, so a case
// can exercise a failure that happens after payload bytes are already on
// the wire — as opposed to a failure before any byte is written, which is
// streamData left empty with streamErr set.
func (s *stubBackupService) Stream(_ context.Context, _ string, w io.Writer) error {
	if s.streamData != "" {
		if _, err := w.Write([]byte(s.streamData)); err != nil {
			return err
		}
	}
	if s.streamErr != nil {
		return s.streamErr
	}
	return nil
}

// chiContext returns a context with chi URL params attached.
// Shared by all handler tests in this package.
func chiContext(r *http.Request, params map[string]string) context.Context {
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	return context.WithValue(r.Context(), chi.RouteCtxKey, rctx)
}

func TestTriggerBackup_HappyPath(t *testing.T) {
	t.Parallel()
	svc := &stubBackupService{triggerID: "backup-001"}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/backups", http.NoBody)
	w := httptest.NewRecorder()
	TriggerBackup(svc).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	if loc := w.Header().Get("Location"); loc != "/api/v1/backups/backup-001" {
		t.Fatalf("expected Location header, got %q", loc)
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["id"] != "backup-001" {
		t.Fatalf("expected id=backup-001, got %q", body["id"])
	}
}

func TestTriggerBackup_ServiceNil_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/backups", http.NoBody)
	w := httptest.NewRecorder()
	TriggerBackup(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestTriggerBackup_ServiceError_Returns502(t *testing.T) {
	t.Parallel()
	svc := &stubBackupService{triggerErr: errors.New("CCU offline")}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/backups", http.NoBody)
	w := httptest.NewRecorder()
	TriggerBackup(svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", w.Code)
	}
}

// TestTriggerBackup_NoBody_CallsUnscopedTrigger locks in the
// backward-compatible default: a bare `POST /backups` (no body) still
// backs up the first registered central via TriggerBackup, not
// TriggerBackupForCentral.
func TestTriggerBackup_NoBody_CallsUnscopedTrigger(t *testing.T) {
	t.Parallel()
	svc := &stubBackupService{triggerID: "backup-001"}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/backups", http.NoBody)
	w := httptest.NewRecorder()
	TriggerBackup(svc).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.unscopedTriggerCalls != 1 {
		t.Errorf("unscopedTriggerCalls = %d, want 1", svc.unscopedTriggerCalls)
	}
	if len(svc.forCentralCalls) != 0 {
		t.Errorf("forCentralCalls = %v, want none", svc.forCentralCalls)
	}
}

// TestTriggerBackup_WithCentralName_CallsTriggerBackupForCentral is the
// REST-side half of the B2 multi-CCU fix: a body carrying central_name
// must route to TriggerBackupForCentral with exactly that name, not the
// unscoped (first-central) trigger.
func TestTriggerBackup_WithCentralName_CallsTriggerBackupForCentral(t *testing.T) {
	t.Parallel()
	svc := &stubBackupService{triggerID: "beta-20260101-000000"}
	body := strings.NewReader(`{"central_name":"beta"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/backups", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	TriggerBackup(svc).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.unscopedTriggerCalls != 0 {
		t.Errorf("unscopedTriggerCalls = %d, want 0", svc.unscopedTriggerCalls)
	}
	if want := []string{"beta"}; len(svc.forCentralCalls) != 1 || svc.forCentralCalls[0] != want[0] {
		t.Errorf("forCentralCalls = %v, want %v", svc.forCentralCalls, want)
	}
}

// TestTriggerBackup_MalformedBody_Returns400 checks that an unparsable
// body is rejected as a client error, not silently ignored (which would
// mask an operator typo as an unscoped, wrong-central trigger).
func TestTriggerBackup_MalformedBody_Returns400(t *testing.T) {
	t.Parallel()
	svc := &stubBackupService{triggerID: "backup-001"}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/backups", strings.NewReader(`{not json`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	TriggerBackup(svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.unscopedTriggerCalls != 0 || len(svc.forCentralCalls) != 0 {
		t.Error("service must not be invoked when the body fails to decode")
	}
}

func TestListBackups_HappyPath(t *testing.T) {
	t.Parallel()
	svc := &stubBackupService{
		listResult: []BackupEntry{
			{ID: "b1", Central: "ccu-01", Bytes: 1024},
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/backups", http.NoBody)
	w := httptest.NewRecorder()
	ListBackups(svc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body []BackupEntry
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 1 || body[0].ID != "b1" {
		t.Fatalf("unexpected body: %+v", body)
	}
}

func TestListBackups_ServiceNil_ReturnsEmpty(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/backups", http.NoBody)
	w := httptest.NewRecorder()
	ListBackups(nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body []BackupEntry
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if len(body) != 0 {
		t.Fatalf("expected empty list, got %+v", body)
	}
}

func TestListBackups_ServiceError_Returns502(t *testing.T) {
	t.Parallel()
	svc := &stubBackupService{listErr: errors.New("DB error")}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/backups", http.NoBody)
	w := httptest.NewRecorder()
	ListBackups(svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", w.Code)
	}
}

func TestRestoreBackup_HappyPath(t *testing.T) {
	t.Parallel()
	svc := &stubBackupService{restoreID: "b1"}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/backups/b1/restore", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"id": "b1"}))
	w := httptest.NewRecorder()
	RestoreBackup(svc).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestRestoreBackup_ServiceNil_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/backups/b1/restore", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"id": "b1"}))
	w := httptest.NewRecorder()
	RestoreBackup(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestRestoreBackup_ServiceError_Returns502(t *testing.T) {
	t.Parallel()
	svc := &stubBackupService{restoreErr: errors.New("restore failed")}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/backups/b1/restore", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"id": "b1"}))
	w := httptest.NewRecorder()
	RestoreBackup(svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", w.Code)
	}
}

func TestDownloadBackup_HappyPath(t *testing.T) {
	t.Parallel()
	svc := &stubBackupService{streamData: "SBKDATA"}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/backups/b1/download", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"id": "b1"}))
	w := httptest.NewRecorder()
	DownloadBackup(svc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Fatalf("expected octet-stream, got %q", ct)
	}
	if !strings.Contains(w.Header().Get("Content-Disposition"), "b1.sbk") {
		t.Fatalf("unexpected Content-Disposition: %q", w.Header().Get("Content-Disposition"))
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("SBKDATA")) {
		t.Fatalf("body does not contain stream data")
	}
}

// TestDownloadBackup_IDWithQuoteIsEscapedInContentDisposition verifies
// that a backup id containing a double quote cannot break out of the
// filename parameter and inject extra Content-Disposition directives
// — the header must stay parseable, and the parsed filename value
// must round-trip the id verbatim (quote included) rather than being
// silently truncated at the injected quote.
func TestDownloadBackup_IDWithQuoteIsEscapedInContentDisposition(t *testing.T) {
	t.Parallel()
	trickyID := `evil"; filename="pwned.sh`
	svc := &stubBackupService{streamData: "SBKDATA"}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/backups/x/download", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"id": trickyID}))
	w := httptest.NewRecorder()
	DownloadBackup(svc).ServeHTTP(w, req)

	cd := w.Header().Get("Content-Disposition")
	_, params, err := mime.ParseMediaType(cd)
	if err != nil {
		t.Fatalf("Content-Disposition is not parseable: %q: %v", cd, err)
	}
	wantFilename := trickyID + ".sbk"
	if params["filename"] != wantFilename {
		t.Errorf("filename param = %q, want %q (header: %q)", params["filename"], wantFilename, cd)
	}
}

func TestDownloadBackup_ServiceNil_Returns503(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/backups/b1/download", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"id": "b1"}))
	w := httptest.NewRecorder()
	DownloadBackup(nil).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// DownloadBackup — the 200 is deferred until the first payload byte
// ---------------------------------------------------------------------------

// TestDownloadBackupReportsAFailureBeforeTheFirstByteAsAnError verifies
// that a stream failing before it writes anything is answered as an
// ordinary problem+json error, not as a committed response with a broken
// or empty body. Before [downloadBody] deferred the 200 and the download
// headers until the first payload byte, DownloadBackup wrote them up
// front, so this exact case — Stream failing before any byte — could not
// be reported at all: the client already had a 200 on the wire.
func TestDownloadBackupReportsAFailureBeforeTheFirstByteAsAnError(t *testing.T) {
	t.Parallel()
	svc := &stubBackupService{streamErr: errors.New("upstream unavailable")}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/backups/b1/download", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"id": "b1"}))
	w := httptest.NewRecorder()
	DownloadBackup(svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body=%s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != problem.ContentType {
		t.Fatalf("Content-Type = %q, want %q", ct, problem.ContentType)
	}
	var body problem.Details
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("body does not parse as a problem document: %v (body=%s)", err, w.Body.String())
	}
	if body.Status != http.StatusBadGateway {
		t.Errorf("problem status = %d, want %d", body.Status, http.StatusBadGateway)
	}
	if got := w.Body.Bytes(); len(got) == 0 || got[0] != '{' {
		t.Fatalf("body must be the JSON problem document, not archive bytes, got %q", got)
	}
}

// TestDownloadBackupDoesNotAppendAProblemDocumentToAPartialArchive covers
// the failure mode adjacent to the one above: once the first payload byte
// has committed the 200, a later Stream failure can no longer be turned
// into a different status. The handler must abort the connection
// (panic(http.ErrAbortHandler), which net/http itself recovers and
// reports to the client as a truncated transfer) instead of appending a
// JSON problem document to the tail of a partial .sbk — the old behaviour
// turned a detectable truncation into a file that read as a complete,
// valid archive an operator could then push at a CCU.
func TestDownloadBackupDoesNotAppendAProblemDocumentToAPartialArchive(t *testing.T) {
	t.Parallel()
	svc := &stubBackupService{streamData: "PARTIALARCHIVEBYTES", streamErr: errors.New("connection reset mid-stream")}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/backups/b1/download", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"id": "b1"}))
	w := httptest.NewRecorder()

	recovered := func() (v any) {
		defer func() { v = recover() }()
		DownloadBackup(svc).ServeHTTP(w, req)
		return nil
	}()

	if err, ok := recovered.(error); !ok || !errors.Is(err, http.ErrAbortHandler) {
		t.Fatalf("expected panic(http.ErrAbortHandler), got %#v", recovered)
	}
	if w.Body.String() != "PARTIALARCHIVEBYTES" {
		t.Fatalf("body must contain only the bytes the stream wrote, no appended problem document, got %q", w.Body.String())
	}
}

// TestDownloadBackupSetsTheDownloadHeadersOnASuccessfulStream pins the
// full happy-path contract in one assertion set: 200, the octet-stream
// content type, an attachment disposition naming "<id>.sbk", and a body
// equal to exactly what the stream wrote.
func TestDownloadBackupSetsTheDownloadHeadersOnASuccessfulStream(t *testing.T) {
	t.Parallel()
	svc := &stubBackupService{streamData: "CCU-BACKUP-BYTES"}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/backups/snap42/download", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"id": "snap42"}))
	w := httptest.NewRecorder()
	DownloadBackup(svc).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Fatalf("Content-Type = %q, want application/octet-stream", ct)
	}
	cd := w.Header().Get("Content-Disposition")
	if !strings.Contains(cd, "attachment") || !strings.Contains(cd, "snap42.sbk") {
		t.Fatalf("Content-Disposition = %q, want an attachment naming snap42.sbk", cd)
	}
	if w.Body.String() != "CCU-BACKUP-BYTES" {
		t.Fatalf("body = %q, want the streamed bytes verbatim", w.Body.String())
	}
}

// TestDownloadBackupRejectsAnEmptyStream verifies a Stream that returns
// nil having written nothing is answered 502, not a 200 zero-byte
// download. A zero-byte "download" reads to an operator as a complete,
// if useless, backup — the distinction only becomes visible at restore
// time, which is exactly the point at which a mistake is expensive.
func TestDownloadBackupRejectsAnEmptyStream(t *testing.T) {
	t.Parallel()
	svc := &stubBackupService{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/backups/b1/download", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"id": "b1"}))
	w := httptest.NewRecorder()
	DownloadBackup(svc).ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body=%s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != problem.ContentType {
		t.Fatalf("Content-Type = %q, want %q", ct, problem.ContentType)
	}
}

// ---------------------------------------------------------------------------
// UploadBackup
// ---------------------------------------------------------------------------

// fakeBackupUploader implements BackupUploader for the UploadBackup handler
// tests. It records the payload it was given and returns a configurable
// entry/error.
type fakeBackupUploader struct {
	calls    int
	lastName string
	lastData []byte
	entry    hmapi.BackupEntry
	err      error
}

func (f *fakeBackupUploader) SaveUploaded(_ context.Context, filename string, data []byte) (hmapi.BackupEntry, error) {
	f.calls++
	f.lastName = filename
	f.lastData = data
	return f.entry, f.err
}

// buildValidSbkBytes assembles a minimal but structurally valid CCU backup
// archive: an uncompressed tar carrying the configuration payload, its
// signature, and a firmware_version member sbk.Inspect can parse.
func buildValidSbkBytes(t *testing.T) []byte {
	t.Helper()
	members := []struct{ name, body string }{
		{"usr_local.tar.gz", "config-archive-bytes"},
		{"signature", "sig-bytes"},
		{"firmware_version", "VERSION=3.89.8.20260719\nPRODUCT=HM-CCU3\n"},
		{"key_index", "1"},
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, m := range members {
		if err := tw.WriteHeader(&tar.Header{Name: m.name, Mode: 0o644, Size: int64(len(m.body))}); err != nil {
			t.Fatalf("write tar header %s: %v", m.name, err)
		}
		if _, err := tw.Write([]byte(m.body)); err != nil {
			t.Fatalf("write tar body %s: %v", m.name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}
	return buf.Bytes()
}

// multipartBackupRequest builds a POST /api/v1/backups/upload request whose
// body is multipart/form-data with a single `file` part carrying data.
func multipartBackupRequest(t *testing.T, data []byte) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", "backup.sbk")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/backups/upload", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	return req
}

// TestUploadBackup_NonMultipartBody_Returns400 verifies a request that
// never claims to be multipart/form-data is rejected before any part is
// read, rather than being (mis)parsed as an empty upload.
func TestUploadBackup_NonMultipartBody_Returns400(t *testing.T) {
	t.Parallel()
	svc := &fakeBackupUploader{}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/backups/upload", strings.NewReader("just some bytes"))
	req.Header.Set("Content-Type", "application/octet-stream")
	w := httptest.NewRecorder()
	UploadBackup(svc, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.calls != 0 {
		t.Fatal("service must not be called for a non-multipart body")
	}
}

// TestUploadBackup_MissingFilePart_Returns400 verifies a well-formed
// multipart body that never carries a `file` part is rejected, rather than
// silently proceeding with a zero-byte archive.
func TestUploadBackup_MissingFilePart_Returns400(t *testing.T) {
	t.Parallel()
	svc := &fakeBackupUploader{}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	field, err := w.CreateFormField("comment")
	if err != nil {
		t.Fatalf("CreateFormField: %v", err)
	}
	if _, err := field.Write([]byte("no file attached")); err != nil {
		t.Fatalf("write field: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/backups/upload", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())

	rr := httptest.NewRecorder()
	UploadBackup(svc, nil).ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rr.Code, rr.Body.String())
	}
	if svc.calls != 0 {
		t.Fatal("service must not be called when the file part is missing")
	}
}

// TestUploadBackup_InvalidArchive_Returns422 verifies a multipart upload
// whose `file` part is not a CCU system backup is rejected by inspection
// before the storage layer ever sees it — an operator picking the wrong
// file learns immediately, not at restore time.
func TestUploadBackup_InvalidArchive_Returns422(t *testing.T) {
	t.Parallel()
	svc := &fakeBackupUploader{}
	req := multipartBackupRequest(t, []byte("this is not a tar archive at all"))
	rr := httptest.NewRecorder()
	UploadBackup(svc, nil).ServeHTTP(rr, req)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d body=%s", rr.Code, rr.Body.String())
	}
	if svc.calls != 0 {
		t.Fatal("service must not be called for an archive that fails inspection")
	}
}

// TestUploadBackup_ValidArchive_Returns201WithEntryAndAudits verifies the
// happy path: a structurally valid archive is stored, the response carries
// both the stored entry and the firmware_version the archive reported, and
// the upload is audited.
func TestUploadBackup_ValidArchive_Returns201WithEntryAndAudits(t *testing.T) {
	t.Parallel()
	svc := &fakeBackupUploader{entry: hmapi.BackupEntry{ID: "upload-20260731-120000.000", Bytes: 999}}
	rec := &captureRecorder{}
	req := multipartBackupRequest(t, buildValidSbkBytes(t))
	rr := httptest.NewRecorder()
	UploadBackup(svc, rec).ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", rr.Code, rr.Body.String())
	}
	var body uploadedBackupResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.ID != "upload-20260731-120000.000" {
		t.Errorf("ID = %q, want the stored entry id", body.ID)
	}
	if body.FirmwareVersion != "3.89.8.20260719" {
		t.Errorf("FirmwareVersion = %q, want 3.89.8.20260719", body.FirmwareVersion)
	}
	if body.Product != "HM-CCU3" {
		t.Errorf("Product = %q, want HM-CCU3", body.Product)
	}
	if svc.calls != 1 {
		t.Fatalf("expected 1 SaveUploaded call, got %d", svc.calls)
	}
	if len(rec.entries) != 1 {
		t.Fatalf("expected 1 audit entry, got %d", len(rec.entries))
	}
	if got := rec.entries[0]; got.Action != audit.ActionBackupUpload || got.Note != "upload-20260731-120000.000" {
		t.Fatalf("audit entry mismatch: %+v", got)
	}
}
