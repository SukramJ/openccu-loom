// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package sbk

import (
	"archive/tar"
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"
)

// sbkMember is one entry to write into a test archive.
type sbkMember struct {
	name string
	body string
}

// buildTar assembles an uncompressed tar from members, mirroring the shape
// the CCU's own backup script produces
// (tar c usr_local.tar.gz signature firmware_version key_index).
func buildTar(t *testing.T, members ...sbkMember) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, m := range members {
		hdr := &tar.Header{Name: m.name, Mode: 0o644, Size: int64(len(m.body))}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write header %s: %v", m.name, err)
		}
		if _, err := tw.Write([]byte(m.body)); err != nil {
			t.Fatalf("write body %s: %v", m.name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}
	return buf.Bytes()
}

// fullMembers returns all four members a real CCU backup carries, in the
// order the CCU writes them.
func fullMembers() []sbkMember {
	return []sbkMember{
		{name: "usr_local.tar.gz", body: "config-archive-bytes"},
		{name: "signature", body: "sig-bytes"},
		{name: "firmware_version", body: "VERSION=3.89.8.20260719\nPRODUCT=HM-CCU3\n"},
		{name: "key_index", body: "1"},
	}
}

func TestInspectFullArchiveReportsVersionAndProduct(t *testing.T) {
	t.Parallel()
	info, err := InspectBytes(buildTar(t, fullMembers()...))
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if info.FirmwareVersion != "3.89.8.20260719" {
		t.Errorf("FirmwareVersion = %q, want 3.89.8.20260719", info.FirmwareVersion)
	}
	if info.Product != "HM-CCU3" {
		t.Errorf("Product = %q, want HM-CCU3", info.Product)
	}
	if len(info.Members) != 4 {
		t.Errorf("Members = %v, want 4 entries", info.Members)
	}
}

// TestInspectDotSlashPrefixedMembersStillMatch verifies that member names
// carrying the "./" prefix some tar writers emit are still recognised —
// the CCU's own archive occasionally comes out this way depending on the
// tool that produced it.
func TestInspectDotSlashPrefixedMembersStillMatch(t *testing.T) {
	t.Parallel()
	members := []sbkMember{
		{name: "./usr_local.tar.gz", body: "config"},
		{name: "./signature", body: "sig"},
	}
	info, err := InspectBytes(buildTar(t, members...))
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(info.Members) != 2 || info.Members[0] != "usr_local.tar.gz" || info.Members[1] != "signature" {
		t.Errorf("Members = %v, want the ./ prefix stripped", info.Members)
	}
}

// TestInspectMissingSignatureIsIncomplete verifies that an archive carrying
// the configuration payload but not its signature is rejected — a restore
// needs both.
func TestInspectMissingSignatureIsIncomplete(t *testing.T) {
	t.Parallel()
	_, err := InspectBytes(buildTar(t, sbkMember{name: "usr_local.tar.gz", body: "config"}))
	if !errors.Is(err, ErrIncomplete) {
		t.Fatalf("want ErrIncomplete, got %v", err)
	}
}

// TestInspectMissingConfigIsIncomplete is the mirror case: a signature
// without the configuration it signs is equally useless for a restore.
func TestInspectMissingConfigIsIncomplete(t *testing.T) {
	t.Parallel()
	_, err := InspectBytes(buildTar(t, sbkMember{name: "signature", body: "sig"}))
	if !errors.Is(err, ErrIncomplete) {
		t.Fatalf("want ErrIncomplete, got %v", err)
	}
}

// TestInspectRandomBytesIsNotAnArchive verifies the common operator mistake
// of uploading something that is not a tar at all is reported distinctly
// from an incomplete-but-readable archive.
func TestInspectRandomBytesIsNotAnArchive(t *testing.T) {
	t.Parallel()
	_, err := InspectBytes([]byte("this is plain text, not a tar archive at all - padding for length"))
	if !errors.Is(err, ErrNotAnArchive) {
		t.Fatalf("want ErrNotAnArchive, got %v", err)
	}
}

// TestInspectGzipBytesIsNotAnArchive covers the specific wrong-file mistake
// this package exists to catch: uploading the inner usr_local.tar.gz
// (gzip-compressed) instead of the outer uncompressed .sbk container.
func TestInspectGzipBytesIsNotAnArchive(t *testing.T) {
	t.Parallel()
	gzMagic := []byte{0x1f, 0x8b, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 'j', 'u', 'n', 'k'}
	_, err := InspectBytes(gzMagic)
	if !errors.Is(err, ErrNotAnArchive) {
		t.Fatalf("want ErrNotAnArchive, got %v", err)
	}
}

func TestInspectEmptyInputIsNotAnArchive(t *testing.T) {
	t.Parallel()
	_, err := InspectBytes(nil)
	if !errors.Is(err, ErrNotAnArchive) {
		t.Fatalf("want ErrNotAnArchive, got %v", err)
	}
}

// TestInspectMissingFirmwareVersionSucceedsWithEmptyVersion verifies that
// CCU2-era backups, which predate the firmware_version member, are still
// accepted — refusing them would be stricter than the CCU's own restore.
func TestInspectMissingFirmwareVersionSucceedsWithEmptyVersion(t *testing.T) {
	t.Parallel()
	members := []sbkMember{
		{name: "usr_local.tar.gz", body: "config"},
		{name: "signature", body: "sig"},
	}
	info, err := InspectBytes(buildTar(t, members...))
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if info.FirmwareVersion != "" || info.Product != "" {
		t.Errorf("expected empty version/product for an absent member, got %+v", info)
	}
}

// TestInspectMalformedFirmwareVersionSucceedsWithEmptyVersion verifies that
// garbage inside firmware_version never fails the whole inspection — the
// version is informational, not a gate, so it must degrade to "unknown"
// rather than blocking a structurally valid backup.
func TestInspectMalformedFirmwareVersionSucceedsWithEmptyVersion(t *testing.T) {
	t.Parallel()
	members := slices.Concat(fullMembers()[:2:2],
		[]sbkMember{{name: "firmware_version", body: "not a key=value file\n\x00\x01binary junk"}})
	info, err := InspectBytes(buildTar(t, members...))
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if info.FirmwareVersion != "" || info.Product != "" {
		t.Errorf("expected empty version/product for malformed content, got %+v", info)
	}
}

// TestInspectOversizedFirmwareVersionIsTruncatedNotFatal verifies that a
// firmware_version far larger than any genuine CCU file is bounded by
// maxFirmwareVersionBytes rather than buffered in full or rejected — the
// VERSION= line placed beyond the read limit is simply not seen.
func TestInspectOversizedFirmwareVersionIsTruncatedNotFatal(t *testing.T) {
	t.Parallel()
	huge := strings.Repeat("X", maxFirmwareVersionBytes*2) + "\nVERSION=9.9.9\n"
	members := slices.Concat(fullMembers()[:2:2], []sbkMember{{name: "firmware_version", body: huge}})
	info, err := InspectBytes(buildTar(t, members...))
	if err != nil {
		t.Fatalf("Inspect must tolerate an oversized member, got error: %v", err)
	}
	if info.FirmwareVersion != "" {
		t.Errorf("FirmwareVersion = %q, want empty (VERSION line lands beyond the read limit)", info.FirmwareVersion)
	}
}
