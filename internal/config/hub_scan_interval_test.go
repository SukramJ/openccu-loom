// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package config

import (
	"strings"
	"testing"
	"time"
)

// TestValidateRejectsTooShortHubScanInterval pins the floor on an
// operator-set scan cadence. Each cycle costs the CCU a ReGa script run on
// a single-threaded interpreter, so a cadence short enough for cycles to
// overlap starves the CCU rather than delivering fresher data.
func TestValidateRejectsTooShortHubScanInterval(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		interval time.Duration
		wantErr  bool
	}{
		{"zero selects the compiled-in default", 0, false},
		{"one second is below the floor", time.Second, true},
		{"just below the floor", MinHubScanInterval - time.Millisecond, true},
		{"exactly the floor", MinHubScanInterval, false},
		{"the compiled-in default", 30 * time.Second, false},
		{"a long cadence", time.Hour, false},
		{"negative", -time.Second, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := Default()
			cfg.Centrals = []CentralConfig{{
				Name:       "ccu",
				Host:       "192.0.2.10",
				Interfaces: []InterfaceSpec{{Name: "HmIP-RF"}},
			}}
			cfg.Centrals[0].Behavior.SysvarScanInterval = tc.interval

			err := cfg.Validate()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Validate() accepted %s, want rejection", tc.interval)
				}
				if !strings.Contains(err.Error(), "sysvar_scan_interval") {
					t.Errorf("error should name the offending field, got: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Validate() rejected %s: %v", tc.interval, err)
			}
		})
	}
}
