// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import "fmt"

// LegacyAliasConfig opt-in legacy-topic mirroring for consumers built
// against the older flat device-state topology
// (`{base}/device/status/{address}/{address}_{channel}_{parameter}`).
// When enabled every per-DP state and availability publish is
// mirrored under that older shape so existing subscribers keep
// working during migration.
//
// Inbound writes (the `/set` tree) and HA Discovery are NOT mirrored.
type LegacyAliasConfig struct {
	// Enabled toggles the device-state alias. When false the alias is
	// a no-op even if Base is set.
	Enabled bool

	// Base is the prefix the legacy bridge published under (default
	// is set in code). Operators that ran a custom base mirror it here.
	Base string
}

// LegacyTopicBuilder produces the device-state mirror topology that
// existed before openccu-loom's bucket-aware tree.
//
//	{base}/device/status/{address}/{address}_{channel}_{parameter}
//	{base}/device/availability/{address}
type LegacyTopicBuilder struct {
	Base string
}

// NewLegacyTopicBuilder returns a builder; an empty base falls back
// to the historical default prefix used by the older bridge.
func NewLegacyTopicBuilder(base string) *LegacyTopicBuilder {
	if base == "" {
		base = "aiohomematic2mqtt"
	}
	return &LegacyTopicBuilder{Base: base}
}

// DataPointState renders the legacy state topic for (address,
// channel, parameter).
func (b *LegacyTopicBuilder) DataPointState(address string, channel int, parameter string) string {
	return fmt.Sprintf("%s/device/status/%s/%s_%d_%s",
		b.Base,
		safe(address),
		safe(address),
		channel,
		safe(parameter))
}

// DeviceAvailability renders the legacy availability topic.
func (b *LegacyTopicBuilder) DeviceAvailability(address string) string {
	return fmt.Sprintf("%s/device/availability/%s", b.Base, safe(address))
}
