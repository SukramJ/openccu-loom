// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ccudata

import (
	"testing"
)

// TestProfileLabelResolvesLocalizedName verifies that ResolvedProfile
// returns locale-specific name and description for embedded profiles.
// .
func TestProfileLabelResolvesLocalizedName(t *testing.T) {
	store, err := LoadProfilesEmbedded()
	if err != nil {
		t.Fatalf("LoadProfilesEmbedded: %v", err)
	}
	if len(store.Receivers) == 0 {
		t.Skip("no embedded profiles to test")
	}

	// Pick the first available receiver type.
	var receiverType string
	var rawDoc []byte
	for rt, raw := range store.Receivers {
		receiverType = rt
		rawDoc = raw
		break
	}
	_ = rawDoc

	// Profile 0 should always exist (it's the "expert" / fallback profile).
	rp, ok := store.ResolvedProfile(receiverType, 0, LocaleDE)
	if !ok {
		t.Fatalf("ResolvedProfile(%q, 0, de): not found", receiverType)
	}
	if rp.ID != 0 {
		t.Errorf("ID: got %d, want 0", rp.ID)
	}
	if rp.Name == "" {
		t.Errorf("Name should be non-empty for profile 0 of %q", receiverType)
	}

	// English locale.
	rpEN, ok := store.ResolvedProfile(receiverType, 0, LocaleEN)
	if !ok {
		t.Fatalf("ResolvedProfile(%q, 0, en): not found", receiverType)
	}
	if rpEN.Name == "" {
		t.Errorf("Name (en) should be non-empty for profile 0 of %q", receiverType)
	}
}

// TestProfileLabelUnknownReceiverReturnsFalse verifies that an unknown
// receiver type returns (ResolvedProfile{}, false).
func TestProfileLabelUnknownReceiverReturnsFalse(t *testing.T) {
	store, err := LoadProfilesEmbedded()
	if err != nil {
		t.Fatalf("LoadProfilesEmbedded: %v", err)
	}
	_, ok := store.ResolvedProfile("NO_SUCH_RECEIVER_TYPE_XYZ", 0, LocaleEN)
	if ok {
		t.Error("expected false for unknown receiver type")
	}
}

// TestTranslationsProfileLabel verifies the Translations.ProfileLabel
// convenience wrapper.
func TestTranslationsProfileLabel(t *testing.T) {
	tr := Empty()
	store, err := LoadProfilesEmbedded()
	if err != nil {
		t.Fatalf("LoadProfilesEmbedded: %v", err)
	}
	if len(store.Receivers) == 0 {
		t.Skip("no embedded profiles to test")
	}
	var receiverType string
	for rt := range store.Receivers {
		receiverType = rt
		break
	}

	label := tr.ProfileLabel(store, receiverType, 0, LocaleDE)
	if label == "" {
		t.Errorf("ProfileLabel returned empty string for %q profile 0 (de)", receiverType)
	}

	// Nil store should return empty.
	if got := tr.ProfileLabel(nil, receiverType, 0, LocaleDE); got != "" {
		t.Errorf("ProfileLabel with nil store: want \"\", got %q", got)
	}
}
