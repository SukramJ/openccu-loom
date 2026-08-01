// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package linkprofile_test

import (
	"context"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/store/linkprofile"
)

// TestProfilesComeFromTheSharedModule pins the outcome of removing this
// package's own copy of the archives.
//
// The copy aged independently: it still carried the CCU's HTML references
// and the pre-3.89.5 constraint set long after the shared module had moved
// on, so the same profile read through two code paths gave two answers.
// These assertions fail if a local copy is reintroduced and shadows the
// module.
func TestProfilesComeFromTheSharedModule(t *testing.T) {
	t.Parallel()
	s := linkprofile.New()

	t.Run("display strings are plain text", func(t *testing.T) {
		t.Parallel()
		profiles, err := s.GetLinkProfiles(context.Background(), "ACOUSTIC_SIGNAL_VIRTUAL_RECEIVER", "ACCELERATION_TRANSCEIVER", "de")
		if err != nil {
			t.Fatalf("GetLinkProfiles: %v", err)
		}
		if len(profiles) == 0 {
			t.Fatal("no profiles returned")
		}
		for _, p := range profiles {
			for locale, name := range p.Name {
				if strings.Contains(name, "&") && strings.Contains(name, ";") {
					t.Errorf("profile %d name[%s] carries an HTML reference: %q", p.ID, locale, name)
				}
			}
		}
	})

	t.Run("constraints match OCCU 3.89.5", func(t *testing.T) {
		t.Parallel()
		// eQ-3 narrowed LONG_PROFILE_ACTION_TYPE from the list {1 5} to the
		// scalar 1 on this profile. Reading "list" here means the stale copy
		// is being served.
		p, ok := s.GetProfileByID("BLIND_VIRTUAL_RECEIVER", "KEY_TRANSCEIVER", 3)
		if !ok {
			t.Fatal("BLIND_VIRTUAL_RECEIVER/KEY_TRANSCEIVER profile 3 not found")
		}
		c, ok := p.Params["LONG_PROFILE_ACTION_TYPE"]
		if !ok {
			t.Fatal("profile 3 has no LONG_PROFILE_ACTION_TYPE constraint")
		}
		if c.ConstraintType != "fixed" {
			t.Errorf("constraint_type = %q, want %q — the archive predates OCCU 3.89.5", c.ConstraintType, "fixed")
		}
	})
}
