// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	"net/url"
	"strings"
	"testing"
)

// TestHmAdpBackupUploadUsesTheFirmwareAddresses pins the two addresses of
// the firmware's restore upload, as the WebUI's own form uses them
// (OpenCCU-Base src/webui/www/config/cp_security.cgi:1045).
//
// fileupload.ccc reads its parameters from the raw QUERY_STRING without
// URL-decoding (fileupload.ccc:41-51) and accepts only the four receiver CGIs
// as `url=` and an `@`-wrapped alphanumeric session id (:60-75), so the query
// is pinned byte for byte. cp_security.cgi dispatches `action_$action`
// (cp_security.cgi:1632); action_backup_upload is the proc that stores the
// archive (:1532). The canonical session id literally carries the `@`
// delimiters (www/tcl/eq3_old/session.tcl:464).
func TestHmAdpBackupUploadUsesTheFirmwareAddresses(t *testing.T) {
	t.Parallel()

	r := &HTTPBackupRestorer{BaseURL: "https://ccu/"}
	if got, want := r.uploadURL("ABCDEF1234"),
		"https://ccu/config/fileupload.ccc?sid=@ABCDEF1234@&action=backup_upload&url=/config/cp_security.cgi"; got != want {
		t.Fatalf("upload url = %q, want %q", got, want)
	}

	raw, err := r.storeURL("ABCDEF1234")
	if err != nil {
		t.Fatalf("storeURL: %v", err)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", raw, err)
	}
	if got, want := u.Path, "/config/cp_security.cgi"; got != want {
		t.Fatalf("store path = %q, want %q", got, want)
	}
	if got, want := u.Query().Get("sid"), "@ABCDEF1234@"; got != want {
		t.Fatalf("store sid = %q, want %q", got, want)
	}
}

// TestHmAdpBackupUploadIsASingleFilePart pins the body shape fileupload.ccc
// can read. The script does not parse multipart: it skips exactly four lines
// and takes Content-Length minus those lines and the closing boundary as the
// file size (fileupload.ccc:12-33). So the body must be one part with exactly
// two header lines; a second part — the form field the old direct upload
// carried — would be written into the archive. The field name is the one the
// WebUI form declares (cp_security.cgi:1046).
func TestHmAdpBackupUploadIsASingleFilePart(t *testing.T) {
	t.Parallel()

	body, contentType, err := buildMultipart("backup-001", strings.NewReader("payload"))
	if err != nil {
		t.Fatalf("buildMultipart: %v", err)
	}
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		t.Fatalf("ParseMediaType(%q): %v", contentType, err)
	}
	mr := multipart.NewReader(bytes.NewReader(body.Bytes()), params["boundary"])

	var parts []*multipart.Part
	for {
		part, perr := mr.NextPart()
		if perr == io.EOF {
			break
		}
		if perr != nil {
			t.Fatalf("NextPart: %v", perr)
		}
		parts = append(parts, part)
		if len(part.Header) != 2 {
			t.Errorf("part %q carries %d header lines, want 2 (Content-Disposition, Content-Type)", part.FormName(), len(part.Header))
		}
	}
	if len(parts) != 1 {
		t.Fatalf("multipart carries %d parts, want exactly 1 — fileupload.ccc writes everything after the first part's headers into the archive", len(parts))
	}
	if got, want := parts[0].FormName(), "backup_file"; got != want {
		t.Fatalf("file field = %q, want %q", got, want)
	}
}
