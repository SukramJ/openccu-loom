// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestSysvarRemovalRetractsDiscovery pins the removal half of the sysvar
// wiring: a variable the operator deleted on the CCU is dropped from the model
// by the next refresh, and its retained discovery config must be cleared with
// an empty payload — otherwise the entity survives in every consumer, frozen
// at its last value, across daemon restarts.
func TestSysvarRemovalRetractsDiscovery(t *testing.T) {
	t.Parallel()
	c, pub, publisher := hubDiscoveryFixture(t)
	c.SetSystemInformation(central.SystemInfo{
		Model:   "HomeMatic Central",
		Version: "3.79.6",
		Serial:  "3014F711A0001F5A4993D962",
	})

	sv := hub.NewSysvar("ccu-01", "Anwesenheit", "", hmenum.HubValueTypeLogic, nil)
	c.HubModel.PutSysvar(sv)

	publisher.Start(context.Background())
	defer publisher.Stop()
	publisher.Flush()

	declared := discoveryConfigTopics(pub, "anwesenheit")
	if len(declared) == 0 {
		t.Fatalf("no discovery config published; topics=%v", publishedTopics(pub))
	}

	c.HubModel.RemoveSysvar("Anwesenheit")
	publisher.Flush()

	for _, topic := range declared {
		if !lastPayloadEmpty(pub, topic) {
			t.Errorf("discovery config %q was not retracted after the sysvar was removed", topic)
		}
	}
}

// discoveryConfigTopics returns every discovery config topic published so far
// whose topic contains needle (lower-cased comparison).
func discoveryConfigTopics(pub *mqtt.NoopClient, needle string) []string {
	var out []string
	seen := map[string]bool{}
	for _, p := range pub.Published() {
		if !strings.HasSuffix(p.Topic, "/config") || !strings.Contains(strings.ToLower(p.Topic), needle) {
			continue
		}
		if !seen[p.Topic] {
			seen[p.Topic] = true
			out = append(out, p.Topic)
		}
	}
	return out
}

// lastPayloadEmpty reports whether the most recent publish to topic carried an
// empty payload, which is how a retained topic is cleared.
func lastPayloadEmpty(pub *mqtt.NoopClient, topic string) bool {
	all := pub.Published()
	for i := len(all) - 1; i >= 0; i-- {
		if all[i].Topic == topic {
			return len(all[i].Payload) == 0
		}
	}
	return false
}
