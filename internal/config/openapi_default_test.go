// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package config

import "testing"

// TestOpenAPIValidateDefaultIsOn pins the production-default
// behaviour: a YAML config without `openapi_validate:` enables the
// validator. The previous behaviour was the opposite (off-by-default
// while the spec was being backfilled). Flipping the default is part
// of the operational-maturity sweep — this test stops accidental
// reverts.
func TestOpenAPIValidateDefaultIsOn(t *testing.T) {
	var n NorthREST
	if !n.OpenAPIValidateEnabled() {
		t.Fatal("OpenAPIValidateEnabled with nil pointer must return true (production default)")
	}
}

// TestOpenAPIValidateExplicitFalse verifies the opt-out path: an
// operator setting `openapi_validate: false` in YAML disables the
// validator. Used by forks that carry endpoints not yet backfilled
// into the spec.
func TestOpenAPIValidateExplicitFalse(t *testing.T) {
	off := false
	n := NorthREST{OpenAPIValidate: &off}
	if n.OpenAPIValidateEnabled() {
		t.Fatal("explicit false must disable validation")
	}
}

// TestOpenAPIValidateExplicitTrue is symmetric — explicit true keeps
// validation on (same as nil but documented intent).
func TestOpenAPIValidateExplicitTrue(t *testing.T) {
	on := true
	n := NorthREST{OpenAPIValidate: &on}
	if !n.OpenAPIValidateEnabled() {
		t.Fatal("explicit true must enable validation")
	}
}

// TestOpenAPIValidateYAMLRoundTrip is the end-to-end check: loading a
// YAML document without the key returns enabled; with `false` returns
// disabled. Mirrors what production deployments will see.
func TestOpenAPIValidateYAMLRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want bool
	}{
		{"unset", "north:\n  rest:\n    listen: ':8080'\n", true},
		{"explicit-true", "north:\n  rest:\n    openapi_validate: true\n    listen: ':8080'\n", true},
		{"explicit-false", "north:\n  rest:\n    openapi_validate: false\n    listen: ':8080'\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Parse([]byte(tc.yaml))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if got := cfg.North.REST.OpenAPIValidateEnabled(); got != tc.want {
				t.Errorf("OpenAPIValidateEnabled=%v want %v", got, tc.want)
			}
		})
	}
}
