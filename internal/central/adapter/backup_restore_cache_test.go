// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/cachereset"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
)

// recordingCacheInvalidator records the scopes a restore asked to clear.
type recordingCacheInvalidator struct {
	scopes []cachereset.Scope
	err    error
}

func (r *recordingCacheInvalidator) Clear(_ context.Context, scope cachereset.Scope) (cachereset.Report, error) {
	r.scopes = append(r.scopes, scope)
	return cachereset.Report{Scope: scope}, r.err
}

// memBackupStorage serves one archive from memory.
type memBackupStorage struct {
	id      string
	payload []byte
}

func (m *memBackupStorage) List(context.Context) ([]hmapi.BackupEntry, error) {
	return []hmapi.BackupEntry{{ID: m.id}}, nil
}

func (m *memBackupStorage) Open(_ context.Context, id string) (io.ReadCloser, error) {
	if id != m.id {
		return nil, errors.New("no such backup")
	}
	return io.NopCloser(bytes.NewReader(m.payload)), nil
}

func (m *memBackupStorage) Save(context.Context, string, string, []byte) error {
	return errors.New("not implemented")
}

func (m *memBackupStorage) Delete(context.Context, string) error { return nil }

// countingRestorer records that the archive reached the CCU.
type countingRestorer struct{ calls int }

func (c *countingRestorer) Restore(_ context.Context, id string, payload io.Reader) (string, error) {
	c.calls++
	_, _ = io.Copy(io.Discard, payload)
	return id, nil
}

// TestRestoreInvalidatesTheRestoredCentralsCaches pins that a successful
// restore clears the CCU-derivable caches of the central it restored.
//
// Without it the daemon keeps serving the pre-restore configuration for
// the lifetime of the installation, not just until the next restart:
// seedMasterValues is cache-first with an unconditional early return on
// a hit, so the persisted MASTER rows survive the CCU's own reboot and
// every subsequent daemon start. An operator who restores an older
// backup precisely to roll a device configuration back sees the daemon
// keep reporting the values they just rolled away, with no error and no
// restart-based recovery.
//
// The scope must name the restored central and nothing wider: a global
// clear would throw away the other CCUs' caches over one CCU's restore.
func TestRestoreInvalidatesTheRestoredCentralsCaches(t *testing.T) {
	t.Parallel()

	reg := central.NewRegistry()
	inv := &recordingCacheInvalidator{}
	restorer := &countingRestorer{}

	a := NewBackupAdapter(reg)
	a.SetStorage(&memBackupStorage{id: "alpha-20260701-100000", payload: validSbkArchive(t)})
	a.SetRestorerForCentral("alpha", restorer)
	a.SetCacheInvalidator(inv)

	if _, err := a.Restore(context.Background(), "alpha-20260701-100000"); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if restorer.calls != 1 {
		t.Fatalf("the archive never reached the CCU (%d restorer calls) — the assertion below "+
			"would then be measuring a restore that did not happen", restorer.calls)
	}
	if len(inv.scopes) != 1 {
		t.Fatalf("a successful restore cleared %d cache scopes, want exactly 1 — the persisted "+
			"MASTER rows survive the CCU reboot and every daemon restart, so the pre-restore "+
			"configuration is served forever", len(inv.scopes))
	}
	got := inv.scopes[0]
	if got.Kind != cachereset.ScopeCentral || got.Central != "alpha" {
		t.Errorf("cleared scope %+v, want {Kind: central, Central: alpha}", got)
	}
}

// TestRestoreDoesNotFailWhenTheCacheClearFails pins that a cache-clear
// failure never turns a completed restore into a reported failure.
//
// The archive is already on the CCU by then and the CCU is rebooting;
// answering the operator with "Backup restore failed" would name the
// wrong party and hide the one thing that did happen.
func TestRestoreDoesNotFailWhenTheCacheClearFails(t *testing.T) {
	t.Parallel()

	reg := central.NewRegistry()
	inv := &recordingCacheInvalidator{err: errors.New("store unavailable")}

	a := NewBackupAdapter(reg)
	a.SetStorage(&memBackupStorage{id: "alpha-20260701-100000", payload: validSbkArchive(t)})
	a.SetRestorerForCentral("alpha", &countingRestorer{})
	a.SetCacheInvalidator(inv)

	jobID, err := a.Restore(context.Background(), "alpha-20260701-100000")
	if err != nil {
		t.Fatalf("a failed cache clear must not fail the restore: %v", err)
	}
	if jobID == "" {
		t.Error("Restore returned no job id")
	}
	if len(inv.scopes) != 1 {
		t.Errorf("the clear was not attempted (%d scopes)", len(inv.scopes))
	}
}

// validSbkArchive builds a minimal but structurally valid CCU backup
// archive, the shape BackupAdapter.Restore inspects before uploading.
func validSbkArchive(t *testing.T) []byte {
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
