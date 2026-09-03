// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"mime"
	"mime/multipart"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/backup/sbk"
)

// TestBackupArchiveExtensionIsOneFact pins the three roles the archive suffix
// plays inside this package to [sbk.Extension]: what Save writes, what List
// recognises, and the filenames that leave the daemon. Each role is measured
// through its production entry point, never against a restated literal, so a
// suffix that moves in one role and not the others fails here.
func TestBackupArchiveExtensionIsOneFact(t *testing.T) {
	t.Parallel()

	t.Run("save then list round-trips the id", func(t *testing.T) {
		t.Parallel()
		st := &FilesystemBackupStorage{Dir: t.TempDir()}
		if err := st.Save(context.Background(), "pin-id", "", []byte("payload")); err != nil {
			t.Fatalf("Save: %v", err)
		}
		entries, err := st.List(context.Background())
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		got := make([]string, 0, len(entries))
		for _, e := range entries {
			got = append(got, e.ID)
		}
		if len(got) != 1 || got[0] != "pin-id" {
			t.Fatalf("Save wrote an archive List does not recognise: ids=%v", got)
		}
	})

	t.Run("restore multipart filename carries the extension", func(t *testing.T) {
		t.Parallel()
		body, contentType, err := buildMultipart("pin-id", strings.NewReader("payload"))
		if err != nil {
			t.Fatalf("buildMultipart: %v", err)
		}
		_, params, err := mime.ParseMediaType(contentType)
		if err != nil {
			t.Fatalf("ParseMediaType: %v", err)
		}
		mr := multipart.NewReader(body, params["boundary"])
		var name string
		for {
			part, err := mr.NextPart()
			if err != nil {
				break
			}
			if part.FormName() == "backup_file" {
				name = part.FileName()
			}
		}
		if !strings.HasSuffix(name, sbk.Extension) {
			t.Fatalf("restore upload filename %q does not end in %q", name, sbk.Extension)
		}
	})
}
