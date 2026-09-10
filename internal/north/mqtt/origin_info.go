// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import hadiscovery "github.com/SukramJ/go-hamqtt/discovery"

// BuildOriginInfo returns the HA Discovery `origin` block that identifies
// the bridge in every Discovery payload (HA 2024+). All call-sites use
// this function so the name/version/support_url triple stays consistent
// across discovery.go, discovery_aggregate.go, discovery_combined.go,
// discovery_schedule.go, discovery_update.go, and discovery_week_profile.go.
//
// The version is read from [originVersionStore] so [SetOriginVersion]
// propagates automatically to every Discovery emit.
func BuildOriginInfo() *hadiscovery.Origin {
	return &hadiscovery.Origin{
		Name: originName,
		SW:   originVersion(),
		URL:  originSupportURL,
	}
}
