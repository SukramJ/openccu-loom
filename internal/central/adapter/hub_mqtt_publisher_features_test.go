// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client/transport/occulited"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
)

// configState reports whether the discovery config of item was declared
// (last publish carried a payload) and whether it was ever published.
func configState(pub *mqtt.NoopClient, item mqtt.DiscoveryItem) (declared, seen bool) {
	for _, p := range pub.Published() {
		if !strings.HasSuffix(p.Topic, "/"+item.ObjectID+"/config") {
			continue
		}
		seen = true
		declared = len(p.Payload) > 0
	}
	return declared, seen
}

// TestHubPlaneTopicsRoundTripLite pins the feature gate of the hub plane
// on an openccu-lite central: what the system does not have (alarm
// messages, the inbox) is never declared, what the token's scopes grant
// (service messages, the system update) is; and when the scopes shrink,
// the entities that lost their feature are retracted.
func TestHubPlaneTopicsRoundTripLite(t *testing.T) {
	t.Parallel()
	c, pub, publisher := hubDiscoveryFixture(t)
	c.SetSystemInformation(central.SystemInfo{Model: "openccu-lite", Serial: "3014F711A0001F0123456789"})
	c.SetFeatures(liteFeatures(occulited.ExpandScopes([]string{"rpc:read", "meta:read", "system:read"})))

	publisher.Start(context.Background())
	defer publisher.Stop()
	publisher.Flush()

	disco := publisher.wiring.Bridge().DefaultBuilder()
	name := c.Name()
	for _, tc := range []struct {
		what string
		item mqtt.DiscoveryItem
		want bool
	}{
		{"alarm messages", disco.BuildAlarmMessagesDiscovery(name), false},
		{"inbox", disco.BuildInboxDiscovery(name), false},
		{"service messages", disco.BuildServiceMessagesDiscovery(name), true},
		{"system update", disco.BuildHubUpdateDiscovery(name), true},
	} {
		if !tc.item.OK {
			t.Fatalf("%s: the builder produced no item; the fixture lacks the serial", tc.what)
		}
		if declared, _ := configState(pub, tc.item); declared != tc.want {
			t.Errorf("%s declared = %v, want %v", tc.what, declared, tc.want)
		}
	}

	// The token loses system:read: service messages and the system update
	// go away and must leave the discovery plane.
	c.SetFeatures(liteFeatures(occulited.ExpandScopes([]string{"rpc:read", "meta:read"})))
	publisher.Flush()
	for _, item := range []mqtt.DiscoveryItem{disco.BuildServiceMessagesDiscovery(name), disco.BuildHubUpdateDiscovery(name)} {
		if declared, seen := configState(pub, item); declared || !seen {
			t.Errorf("%s: declared=%v seen=%v after the scope shrank, want retracted", item.ObjectID, declared, seen)
		}
	}
}
