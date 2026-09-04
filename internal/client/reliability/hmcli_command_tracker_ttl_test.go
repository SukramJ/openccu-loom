// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package reliability

import (
	"testing"
	"time"
)

// hmCliHmIPLegacyResponseBudget is the CCU's own ceiling on a synchronous
// legacy call: Legacy.ResponseTimeout, default 25 s
// (HMIPServer de.eq3.cbcs.legacy.bidcos.rpc.LegacyServiceHandler). It is the
// largest firmware-stated synchronous budget the optimistic tracker has to
// outlive; the BidCos per-frame waits are smaller (1500 / 4500 / 12500 ms,
// OpenCCU-Base src/rfd/BidcosFrameWaitTime.h).
const hmCliHmIPLegacyResponseBudget = 25 * time.Second

// TestHmCliCommandTrackerTTLCoversSynchronousBudgets pins the one half of the
// TTL that a firmware value can justify.
//
// The optimistic entry has to survive at least as long as the CCU may take to
// answer a synchronous write, or an ordinary confirmation is dropped as stale.
// It cannot be made to cover a wake-up-only device at any value: rfd queues
// that frame with no expiry until the device next transmits, so the TTL is a
// bounded-memory policy there rather than a latency model. This guard measures
// the part that is measurable and fails if the default drops under the CCU's
// own synchronous ceiling.
func TestHmCliCommandTrackerTTLCoversSynchronousBudgets(t *testing.T) {
	t.Parallel()

	var cfg CommandTrackerConfig
	cfg.applyDefaults()

	if cfg.TTL <= hmCliHmIPLegacyResponseBudget {
		t.Errorf("default CommandTracker TTL = %s, want more than the CCU's synchronous legacy-call budget of %s — a shorter TTL expires entries while the CCU is still allowed to be answering",
			cfg.TTL, hmCliHmIPLegacyResponseBudget)
	}
}

// TestHmCliCommandTrackerEntryExpiresAtTTL measures the expiry itself, so the
// documented "the value reverts once the TTL passes" behaviour on a wake-up
// device is asserted rather than asserted-in-prose.
func TestHmCliCommandTrackerEntryExpiresAtTTL(t *testing.T) {
	t.Parallel()

	tracker := NewCommandTracker("central-iface", CommandTrackerConfig{TTL: 20 * time.Millisecond})
	dpk, ok := tracker.AddSetValue("VCU0000123:1", "LEVEL", "VALUES", 0.5)
	if !ok {
		t.Fatal("AddSetValue reported no tracked key")
	}
	if _, live := tracker.GetLastSentValue(dpk); !live {
		t.Fatal("GetLastSentValue right after the send = (nil, false), want the sent value")
	}
	time.Sleep(40 * time.Millisecond)
	if v, live := tracker.GetLastSentValue(dpk); live {
		t.Errorf("GetLastSentValue past the TTL = (%v, true), want (nil, false) — a late callback must not be filed as the confirmation of this send", v)
	}
}
