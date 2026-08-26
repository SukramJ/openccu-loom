// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package config

import "time"

// ScheduleTimerConfig groups the intervals that drive the periodic
// background jobs inside the scheduler. Naming each field explicitly
// avoids the "which position is which?" confusion of bare
// []time.Duration slices and maps directly to the job names in
// [scheduler.Job].
//
// All fields are [time.Duration]. Zero means "use the default"
// callers should call [ScheduleTimerConfig.WithDefaults] before reading
// values.
type ScheduleTimerConfig struct {
	// PeriodicRefresh is the interval between full param-value refresh
	// cycles for backends with RequiresPeriodicRefresh = true
	// (currently unused in 0.1.0 where all backends push). Default: 60 s.
	PeriodicRefresh time.Duration `yaml:"periodic_refresh"`

	// PingPong is the interval between keepalive ping calls to each
	// active backend. Default: 30 s.
	PingPong time.Duration `yaml:"ping_pong"`

	// Reconnect is the minimum wait between reconnect attempts when a
	// backend has entered the FAILED state. Default: 30 s.
	Reconnect time.Duration `yaml:"reconnect"`

	// SystemInfo is the interval at which the JSON-RPC coordinator
	// re-fetches CCU system information (firmware version, serial,
	// software update state). Default: 300 s.
	SystemInfo time.Duration `yaml:"system_info"`

	// HubRefresh is the interval between hub (sysvar / program) full
	// refresh cycles. Default: 60 s.
	HubRefresh time.Duration `yaml:"hub_refresh"`

	// HealthCheck is the interval at which the health tracker reconciles
	// interface-level health signals. Default: 15 s.
	HealthCheck time.Duration `yaml:"health_check"`
}

// DefaultScheduleTimerConfig returns a [ScheduleTimerConfig] with
// recommended baseline values.
func DefaultScheduleTimerConfig() ScheduleTimerConfig {
	return ScheduleTimerConfig{
		PeriodicRefresh: 60 * time.Second,
		PingPong:        30 * time.Second,
		Reconnect:       30 * time.Second,
		SystemInfo:      300 * time.Second,
		HubRefresh:      60 * time.Second,
		HealthCheck:     15 * time.Second,
	}
}

// WithDefaults returns a copy of c with every zero-valued field
// replaced by the corresponding [DefaultScheduleTimerConfig] value.
func (c ScheduleTimerConfig) WithDefaults() ScheduleTimerConfig {
	d := DefaultScheduleTimerConfig()
	if c.PeriodicRefresh == 0 {
		c.PeriodicRefresh = d.PeriodicRefresh
	}
	if c.PingPong == 0 {
		c.PingPong = d.PingPong
	}
	if c.Reconnect == 0 {
		c.Reconnect = d.Reconnect
	}
	if c.SystemInfo == 0 {
		c.SystemInfo = d.SystemInfo
	}
	if c.HubRefresh == 0 {
		c.HubRefresh = d.HubRefresh
	}
	if c.HealthCheck == 0 {
		c.HealthCheck = d.HealthCheck
	}
	return c
}
