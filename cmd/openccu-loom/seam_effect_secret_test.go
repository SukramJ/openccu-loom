// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/configstore"
	"github.com/SukramJ/openccu-loom/internal/wiring"
)

// TestSeamEffect_ConfigStoreCrypto_SealsASectionSecret asserts what the
// secret.config_store_crypto seam's Why claims: that an SPA-saved config
// section does not reach the database in cleartext.
//
// It reads the stored row back, not the accessor. Get runs the same
// transform in reverse, so a round-trip through the store returns the
// plaintext whether or not anything was ever sealed — the only place the
// question can be answered is the column itself.
func TestSeamEffect_ConfigStoreCrypto_SealsASectionSecret(t *testing.T) {
	const secretValue = "seam-effect-broker-passphrase"

	ov, teardown := seamEffectOverlay(t)
	t.Cleanup(teardown)

	payload := []byte(`{"password":"` + secretValue + `"}`)
	if _, err := ov.sqSections.Put(context.Background(),
		string(configstore.SectionMQTT), payload, "seam-effect"); err != nil {
		t.Fatalf("put section: %v", err)
	}

	stored := rawSectionValue(t, ov, string(configstore.SectionMQTT))
	if strings.Contains(stored, secretValue) {
		t.Error("the broker passphrase is in the database in the clear: no cipher reached " +
			"the config-section store, and every surface still reports secrets as encrypted")
	}

	// The seal has to be reversible, or the seam trades a disclosure for a
	// daemon that cannot read its own configuration back.
	row, err := ov.sqSections.Get(context.Background(), string(configstore.SectionMQTT))
	if err != nil {
		t.Fatalf("get section: %v", err)
	}
	if !strings.Contains(string(row.ValueJSON), secretValue) {
		t.Errorf("the sealed section did not open again: stored value %q does not carry the "+
			"passphrase, so the daemon cannot read back what the operator saved",
			string(row.ValueJSON))
	}
}

// TestSeamEffect_ConfigStoreCrypto_IsAttributableToTheSeam is the negative
// control: with no transform installed, the same write must land in the
// clear. It is what makes the assertion above a statement about the seam
// rather than about a store that happens to encode its payload.
func TestSeamEffect_ConfigStoreCrypto_IsAttributableToTheSeam(t *testing.T) {
	const secretValue = "seam-effect-control-passphrase"

	ov, teardown := seamEffectOverlay(t)
	t.Cleanup(teardown)

	// Undo exactly what the seam installed, leaving everything else as the
	// composition root built it.
	ov.sqSections.SetSecretTransform(nil)

	payload := []byte(`{"password":"` + secretValue + `"}`)
	if _, err := ov.sqSections.Put(context.Background(),
		string(configstore.SectionMQTT), payload, "seam-effect"); err != nil {
		t.Fatalf("put section: %v", err)
	}

	if stored := rawSectionValue(t, ov, string(configstore.SectionMQTT)); !strings.Contains(stored, secretValue) {
		t.Error("the passphrase was sealed with no transform installed — something other " +
			"than this seam encrypts section secrets, so the test above proves nothing " +
			"about it")
	}
}

// seamEffectOverlay builds the audit overlay the way boot does, which is
// what installs the cipher on the section store.
func seamEffectOverlay(t *testing.T) (overlay *auditOverlay, teardown func()) {
	t.Helper()

	cfg := config.Default()
	cfg.DataDir = t.TempDir()

	ov, done := wireAuditOverlay(context.Background(), wiring.NewManifest(), cfg,
		slog.New(slog.DiscardHandler))
	if ov.db == nil {
		t.Fatal("the audit overlay opened no database — the seam would have no store to " +
			"attach a cipher to, and this test would measure the fixture")
	}
	if ov.sqSections == nil {
		t.Fatal("the audit overlay built no config-section store")
	}
	return ov, done
}

// rawSectionValue reads the stored column straight out of SQLite, past
// every accessor that would undo the seal on the way.
func rawSectionValue(t *testing.T, ov *auditOverlay, section string) string {
	t.Helper()

	var raw []byte
	err := ov.db.QueryRow(`SELECT value_json FROM config_sections WHERE section = ?`, section).Scan(&raw)
	if err != nil {
		t.Fatalf("read stored section: %v", err)
	}
	return string(raw)
}
