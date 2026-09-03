// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package config

import "testing"

// TestW2CfgValidateRejectsNegativeAlarmSeconds pins that a negative
// value on one of the three acoustic-budget knobs is refused at save
// time.
//
// [Config.applyDefaults] rewrites only the zero, so a negative reached
// the alarm output manager, which silently substituted its own copy of
// the same three numbers (180 s / 900 s / 120 s). The operator got a
// siren length nobody configured, a 200 on the save, and no log line
// naming the field.
func TestW2CfgValidateRejectsNegativeAlarmSeconds(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		field   string
		set     func(*Config, int)
		value   int
		wantErr bool
	}{
		{
			name:  "negative default siren",
			field: "alarm.default_siren_seconds",
			set:   func(c *Config, v int) { c.Alarm.DefaultSirenSeconds = v },
			value: -1, wantErr: true,
		},
		{
			name:  "negative acoustic budget",
			field: "alarm.max_acoustic_per_incident_seconds",
			set:   func(c *Config, v int) { c.Alarm.MaxAcousticPerIncidentSeconds = v },
			value: -1, wantErr: true,
		},
		{
			name:  "negative stop-verify window",
			field: "alarm.stop_verify_seconds",
			set:   func(c *Config, v int) { c.Alarm.StopVerifySeconds = v },
			value: -1, wantErr: true,
		},
		{
			name:  "zero selects the default",
			field: "alarm.default_siren_seconds",
			set:   func(c *Config, v int) { c.Alarm.DefaultSirenSeconds = v },
			value: 0, wantErr: false,
		},
		{
			name:  "a configured duration",
			field: "alarm.stop_verify_seconds",
			set:   func(c *Config, v int) { c.Alarm.StopVerifySeconds = v },
			value: 45, wantErr: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := Default()
			tc.set(cfg, tc.value)
			err := cfg.Validate()
			if tc.wantErr {
				assertRejected(t, err, tc.field)
				return
			}
			assertAccepted(t, err)
		})
	}
}
