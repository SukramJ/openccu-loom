// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package coordinators

import (
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// TestPublishInstallModeRefreshedChangeDetection covers the change detection
// the periodic install-mode refresh depends on: the first run publishes, an
// identical repeat does not, and a genuine transition does.
//
// The bookkeeping lives on the publisher, not on the install-mode model, so
// the last assertion is expressible at all: another plane reading the model's
// state between two runs must not consume this job's change flag. While the
// counter sat on the model, any second reader silently suppressed the next
// publish.
func TestPublishInstallModeRefreshedChangeDetection(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	h := NewHubCoordinator("c", bus)
	m := hub.NewHub("c")
	im := hub.NewInstallMode("ccu-HmIP-RF", nil)
	m.PutInstallMode(im)
	h.SetHubModel(m)

	var published int
	unsub := events.Subscribe(bus, func(hmevent.InstallModeChangedEvent) { published++ })
	defer unsub()

	h.PublishInstallModeRefreshed()
	if published != 1 {
		t.Fatalf("first run: published %d events, want 1", published)
	}

	h.PublishInstallModeRefreshed()
	if published != 1 {
		t.Fatalf("identical repeat published again: %d events, want 1", published)
	}

	im.OnState(true, 60*time.Second)
	h.PublishInstallModeRefreshed()
	if published != 2 {
		t.Fatalf("after enabling install mode: published %d events, want 2", published)
	}

	im.OnState(false, 0)
	// A second reader asks the model for the same state between the two
	// runs. It must not affect what the refresh job publishes.
	_, _, _ = im.InstallState()
	h.PublishInstallModeRefreshed()
	if published != 3 {
		t.Fatalf("after disabling install mode: published %d events, want 3", published)
	}

	_, _, _ = im.InstallState()
	h.PublishInstallModeRefreshed()
	if published != 3 {
		t.Fatalf("repeated steady state published again: %d events, want 3", published)
	}
}
