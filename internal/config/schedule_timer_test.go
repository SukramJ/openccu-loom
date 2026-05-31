// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package config

import (
	"testing"
	"time"
)

func TestDefaultScheduleTimerConfigNonZero(t *testing.T) {
	d := DefaultScheduleTimerConfig()
	checks := map[string]time.Duration{
		"PeriodicRefresh": d.PeriodicRefresh,
		"PingPong":        d.PingPong,
		"Reconnect":       d.Reconnect,
		"SystemInfo":      d.SystemInfo,
		"HubRefresh":      d.HubRefresh,
		"HealthCheck":     d.HealthCheck,
	}
	for name, v := range checks {
		if v == 0 {
			t.Errorf("DefaultScheduleTimerConfig.%s is zero", name)
		}
	}
}

func TestScheduleTimerWithDefaultsFillsZeroFields(t *testing.T) {
	empty := ScheduleTimerConfig{}
	filled := empty.WithDefaults()
	d := DefaultScheduleTimerConfig()
	if filled.PingPong != d.PingPong {
		t.Errorf("PingPong: got %v, want %v", filled.PingPong, d.PingPong)
	}
	if filled.HubRefresh != d.HubRefresh {
		t.Errorf("HubRefresh: got %v, want %v", filled.HubRefresh, d.HubRefresh)
	}
}

func TestScheduleTimerWithDefaultsPreservesExplicit(t *testing.T) {
	c := ScheduleTimerConfig{PingPong: 5 * time.Second}
	filled := c.WithDefaults()
	if filled.PingPong != 5*time.Second {
		t.Errorf("PingPong: got %v, want 5s", filled.PingPong)
	}
	d := DefaultScheduleTimerConfig()
	if filled.Reconnect != d.Reconnect {
		t.Errorf("Reconnect should have fallen back to default, got %v", filled.Reconnect)
	}
}
