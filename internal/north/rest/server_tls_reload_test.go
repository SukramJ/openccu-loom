// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package rest

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestCertReloader_PicksUpAnOnDiskRotation pins the hot-reload the
// config field and the SPA help text promise the operator: an ACME
// client overwriting the certificate files must reach the next
// handshake without a daemon restart.
func TestCertReloader_PicksUpAnOnDiskRotation(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	certA, keyA := selfSignedPEM(t)
	certPath, keyPath := writePair(t, dir, certA, keyA)

	r, err := NewCertReloader(certPath, keyPath, nil)
	if err != nil {
		t.Fatalf("NewCertReloader: %v", err)
	}
	first, err := r.GetCertificate(nil)
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}

	certB, keyB := selfSignedPEM(t)
	writePair(t, dir, certB, keyB)
	// Make the change observable regardless of filesystem mtime
	// granularity — a real rotation is minutes apart, not microseconds.
	future := time.Now().Add(time.Minute)
	for _, p := range []string{certPath, keyPath} {
		if err := os.Chtimes(p, future, future); err != nil {
			t.Fatalf("chtimes %s: %v", p, err)
		}
	}

	rotated, err := r.GetCertificate(nil)
	if err != nil {
		t.Fatalf("GetCertificate after rotation: %v", err)
	}
	if bytes.Equal(rotated.Certificate[0], first.Certificate[0]) {
		t.Fatal("GetCertificate still serves the boot certificate after an on-disk rotation")
	}
}

// TestCertReloader_SaveAndReloadLeavesTheOldPairOnAFailedKeyWrite pins
// that the upload never leaves a mismatched cert/key pair on disk: a
// failure halfway through made the next boot fail to load the pair and
// silently fall back to plain HTTP.
func TestCertReloader_SaveAndReloadLeavesTheOldPairOnAFailedKeyWrite(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	certA, keyA := selfSignedPEM(t)
	certPath, keyPath := writePair(t, dir, certA, keyA)
	r, err := NewCertReloader(certPath, keyPath, nil)
	if err != nil {
		t.Fatalf("NewCertReloader: %v", err)
	}

	// Make the key install fail: a directory can neither be written
	// nor renamed over. This stands in for the disk-full / crash case.
	blocked := filepath.Join(dir, "blocked")
	if err := os.MkdirAll(blocked, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	r.keyPath = blocked

	certB, keyB := selfSignedPEM(t)
	if err := r.SaveAndReload(certB, keyB); err == nil {
		t.Fatal("SaveAndReload reported success although the key could not be installed")
	}

	onDisk, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatalf("read cert: %v", err)
	}
	if !bytes.Equal(onDisk, certA) {
		t.Fatal("a failed key install left the new certificate on disk next to the old key")
	}
	leftovers, err := filepath.Glob(certPath + ".tmp*")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(leftovers) != 0 {
		t.Fatalf("SaveAndReload left staging files behind: %v", leftovers)
	}
}
