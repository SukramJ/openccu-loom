// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ccudata

import "testing"

// TestEasymodeEmbeddedHasVisibilityRules verifies that the embedded
// easymode archive actually carries ConditionalVisibility entries
// for the channel/sender combinations we rely on. If the extractor
// regresses to emitting empty visibility lists, the form-schema
// would surface every parameter unconditionally — which is exactly
// the UX regression P2-4 protects against.
func TestEasymodeEmbeddedHasVisibilityRules(t *testing.T) {
	em, err := LoadEasymodeEmbedded()
	if err != nil {
		t.Fatalf("LoadEasymodeEmbedded: %v", err)
	}

	// Count (channel, sender) combinations that carry at least one
	// conditional rule. ConditionalVisibility lives on
	// SenderTypeMetadata, not ChannelMetadata.
	withRules := 0
	totalSenders := 0
	for _, ch := range em.ChannelMetadata {
		for _, st := range ch.SenderTypes {
			totalSenders++
			if len(st.ConditionalVisibility) > 0 {
				withRules++
			}
		}
	}
	if withRules == 0 {
		t.Fatal("no sender combination exposes ConditionalVisibility — extractor regressed")
	}
	t.Logf("%d/%d sender combinations expose visibility rules", withRules, totalSenders)
}

// TestEasymodeEmbeddedHasOptionPresets sanity-checks that the
// option-preset registry is non-empty.
func TestEasymodeEmbeddedHasOptionPresets(t *testing.T) {
	em, err := LoadEasymodeEmbedded()
	if err != nil {
		t.Fatalf("LoadEasymodeEmbedded: %v", err)
	}
	if len(em.OptionPresets) == 0 {
		t.Fatal("OptionPresets empty — extractor regressed")
	}
}

// TestEasymodeEmbeddedHasCrossValidations asserts that
// cross-validation rules — parameter A requires/forbids parameter B
// — survive the extraction.
func TestEasymodeEmbeddedHasCrossValidations(t *testing.T) {
	em, err := LoadEasymodeEmbedded()
	if err != nil {
		t.Fatalf("LoadEasymodeEmbedded: %v", err)
	}
	if len(em.CrossValidations.Rules) == 0 {
		t.Fatal("CrossValidations.Rules empty — extractor regressed")
	}
}

// TestEasymodeEmbeddedMasterProfileSurface verifies that the
// MasterProfile decoder doesn't blow up on the embedded archive,
// regardless of whether any channel currently ships one. The
// embedded extract may legitimately omit profiles when no source
// channel defines them; the assertion is therefore a smoke test
// rather than a count.
func TestEasymodeEmbeddedMasterProfileSurface(t *testing.T) {
	em, err := LoadEasymodeEmbedded()
	if err != nil {
		t.Fatalf("LoadEasymodeEmbedded: %v", err)
	}
	withProfile := 0
	for _, ch := range em.ChannelMetadata {
		if ch.MasterProfile != nil {
			withProfile++
		}
	}
	t.Logf("%d channels ship a MasterProfile", withProfile)
}
