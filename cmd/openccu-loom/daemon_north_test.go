// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/config"
)

func TestFirstRunNeedsSetup(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		localUserCount int
		mutate         func(*config.Config)
		want           bool
	}{
		{
			name:           "local user present",
			localUserCount: 1,
			want:           false,
		},
		{
			name:           "YAML user present",
			localUserCount: 0,
			mutate: func(c *config.Config) {
				c.North.REST.Auth.Users = map[string]string{"admin": "x"}
			},
			want: false,
		},
		{
			name:           "CCU auth explicitly enabled",
			localUserCount: 0,
			mutate: func(c *config.Config) {
				c.North.REST.Auth.CCU.Enabled = ptrBool(true)
			},
			want: false,
		},
		{
			name:           "OIDC enabled",
			localUserCount: 0,
			mutate: func(c *config.Config) {
				c.North.REST.Auth.OIDC.Enabled = true
			},
			want: false,
		},
		{
			name:           "genuine first run: nothing configured",
			localUserCount: 0,
			// CCU.Enabled nil → build.IsAddon() == false in a normal test build.
			want: true,
		},
		{
			name:           "CCU auth explicitly disabled, nothing else",
			localUserCount: 0,
			mutate: func(c *config.Config) {
				c.North.REST.Auth.CCU.Enabled = ptrBool(false)
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := &config.Config{}
			if tt.mutate != nil {
				tt.mutate(cfg)
			}
			got := firstRunNeedsSetup(cfg, tt.localUserCount)
			if got != tt.want {
				t.Errorf("firstRunNeedsSetup(..., %d) = %v, want %v", tt.localUserCount, got, tt.want)
			}
		})
	}
}
