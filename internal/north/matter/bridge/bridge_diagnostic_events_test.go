// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package bridge

import (
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/diagevent"
)

// TestDiagnosticRingAttachIsSafeWhileTheReceivePathRecords pins that the
// diagnostics ring can be attached, replaced and detached while the bridge is
// serving.
//
// The record calls sit on the per-datagram receive path, so any attach that is
// not published atomically is a data race against every datagram in flight —
// and the operator-facing attach is a REST-triggered call on a live bridge, not
// a boot-time one. Run under -race this fails on an unsynchronised field.
func TestDiagnosticRingAttachIsSafeWhileTheReceivePathRecords(t *testing.T) {
	t.Parallel()

	b := newStartedBridge(t)
	const rounds = 200

	var wg sync.WaitGroup
	wg.Add(3)
	// The receive path: every re-announce records a discovery event.
	go func() {
		defer wg.Done()
		for range rounds {
			b.triggerSessionReannounce()
		}
	}()
	// The operator path: attaching and detaching a ring at runtime.
	go func() {
		defer wg.Done()
		for i := range rounds {
			if i%2 == 0 {
				b.AttachDiagnosticEvents(diagevent.NewRing(16))
				continue
			}
			b.AttachDiagnosticEvents(nil)
		}
	}()
	// The REST reader.
	go func() {
		defer wg.Done()
		for range rounds {
			_ = b.DiagnosticEvents()
		}
	}()
	wg.Wait()

	// A ring attached after the storm still receives what happens next —
	// a detach must not leave the record sites pointing at a dead ring.
	ring := diagevent.NewRing(16)
	b.AttachDiagnosticEvents(ring)
	b.triggerSessionReannounce()
	if got := b.DiagnosticEvents(); len(got) == 0 {
		t.Fatal("no event recorded into the freshly attached ring")
	}
}
