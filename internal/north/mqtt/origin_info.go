// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

// BuildOriginInfo returns the HA Discovery `origin` block that identifies
// the bridge in every Discovery payload (HA 2024+). All call-sites use
// this function so the name/version/support_url triple stays consistent
// across discovery.go, discovery_aggregate.go, discovery_combined.go,
// discovery_schedule.go, discovery_update.go, and discovery_week_profile.go.
//
// The version is read from [originVersionStore] so [SetOriginVersion]
// propagates automatically to every Discovery emit.
func BuildOriginInfo() map[string]any {
	return map[string]any{
		"name":        originName,
		"sw_version":  originVersion(),
		"support_url": originSupportURL,
	}
}
