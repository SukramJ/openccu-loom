// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package subscription_test

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/im/subscription"
	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/operational"
)

// TestSendInterval_LongMaxIntervalStillBeatsSessionIdleTimeout pins the
// heartbeat cadence against the operational idle reaper. The bridge
// accepts MaxIntervalCeiling up to an hour, and the publisher heartbeat
// is the only traffic on an otherwise quiet CASE session — so a cadence
// derived from maxInterval alone lets the reaper evict the session (and
// with it the subscription) long before the next report is due. Two
// heartbeats must fit inside the idle TTL so a single lost report does
// not cost the session.
func TestSendInterval_LongMaxIntervalStillBeatsSessionIdleTimeout(t *testing.T) {
	t.Parallel()
	ch := make(chan reporterCall, 4)
	m := newManager(subscription.Config{}, chanReporter(ch))

	args := defaultArgs()
	args.MaxIntervalCeiling = 3600 // the manager's default negotiation ceiling
	if _, err := m.Subscribe(args); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	t0 := time.Now()
	m.Tick(context.Background(), t0.Add(operational.SessionIdleTimeout/2))
	select {
	case <-ch:
	default:
		t.Fatalf("no heartbeat within half the %v session idle timeout — the reaper closes the session first",
			operational.SessionIdleTimeout)
	}
}
