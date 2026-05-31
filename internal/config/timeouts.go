// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package config

import "time"

// TimeoutConfig groups all configurable timeout durations in one
// typed struct so that callsites get named, self-documenting values
// instead of scattered magic numbers.
//
// All fields are [time.Duration]. Zero means "use the default"
// callers should call [TimeoutConfig.WithDefaults] before reading
// values so that unset fields resolve to the documented baseline.
type TimeoutConfig struct {
	// Connect is the maximum time allowed to establish the TCP
	// connection to a CCU endpoint (XML-RPC, BIN-RPC). Default: 10 s.
	Connect time.Duration `yaml:"connect"`

	// Request is the maximum time allowed for a single RPC round-trip
	// (XML-RPC call, BIN-RPC call, JSON-RPC call). Default: 60 s.
	Request time.Duration `yaml:"request"`

	// Init is the maximum time allowed for the init() / de-init()
	// handshake that registers the callback URL with the CCU.
	// Default: 30 s.
	Init time.Duration `yaml:"init"`

	// Ping is the maximum time the ping-pong keepalive waits for a
	// pong reply before declaring the interface unhealthy. Default: 30 s.
	Ping time.Duration `yaml:"ping"`

	// WaitForCallback is the maximum time [client.WaitForStateChangeOrTimeout]
	// will block after a SetValue / PutParamset call waiting for the
	// CCU event-push confirmation. Default: 60 s.
	WaitForCallback time.Duration `yaml:"wait_for_callback"`

	// ScheduleRefresh is the maximum time the periodic-refresh
	// coordinator waits for a full param-read round-trip when
	// RequiresPeriodicRefresh is true. Default: 30 s.
	ScheduleRefresh time.Duration `yaml:"schedule_refresh"`
}

// DefaultTimeoutConfig returns a [TimeoutConfig] populated with the
// recommended baseline values.
func DefaultTimeoutConfig() TimeoutConfig {
	return TimeoutConfig{
		Connect:         10 * time.Second,
		Request:         60 * time.Second,
		Init:            30 * time.Second,
		Ping:            30 * time.Second,
		WaitForCallback: 60 * time.Second,
		ScheduleRefresh: 30 * time.Second,
	}
}

// WithDefaults returns a copy of c with every zero-valued field
// replaced by the corresponding [DefaultTimeoutConfig] value.
func (c TimeoutConfig) WithDefaults() TimeoutConfig {
	d := DefaultTimeoutConfig()
	if c.Connect == 0 {
		c.Connect = d.Connect
	}
	if c.Request == 0 {
		c.Request = d.Request
	}
	if c.Init == 0 {
		c.Init = d.Init
	}
	if c.Ping == 0 {
		c.Ping = d.Ping
	}
	if c.WaitForCallback == 0 {
		c.WaitForCallback = d.WaitForCallback
	}
	if c.ScheduleRefresh == 0 {
		c.ScheduleRefresh = d.ScheduleRefresh
	}
	return c
}
