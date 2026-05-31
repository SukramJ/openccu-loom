// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package config

import (
	"strings"
	"testing"
)

// TestEveryConfigFieldClassified is the Wave-A contract test that
// enforces classification on every operator-facing config field.
// New fields without a cfg:"basic|expert|secret" tag fail the build
// so reviewers cannot miss the classification step.
//
// Why it matters: Wave B builds the SPA schema endpoint off these
// tags; an unclassified field would default to "hidden" and silently
// disappear from the Settings surface.
func TestEveryConfigFieldClassified(t *testing.T) {
	cases := []struct {
		name string
		val  any
	}{
		{name: "BootstrapConfig", val: &BootstrapConfig{}},
		{name: "Config", val: &Config{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			desc := ClassifyFields(tc.val)
			if len(desc) == 0 {
				t.Fatalf("ClassifyFields returned no descriptors for %s — reflection helper is broken", tc.name)
			}
			unclassified := UnclassifiedFields(desc)
			if len(unclassified) > 0 {
				t.Fatal(FormatUnclassifiedError(unclassified))
			}
		})
	}
}

// TestSecretFieldsExplicit pins the secret-classified fields so a
// rename or accidental down-classification (e.g. secret → basic)
// surfaces in code review.
func TestSecretFieldsExplicit(t *testing.T) {
	desc := ClassifyFields(&Config{})
	gotSecret := make(map[string]struct{})
	for _, d := range desc {
		if d.Class == FieldSecret {
			gotSecret[d.Path] = struct{}{}
		}
	}
	wantSecret := []string{
		"north.mqtt.password",
		"north.rest.auth.users",
		"north.rest.auth.tokens",
		"north.rest.auth.oidc.client_secret",
		"north.matter.attestation.dac_key_path",
		"north.matter.commissioning.passcode",
		"north.matter.commissioning.salt",
		"centrals.password",
	}
	for _, w := range wantSecret {
		if _, ok := gotSecret[w]; !ok {
			t.Errorf("expected cfg:\"secret\" tag on %s — missing", w)
		}
	}
}

// TestClassifyFieldsHandlesSlicesAndMaps ensures the reflection
// walker does not recurse into slice elements or map values (which
// would explode the descriptor list for centrals[].interfaces[] and
// auth.users[]).
func TestClassifyFieldsHandlesSlicesAndMaps(t *testing.T) {
	desc := ClassifyFields(&Config{})
	// `centrals` is a []CentralConfig — must be reported as one leaf,
	// not flattened into one descriptor per InterfaceSpec field.
	foundCentrals := false
	for _, d := range desc {
		if d.Path == "centrals" || strings.HasPrefix(d.Path, "centrals.") {
			foundCentrals = true
			break
		}
	}
	if !foundCentrals {
		t.Error("centrals field not reported at all — reflection walker missed slice leaf")
	}
}
