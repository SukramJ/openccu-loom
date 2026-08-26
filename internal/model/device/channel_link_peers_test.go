// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package device

import (
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestChannelLinkPeersRoundtrip verifies the basic get/set contract.
func TestChannelLinkPeersRoundtrip(t *testing.T) {
	t.Parallel()

	d := New(Config{Address: "DEV001", Model: "HmIP-eTRV"})
	ch := d.AddChannel("DEV001:1", 1, "CLIMATE", hmenum.ParamsetKeyValues)

	// Initially nil.
	if got := ch.LinkPeers(); got != nil {
		t.Fatalf("LinkPeers() before SetLinkPeers = %v, want nil", got)
	}

	peers := []string{"VALVE001:1", "VALVE002:1"}
	ch.SetLinkPeers(peers)

	got := ch.LinkPeers()
	if len(got) != len(peers) {
		t.Fatalf("LinkPeers() len = %d, want %d", len(got), len(peers))
	}
	for i, p := range peers {
		if got[i] != p {
			t.Errorf("LinkPeers()[%d] = %q, want %q", i, got[i], p)
		}
	}
}

// TestChannelLinkPeersReturnsCopy ensures the returned slice is a copy —
// mutations by the caller do not affect the stored cache.
func TestChannelLinkPeersReturnsCopy(t *testing.T) {
	t.Parallel()

	d := New(Config{Address: "DEV002", Model: "HmIP-eTRV"})
	ch := d.AddChannel("DEV002:1", 1, "CLIMATE", hmenum.ParamsetKeyValues)

	ch.SetLinkPeers([]string{"VALVE001:1"})

	got := ch.LinkPeers()
	got[0] = "MUTATED"

	got2 := ch.LinkPeers()
	if got2[0] != "VALVE001:1" {
		t.Errorf("LinkPeers() returned same slice (mutation leaked): %q", got2[0])
	}
}

// TestChannelSetLinkPeersClear verifies that passing an empty slice clears
// the cache (LinkPeers returns nil afterwards).
func TestChannelSetLinkPeersClear(t *testing.T) {
	t.Parallel()

	d := New(Config{Address: "DEV003", Model: "HmIP-eTRV"})
	ch := d.AddChannel("DEV003:1", 1, "CLIMATE", hmenum.ParamsetKeyValues)

	ch.SetLinkPeers([]string{"VALVE001:1"})
	ch.SetLinkPeers([]string{}) // clear

	if got := ch.LinkPeers(); got != nil {
		t.Fatalf("LinkPeers() after clearing with empty slice = %v, want nil", got)
	}
}

// TestChannelSetLinkPeersNilClear verifies that passing a nil slice also
// clears the cache.
func TestChannelSetLinkPeersNilClear(t *testing.T) {
	t.Parallel()

	d := New(Config{Address: "DEV004", Model: "HmIP-eTRV"})
	ch := d.AddChannel("DEV004:1", 1, "CLIMATE", hmenum.ParamsetKeyValues)

	ch.SetLinkPeers([]string{"VALVE001:1"})
	ch.SetLinkPeers(nil) // clear via nil

	if got := ch.LinkPeers(); got != nil {
		t.Fatalf("LinkPeers() after SetLinkPeers(nil) = %v, want nil", got)
	}
}

// TestChannelLinkPeersNilReceiver verifies that calling methods on a nil
// *Channel is safe.
func TestChannelLinkPeersNilReceiver(t *testing.T) {
	t.Parallel()

	var ch *Channel
	if got := ch.LinkPeers(); got != nil {
		t.Fatalf("nil.LinkPeers() = %v, want nil", got)
	}
	ch.SetLinkPeers([]string{"X:1"}) // must not panic
}

// TestChannelLinkPeersConcurrentSafe exercises the RWMutex under concurrent
// readers and writers to verify there are no data races.
func TestChannelLinkPeersConcurrentSafe(t *testing.T) {
	t.Parallel()

	d := New(Config{Address: "DEV005", Model: "HmIP-eTRV"})
	ch := d.AddChannel("DEV005:1", 1, "CLIMATE", hmenum.ParamsetKeyValues)

	var wg sync.WaitGroup
	const goroutines = 20

	for i := range goroutines {
		wg.Add(2)
		go func(n int) {
			defer wg.Done()
			ch.SetLinkPeers([]string{"VALVE:1", "VALVE:2"})
			_ = n
		}(i)
		go func(n int) {
			defer wg.Done()
			_ = ch.LinkPeers()
			_ = n
		}(i)
	}
	wg.Wait()
}
