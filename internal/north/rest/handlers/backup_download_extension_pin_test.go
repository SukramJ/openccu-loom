// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/backup/sbk"
)

// TestDownloadFilenameFallbackCarriesArchiveExtension pins the served
// filename's suffix to [sbk.Extension] on both fallback paths — a failed
// listing and an id with no recorded name — so the download name cannot
// drift away from what the storage writes.
func TestDownloadFilenameFallbackCarriesArchiveExtension(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		svc  BackupService
	}{
		{name: "list fails", svc: &stubBackupService{listErr: errors.New("boom")}},
		{name: "no recorded name", svc: &stubBackupService{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := downloadFilename(context.Background(), tc.svc, "pin-id")
			if !strings.HasSuffix(got, sbk.Extension) {
				t.Fatalf("downloadFilename = %q, want a %q suffix", got, sbk.Extension)
			}
		})
	}
}
