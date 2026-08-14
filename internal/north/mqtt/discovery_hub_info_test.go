// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"sync"
	"testing"
)

// TestDiscoveryBuilderHubInfoStampIsConcurrencySafe drives the two
// goroutine roles the daemon really runs against one builder: several
// paths stamp per-central CCU metadata (the composition root, the
// central snapshot pass, the hub publisher's worker) while the event
// path builds discovery payloads for incoming CCU values and reads that
// metadata back.
//
// With the per-central map unsynchronised this is not a benign data
// race: the Go runtime aborts the whole daemon with `fatal error:
// concurrent map read and map write` — a crash whose likelihood grows
// with every additional CCU. Run under `-race` it fails on the first
// overlapping pair.
func TestDiscoveryBuilderHubInfoStampIsConcurrencySafe(t *testing.T) {
	t.Parallel()
	const iterations = 2000
	centrals := []string{"ccu-01", "ccu-02"}
	d := NewDefaultDiscoveryBuilder(NewTopicBuilder("openccu-loom"), centrals[0])

	var wg sync.WaitGroup
	for i, name := range centrals {
		wg.Add(1)
		go func(name string, seed int) {
			defer wg.Done()
			for n := range iterations {
				d.SetHubInfoFor(name, HubInfo{
					Name:    name,
					Serial:  "SERIAL" + string(rune('A'+(seed+n)%26)) + "0001",
					Version: "3.79.6",
				})
			}
		}(name, i)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range iterations {
			for _, name := range centrals {
				// The real read path of every hub payload: hubSerial and
				// the device block both resolve through hubFor.
				_ = d.BuildSystemHealthDiscovery(name)
			}
		}
	}()
	wg.Wait()

	// The last stamp must still be readable — a lock that swallowed the
	// write would make the whole plane serial-less and silently skip
	// every hub payload.
	if got := d.hubFor("ccu-02").Name; got != "ccu-02" {
		t.Fatalf("hubFor(ccu-02).Name = %q, want %q", got, "ccu-02")
	}
}
