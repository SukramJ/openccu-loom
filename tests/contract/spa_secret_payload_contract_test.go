// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSPASecretPlaceholderMatchesHandler pins the two halves of the
// masked-secret round-trip against each other.
//
// The SPA never receives a secret's cleartext — the section GET masks it — so
// on save it has to tell the daemon "I did not change this". Which form it
// picks and which forms the handler accepts are two independent edits in two
// languages, and when they drift the failure is silent and destructive: the
// section persists with an empty credential, the daemon connects without it,
// and the remote end answers with an authentication error that points at the
// operator's typing rather than at the save path. That is exactly how a
// working MQTT broker link disappeared on a save that only touched
// topic_base.
//
// The contract:
//   - The editor DROPS an untouched secret from the payload
//     (SectionEditor.svelte's save()) — an absent key means "keep the stored
//     value".
//   - restoreMaskedSecrets additionally accepts the two legacy forms an older
//     SPA build or a hand-written PUT can still send: JSON null and the "***"
//     sentinel.
//   - An empty string on a string secret is NOT a placeholder — it is how a
//     credential gets cleared.
//
// This test asserts the source of both sides still encodes that agreement.
// It is deliberately textual: importing the SPA is not possible from Go, and
// the failure mode it guards is precisely one side being edited alone.
func TestSPASecretPlaceholderMatchesHandler(t *testing.T) {
	editor := readRepoFile(t, filepath.Join("assets", "ui", "src", "lib", "components", "settings", "SectionEditor.svelte"))
	handler := readRepoFile(t, filepath.Join("internal", "north", "rest", "handlers", "admin_config.go"))

	// SPA side: an untouched secret is removed from the payload, not sent as a
	// placeholder value. The old behaviour (rewriting it to null) is what the
	// handler could not distinguish from an operator edit.
	if !strings.Contains(editor, "deleteDeep(payload, rel)") {
		t.Error("SectionEditor.save() must drop an untouched secret from the payload " +
			"(deleteDeep(payload, rel)); sending a placeholder value instead is what wiped credentials")
	}
	if strings.Contains(editor, `setDeep(payload, rel, null)`) {
		t.Error("SectionEditor.save() still rewrites an untouched secret to null — " +
			"the handler cannot tell that apart from a deliberate clear")
	}

	// Handler side: absent keys and both legacy placeholder forms are
	// reconciled back to the stored value.
	for _, want := range []struct {
		snippet string
		why     string
	}{
		{"func secretPayloadIsPlaceholder", "the placeholder contract must live in one named place"},
		{"if !present || v == nil", "an absent key and an explicit null must both mean \"unchanged\""},
		{"s == maskSentinel", "the masked sentinel must still be accepted from older SPA builds"},
		{`s == "" && !isStringSecret(goType)`, "an empty string may only be a placeholder for a complex secret; on a string secret it clears the credential"},
	} {
		if !strings.Contains(handler, want.snippet) {
			t.Errorf("restoreMaskedSecrets no longer encodes %q: %s", want.snippet, want.why)
		}
	}

	// Masking side: an unset secret must not be reported as "***", or a
	// credential that was silently dropped still reads as configured in the UI.
	if !strings.Contains(handler, "if secretIsSet(val)") {
		t.Error("maskPath must only mask secrets that are actually set — masking an empty " +
			"one hides a dropped credential behind a \"configured\" placeholder")
	}
}

// readRepoFile reads a repo-relative path, failing the test when it is missing
// (a moved file must update this contract, not silently skip it).
func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(raw)
}
