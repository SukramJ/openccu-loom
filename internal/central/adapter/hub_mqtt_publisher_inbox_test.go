// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/hub"
)

// TestInboxUpdateReachesMQTT verifies that an Inbox.Replace call causes
// the publisher to push the inbox state to the MQTT broker.
func TestInboxUpdateReachesMQTT(t *testing.T) {
	t.Parallel()
	_, c, pub, publisher := hubMQTTFixture(t)

	publisher.Start(context.Background())
	defer publisher.Stop()
	publisher.Flush()

	prev := len(pub.Published())

	// Populate inbox and trigger an update.
	c.HubModel.Inbox.Replace([]hub.InboxDevice{
		{Address: "AABBCC001122", Model: "HmIP-PS", Serial: "123456"},
	})

	publisher.Flush()
	after := pub.Published()
	if len(after) <= prev {
		t.Fatalf("no publish after Inbox.Replace; before=%d after=%d topics=%v",
			prev, len(after), publishedTopics(pub))
	}

	var found bool
	for _, p := range after[prev:] {
		if strings.Contains(p.Topic, "inbox") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("inbox topic missing; new topics=%v", func() []string {
			ts := publishedTopics(pub)
			if len(ts) > prev {
				return ts[prev:]
			}
			return nil
		}())
	}
}

// TestInboxInitialStatePublishedAtStart verifies that a pre-populated
// inbox triggers an initial-state publish at Start time.
func TestInboxInitialStatePublishedAtStart(t *testing.T) {
	t.Parallel()
	_, c, pub, publisher := hubMQTTFixture(t)

	// Seed inbox before Start.
	c.HubModel.Inbox.Replace([]hub.InboxDevice{
		{Address: "DDEEFF112233", Model: "HmIP-SWDO"},
	})

	publisher.Start(context.Background())
	defer publisher.Stop()
	publisher.Flush()

	var found bool
	for _, p := range pub.Published() {
		if strings.Contains(p.Topic, "inbox") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("inbox initial-state publish missing; topics=%v", publishedTopics(pub))
	}
}
