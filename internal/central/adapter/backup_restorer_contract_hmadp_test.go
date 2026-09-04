// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"io"
	"mime"
	"mime/multipart"
	"net/url"
	"strings"
	"testing"
)

// TestHmAdpBackupUploadUsesTheFirmwareActionVerb pins the action the CGI
// actually dispatches on.
//
// www/config/cp_security.cgi dispatches by string concatenation
// (`action_$action`) and defines exactly three backup procs:
// action_backup_upload (:1510), action_backup_restore_check (:373) and
// action_backup_restore_go (:511). There is no action_restore_backup anywhere
// in the firmware tree, so that verb reaches no handler at all.
//
// The `@`-wrapping of the session id is the one part the previous contract had
// right and is pinned here too: the canonical session id literally carries the
// delimiters (www/tcl/eq3_old/session.tcl:464) and the query parser only
// accepts an @-wrapped value (:312).
func TestHmAdpBackupUploadUsesTheFirmwareActionVerb(t *testing.T) {
	t.Parallel()

	r := &HTTPBackupRestorer{BaseURL: "https://ccu/"}
	raw, err := r.buildURL("ABCDEF1234")
	if err != nil {
		t.Fatalf("buildURL: %v", err)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", raw, err)
	}
	if got, want := u.Path, "/config/cp_security.cgi"; got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
	if got, want := u.Query().Get("action"), "backup_upload"; got != want {
		t.Fatalf("action = %q, want %q — no action_%s proc exists in the firmware", got, want, got)
	}
	if got, want := u.Query().Get("sid"), "@ABCDEF1234@"; got != want {
		t.Fatalf("sid = %q, want %q", got, want)
	}
}

// TestHmAdpBackupUploadUsesTheFirmwareFieldName pins the multipart field the
// receiver reads: action_backup_upload's body is `import_file -client
// backup_file` (cp_security.cgi:1515), and the WebUI's own form declares
// `file_button backup_file` (:1028). A field named anything else is simply not
// imported.
func TestHmAdpBackupUploadUsesTheFirmwareFieldName(t *testing.T) {
	t.Parallel()

	body, contentType, err := buildMultipart("backup-001", strings.NewReader("payload"))
	if err != nil {
		t.Fatalf("buildMultipart: %v", err)
	}
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		t.Fatalf("ParseMediaType(%q): %v", contentType, err)
	}
	mr := multipart.NewReader(body, params["boundary"])

	fileFields := map[string]string{}
	values := map[string]string{}
	for {
		part, perr := mr.NextPart()
		if perr == io.EOF {
			break
		}
		if perr != nil {
			t.Fatalf("NextPart: %v", perr)
		}
		data, _ := io.ReadAll(part)
		if part.FileName() != "" {
			fileFields[part.FormName()] = part.FileName()
			continue
		}
		values[part.FormName()] = string(data)
	}

	if _, ok := fileFields["backup_file"]; !ok {
		t.Fatalf("multipart carries file fields %v, want a field named backup_file — the CGI reads `import_file -client backup_file`", fileFields)
	}
	if got, want := values["action"], "backup_upload"; got != want {
		t.Fatalf("multipart action field = %q, want %q", got, want)
	}
}
