// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package config

import (
	"testing"
)

// TestConfigUIURL verifies that ConfigUIURL appends "/app/" to the
// trimmed PublicURL, and returns "" when PublicURL is empty.
func TestConfigUIURL(t *testing.T) {
	t.Parallel()

	cases := []struct {
		publicURL string
		want      string
	}{
		{"", ""},
		{"https://loom.example.de", "https://loom.example.de/app/"},
		{"https://loom.example.de/", "https://loom.example.de/app/"},
		{"https://loom.example.de///", "https://loom.example.de/app/"},
		{"http://ccu.local:8119", "http://ccu.local:8119/app/"},
	}

	for _, tc := range cases {
		t.Run(tc.publicURL, func(t *testing.T) {
			t.Parallel()
			n := NorthREST{PublicURL: tc.publicURL}
			got := n.ConfigUIURL()
			if got != tc.want {
				t.Errorf("ConfigUIURL(%q) = %q, want %q", tc.publicURL, got, tc.want)
			}
		})
	}
}

// TestValidatePublicURL verifies that Config.Validate accepts valid
// public_url values and rejects malformed ones.
func TestValidatePublicURL(t *testing.T) {
	t.Parallel()

	cases := []struct {
		publicURL string
		wantErr   bool
	}{
		{"", false},
		{"https://loom.example.de", false},
		{"http://ccu.local:8119", false},
		{"ftp://loom.example.de", true},
		{"loom.example.de", true},
		{"https://", true},
		{"://broken", true},
	}

	for _, tc := range cases {
		t.Run(tc.publicURL, func(t *testing.T) {
			t.Parallel()
			c := Default()
			c.North.REST.PublicURL = tc.publicURL
			err := c.Validate()
			if tc.wantErr && err == nil {
				t.Errorf("Validate(%q): expected error, got nil", tc.publicURL)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("Validate(%q): unexpected error: %v", tc.publicURL, err)
			}
		})
	}
}
