// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/SukramJ/godevccu/pkg/litefake"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// liteRestorerFor is the restorer the lite profile's bring-up hands to
// the backup wiring, against a fresh fake.
func liteRestorerFor(t *testing.T) (BackupRestorer, *litefake.Fake) {
	t.Helper()
	f := startTestFake(t, litefake.Options{Meta: litefake.DefaultMeta()})
	cc := liteCentralFor(t, f, litefake.DefaultToken)
	p, err := newLiteProfile(cc, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("newLiteProfile: %v", err)
	}
	unit, err := central.New(central.Config{Name: cc.Name})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	session, err := p.BringUpHub(context.Background(), HubBringUpInput{CC: cc, Unit: unit})
	if err != nil {
		t.Fatalf("BringUpHub: %v", err)
	}
	t.Cleanup(session.Close)
	r := session.Restorer()
	if r == nil {
		t.Fatal("lite session offers no restorer")
	}
	return r, f
}

// TestLiteRestoreAppliesTheBoxOwnBackup pins the restore round trip
// against the box's rules: the apply carries the confirmation the box
// requires, so a backup the box made is applied.
func TestLiteRestoreAppliesTheBoxOwnBackup(t *testing.T) {
	t.Parallel()
	r, f := liteRestorerFor(t)
	id, err := r.Restore(context.Background(), "bk1", bytes.NewReader(litefake.BackupBlob()))
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if id != "bk1" {
		t.Errorf("Restore id = %q, want bk1", id)
	}
	if got := f.Restores(); len(got) != 1 {
		t.Errorf("box applied %v, want exactly one archive", got)
	}
}

// TestLiteRestoreRefusesABackupOfAnotherSystem pins that a CCU's backup
// is refused as a validation error naming what the file is, and that the
// box applies nothing.
func TestLiteRestoreRefusesABackupOfAnotherSystem(t *testing.T) {
	t.Parallel()
	r, f := liteRestorerFor(t)
	_, err := r.Restore(context.Background(), "bk1", bytes.NewReader(litefake.CCUBackupBlob()))
	if !errors.Is(err, hmerr.ErrValidation) {
		t.Fatalf("Restore err = %v, want ErrValidation", err)
	}
	if !strings.Contains(err.Error(), "OpenCCU") {
		t.Errorf("Restore err = %q, want it to name the backup's system", err)
	}
	if got := f.Restores(); len(got) != 0 {
		t.Errorf("box applied %v, want nothing", got)
	}
}

// TestLiteRestoreRefusesAFileThatIsNoBackup pins that a file the box
// cannot read as an archive is refused as a validation error.
func TestLiteRestoreRefusesAFileThatIsNoBackup(t *testing.T) {
	t.Parallel()
	r, f := liteRestorerFor(t)
	_, err := r.Restore(context.Background(), "bk1", strings.NewReader("not a tar archive"))
	if !errors.Is(err, hmerr.ErrValidation) {
		t.Fatalf("Restore err = %v, want ErrValidation", err)
	}
	if got := f.Restores(); len(got) != 0 {
		t.Errorf("box applied %v, want nothing", got)
	}
}
