// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package sbk inspects CCU system-backup archives.
//
// A .sbk is an uncompressed tar the CCU builds as
//
//	tar c usr_local.tar.gz signature firmware_version key_index
//
// (occu WebUI/www/config/backup.tcl, proc create_backup). usr_local.tar.gz
// carries the configuration, signature is its crypttool signature,
// firmware_version is a copy of the CCU's /boot/VERSION, and key_index
// records which system key signed it.
//
// The daemon cannot verify the signature — that needs the CCU's key
// material — so this package deliberately checks only what it can check
// honestly: that the archive is a readable tar carrying the members a
// restore needs, and what firmware it came from. That is enough to catch
// the failure this exists for, which is an operator uploading the wrong
// file, and it mirrors what the CCU's own restore reads before deciding
// whether the backup is usable (occu WebUI/www/config/cp_security.cgi).
package sbk

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Extension is the filename suffix of a CCU system-backup archive. It is the
// one place the daemon decides what a backup file is called: the filesystem
// store recognises stored archives by it and builds their paths with it, and
// it is the suffix of both filenames that leave the daemon — the HTTP
// download name and the restore multipart name.
const Extension = ".sbk"

// Member names inside the archive, as written by the CCU.
const (
	memberConfig    = "usr_local.tar.gz"
	memberSignature = "signature"
	memberFirmware  = "firmware_version"
	memberKeyIndex  = "key_index"
)

// maxFirmwareVersionBytes bounds how much of firmware_version is read. The
// real file is a handful of KEY=VALUE lines; anything larger is malformed
// and must not be pulled into memory just to be parsed.
const maxFirmwareVersionBytes = 4 << 10

// ErrNotAnArchive reports input that is not a readable tar at all — the
// common case being a .tar.gz, an image, or a truncated download.
var ErrNotAnArchive = errors.New("sbk: not a readable tar archive")

// ErrIncomplete reports a readable tar that is missing a member the CCU's
// restore requires.
var ErrIncomplete = errors.New("sbk: archive is missing a required member")

// Info describes an inspected archive.
type Info struct {
	// FirmwareVersion is the VERSION= value from firmware_version, e.g.
	// "3.89.8.20260719". Empty when the member is absent or unparseable —
	// it is informational, not a gate.
	FirmwareVersion string
	// Product is the PRODUCT= value from firmware_version (e.g. "ova",
	// "HM-CCU3"). Empty when unknown.
	Product string
	// Members lists the archive's top-level entries in the order found,
	// so a rejection message can say what was actually inside.
	Members []string
}

// Inspect validates that r carries a CCU system backup and reports what it
// found. It reads the archive once and streams it, so the caller's copy is
// the only full copy in memory.
//
// It returns [ErrNotAnArchive] when the input is not a tar, and
// [ErrIncomplete] when the configuration archive or its signature is
// missing. A missing firmware_version is tolerated: older CCU2 backups
// predate it, and refusing them would be stricter than the CCU itself.
func Inspect(r io.Reader) (Info, error) {
	var info Info
	tr := tar.NewReader(r)
	var sawConfig, sawSignature bool
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			// A tar reader fails on the first header when the input is
			// something else entirely, which is the case worth naming.
			if len(info.Members) == 0 {
				return Info{}, fmt.Errorf("%w: %w", ErrNotAnArchive, err)
			}
			return Info{}, fmt.Errorf("sbk: reading archive: %w", err)
		}
		name := normaliseMember(hdr.Name)
		if name == "" {
			continue
		}
		info.Members = append(info.Members, name)
		switch name {
		case memberConfig:
			sawConfig = true
		case memberSignature:
			sawSignature = true
		case memberFirmware:
			info.FirmwareVersion, info.Product = parseFirmwareVersion(io.LimitReader(tr, maxFirmwareVersionBytes))
		case memberKeyIndex:
			// Recorded in Members; its content is the CCU's own key
			// bookkeeping and means nothing here.
		}
	}
	switch {
	case len(info.Members) == 0:
		return Info{}, fmt.Errorf("%w: archive is empty", ErrNotAnArchive)
	case !sawConfig:
		return Info{}, fmt.Errorf("%w: %s", ErrIncomplete, memberConfig)
	case !sawSignature:
		return Info{}, fmt.Errorf("%w: %s", ErrIncomplete, memberSignature)
	}
	return info, nil
}

// InspectBytes is [Inspect] over an in-memory archive.
func InspectBytes(data []byte) (Info, error) { return Inspect(bytes.NewReader(data)) }

// normaliseMember strips the leading "./" tar writers sometimes emit so a
// member matches regardless of how the archive was produced.
func normaliseMember(name string) string {
	return strings.TrimPrefix(strings.TrimSpace(name), "./")
}

// parseFirmwareVersion pulls VERSION= and PRODUCT= out of the CCU's
// /boot/VERSION copy. Unparseable content yields empty strings rather than
// an error: the version is reported to the operator, never enforced.
func parseFirmwareVersion(r io.Reader) (version, product string) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return "", ""
	}
	for line := range strings.SplitSeq(string(raw), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch key {
		case "VERSION":
			version = value
		case "PRODUCT":
			product = value
		}
	}
	return version, product
}
