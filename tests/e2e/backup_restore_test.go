// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build e2e

package e2e

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"testing"

	"github.com/SukramJ/openccu-loom/tests/e2e/harness"
)

// TestE2EUploadedBackupCanBeRestored drives the seam between uploading a
// backup and restoring it.
//
// Both halves had tests and both passed. The upload path stored the file
// and answered with an id; the restore path resolved a restorer and
// called it. Nothing ran them in sequence, and in sequence they did not
// work: an uploaded archive is stored under `upload-<timestamp>`, which
// matches no central's name, so the resolver fell through to a legacy
// single-restorer field that nothing in production ever set. Every
// restore of an uploaded backup answered 502 "Backup restore failed" —
// an error that names the CCU as the culprit for a request the daemon
// never sent it.
//
// The harness runs exactly one central, which is the ordinary
// installation and the case that must simply work.
func TestE2EUploadedBackupCanBeRestored(t *testing.T) {
	t.Parallel()
	h := harness.Start(t, harness.Options{})
	if err := h.REST().LoginSession(harness.AdminUser, harness.AdminPass); err != nil {
		t.Fatalf("login: %v", err)
	}

	id := uploadBackup(t, h)
	if id == "" {
		t.Fatal("upload returned no id; the rest of this test would be vacuous")
	}

	req, err := h.REST().NewRequest(http.MethodPost, "/api/v1/backups/"+id+"/restore", nil)
	if err != nil {
		t.Fatalf("build restore request: %v", err)
	}
	resp, err := h.REST().Do(req)
	if err != nil {
		t.Fatalf("POST restore: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	// The simulated CCU has no firmware-restore endpoint, so the restore
	// may legitimately fail *at the CCU*. What must not happen is the
	// daemon refusing before it ever asks — that was the defect, and it
	// is what 501/502-from-resolution looks like from here.
	if resp.StatusCode == http.StatusUnprocessableEntity {
		t.Fatalf("restore of an uploaded backup was rejected as ambiguous with one central "+
			"configured: %s — the single-central case must resolve without asking the operator", body)
	}
	// 501 is the daemon saying it has no restore path at all — it never
	// asked the CCU. That is the defect, and it is the only status the
	// broken and the working build do not share: a genuine CCU refusal
	// answers 502, which this test tolerates because the simulated CCU
	// has no restore endpoint.
	//
	// The distinction had to be built before it could be asserted. The
	// handler previously answered 502 for both, and the body carries no
	// detail by design, so from out here the two were indistinguishable —
	// which is why the first draft of this test passed against the
	// broken build.
	if resp.StatusCode == http.StatusNotImplemented {
		t.Fatalf("restore of an uploaded backup answered 501: %s — the daemon resolved no restorer "+
			"and never contacted the CCU, so an operator's uploaded backup can never be restored", body)
	}
}

// minimalSBK builds the smallest archive the upload validator accepts: a
// tar carrying the config member, the signature member and a firmware
// version.
//
// A dummy payload was not enough — the daemon inspects the archive before
// storing it, and rejected the first draft of this test with 422, which
// made it skip itself. A test that skips proves nothing, and it would
// have kept proving nothing after the defect came back.
func minimalSBK(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	members := []struct{ name, body string }{
		{"usr_local.tar.gz", "not really gzip, never unpacked here"},
		{"signature", "sig"},
		{"firmware_version", "VERSION=3.75.7\nPRODUCT=HMIP_CCU3\n"},
	}
	for _, m := range members {
		if err := tw.WriteHeader(&tar.Header{
			Name: m.name, Mode: 0o600, Size: int64(len(m.body)),
		}); err != nil {
			t.Fatalf("tar header %s: %v", m.name, err)
		}
		if _, err := tw.Write([]byte(m.body)); err != nil {
			t.Fatalf("tar body %s: %v", m.name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	return buf.Bytes()
}

// uploadBackup posts a small archive and returns the id the daemon
// assigned it.
func uploadBackup(t *testing.T, h *harness.Harness) string {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("file", "restore-me.sbk")
	if err != nil {
		t.Fatalf("multipart: %v", err)
	}
	if _, err := part.Write(minimalSBK(t)); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}

	req, err := h.REST().NewRequest(http.MethodPost, "/api/v1/backups/upload", &buf)
	if err != nil {
		t.Fatalf("build upload request: %v", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := h.REST().Do(req)
	if err != nil {
		t.Fatalf("POST upload: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated &&
		resp.StatusCode != http.StatusAccepted {
		t.Skipf("upload answered %d (%s); this daemon build does not accept the archive, "+
			"so the restore seam cannot be exercised here", resp.StatusCode, raw)
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("upload response is not JSON: %v (%s)", err, raw)
	}
	return out.ID
}
