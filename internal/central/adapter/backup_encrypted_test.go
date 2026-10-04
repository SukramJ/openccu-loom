// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/SukramJ/godevccu/pkg/litefake"

	"github.com/SukramJ/openccu-loom/internal/backup/sbk"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// encryptedStandIn starts like a binary age file. The rest is opaque without
// the key, so a stand-in is enough: the daemon must never look past the
// header anyway.
const encryptedStandIn = "age-encryption.org/v1\n-> X25519 c3RhbmQtaW4\n--- c3RhbmQtaW4\n\x00\x01\x02\x03"

// encryptedCapableRestorer is a [stubBackupRestorer] that declares
// [EncryptedArchiveRestorer], the way the openccu-lite restorer does.
type encryptedCapableRestorer struct{ stubBackupRestorer }

func (*encryptedCapableRestorer) RestoresEncryptedArchives() bool { return true }

// TestBackupAdapterRestoreHandsEncryptedArchiveToCapableRestorer pins that
// an encrypted archive reaches a restorer that opens it itself, byte for
// byte, instead of being refused by an inspection that cannot read it.
func TestBackupAdapterRestoreHandsEncryptedArchiveToCapableRestorer(t *testing.T) {
	t.Parallel()
	a := NewBackupAdapter(newRegistryForBackupTest(t))
	a.SetStorage(&stubBackupStorage{content: map[string]string{"bk1": encryptedStandIn}})
	restorer := &encryptedCapableRestorer{stubBackupRestorer{jobID: "job-enc"}}
	a.SetRestorer(restorer)

	jobID, err := a.Restore(context.Background(), "bk1")
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if jobID != "job-enc" {
		t.Errorf("jobID = %q, want job-enc", jobID)
	}
	if string(restorer.capturedPayload) != encryptedStandIn {
		t.Errorf("restorer got %d bytes, want the stored encrypted archive unchanged (%d bytes)",
			len(restorer.capturedPayload), len(encryptedStandIn))
	}
}

// TestBackupAdapterRestoreRefusesEncryptedArchiveForOtherTargets pins the
// other side: a target that does not open encrypted archives (a CCU, whose
// restore unpacks whatever it is sent) never receives one, and the refusal
// is a validation error the REST layer answers with 422.
func TestBackupAdapterRestoreRefusesEncryptedArchiveForOtherTargets(t *testing.T) {
	t.Parallel()
	a := NewBackupAdapter(newRegistryForBackupTest(t))
	a.SetStorage(&stubBackupStorage{content: map[string]string{"bk1": encryptedStandIn}})
	restorer := &stubBackupRestorer{jobID: "job-should-never-run"}
	a.SetRestorer(restorer)

	_, err := a.Restore(context.Background(), "bk1")
	if !errors.Is(err, hmerr.ErrValidation) {
		t.Fatalf("Restore = %v, want hmerr.ErrValidation", err)
	}
	if !strings.Contains(err.Error(), "openccu-lite") {
		t.Errorf("refusal %q does not name where the archive can be restored", err)
	}
	if restorer.capturedID != "" {
		t.Errorf("restorer was called with %q; an encrypted archive must not reach it", restorer.capturedID)
	}
}

// TestBackupAdapterRestoreStillInspectsPlainArchiveForCapableRestorer pins
// that the capability skips the inspection only for an encrypted archive: a
// plain archive that is not a backup is refused even when the target is a
// box.
func TestBackupAdapterRestoreStillInspectsPlainArchiveForCapableRestorer(t *testing.T) {
	t.Parallel()
	a := NewBackupAdapter(newRegistryForBackupTest(t))
	a.SetStorage(&stubBackupStorage{content: map[string]string{"bk1": "plain bytes, not a tar and not encrypted"}})
	restorer := &encryptedCapableRestorer{stubBackupRestorer{jobID: "job-should-never-run"}}
	a.SetRestorer(restorer)

	_, err := a.Restore(context.Background(), "bk1")
	if !errors.Is(err, sbk.ErrNotAnArchive) {
		t.Fatalf("Restore = %v, want sbk.ErrNotAnArchive", err)
	}
	if restorer.capturedID != "" {
		t.Errorf("restorer was called with %q for an archive inspection refused", restorer.capturedID)
	}
}

// TestBackupAdapterAcceptsEncryptedBackupsFollowsWiredRestorers pins the
// upload gate's source: true only while some central's restorer opens
// encrypted archives.
func TestBackupAdapterAcceptsEncryptedBackupsFollowsWiredRestorers(t *testing.T) {
	t.Parallel()
	a := NewBackupAdapter(newRegistryForBackupTest(t))
	if a.AcceptsEncryptedBackups() {
		t.Error("no restorer wired: AcceptsEncryptedBackups = true")
	}
	a.SetRestorerForCentral("ccu", &stubBackupRestorer{})
	if a.AcceptsEncryptedBackups() {
		t.Error("only a CCU restorer wired: AcceptsEncryptedBackups = true")
	}
	a.SetRestorerForCentral("box", &encryptedCapableRestorer{})
	if !a.AcceptsEncryptedBackups() {
		t.Error("a box restorer wired: AcceptsEncryptedBackups = false")
	}
}

// TestFilesystemSaveUploadedNamesEncryptedArchive pins that an encrypted
// upload lists with a generated `.sbk.age` name — the suffix the SPA marks
// as encrypted — while a plain upload keeps no display name.
func TestFilesystemSaveUploadedNamesEncryptedArchive(t *testing.T) {
	t.Parallel()
	st, err := NewFilesystemBackupStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewFilesystemBackupStorage: %v", err)
	}
	ctx := context.Background()
	entry, err := st.SaveUploaded(ctx, "../evil\r\n.sbk.age", []byte(encryptedStandIn))
	if err != nil {
		t.Fatalf("SaveUploaded: %v", err)
	}
	want := entry.ID + sbk.Extension + sbk.EncryptedSuffix
	if entry.Filename != want {
		t.Errorf("entry Filename = %q, want %q", entry.Filename, want)
	}
	list, err := st.List(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("List = %v, %v; want one entry", list, err)
	}
	if list[0].Filename != want {
		t.Errorf("listed Filename = %q, want %q", list[0].Filename, want)
	}

	plain, err := st.SaveUploaded(ctx, "x.sbk", []byte(validBackupArchive(t)))
	if err != nil {
		t.Fatalf("SaveUploaded plain: %v", err)
	}
	if plain.Filename != "" {
		t.Errorf("plain upload Filename = %q, want empty", plain.Filename)
	}
}

// TestLiteRestorerRestoresEncryptedArchives pins that the restorer the lite
// profile's bring-up hands to the backup wiring — the value
// [HubSession.Restorer] returns, not a constructed stand-in — declares the
// capability, and that the CCU restorer does not.
func TestLiteRestorerRestoresEncryptedArchives(t *testing.T) {
	t.Parallel()
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
	if !restoresEncrypted(r) {
		t.Errorf("lite restorer %T does not declare EncryptedArchiveRestorer", r)
	}
	if restoresEncrypted(&HTTPBackupRestorer{}) {
		t.Error("the CCU restorer declares EncryptedArchiveRestorer; a CCU would unpack an encrypted archive blind")
	}
}
