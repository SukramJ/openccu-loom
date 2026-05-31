// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package config

import (
	"testing"
	"time"
)

func TestDefaultTimeoutConfigNonZero(t *testing.T) {
	d := DefaultTimeoutConfig()
	checks := map[string]time.Duration{
		"Connect":         d.Connect,
		"Request":         d.Request,
		"Init":            d.Init,
		"Ping":            d.Ping,
		"WaitForCallback": d.WaitForCallback,
		"ScheduleRefresh": d.ScheduleRefresh,
	}
	for name, v := range checks {
		if v == 0 {
			t.Errorf("DefaultTimeoutConfig.%s is zero", name)
		}
	}
}

func TestWithDefaultsFillsZeroFields(t *testing.T) {
	empty := TimeoutConfig{}
	filled := empty.WithDefaults()
	d := DefaultTimeoutConfig()
	if filled.Connect != d.Connect {
		t.Errorf("Connect: got %v, want %v", filled.Connect, d.Connect)
	}
	if filled.Request != d.Request {
		t.Errorf("Request: got %v, want %v", filled.Request, d.Request)
	}
}

func TestWithDefaultsPreservesExplicitValues(t *testing.T) {
	custom := TimeoutConfig{Connect: 5 * time.Second}
	filled := custom.WithDefaults()
	if filled.Connect != 5*time.Second {
		t.Errorf("Connect: got %v, want 5s", filled.Connect)
	}
	d := DefaultTimeoutConfig()
	if filled.Request != d.Request {
		t.Errorf("Request should have fallen back to default, got %v", filled.Request)
	}
}
