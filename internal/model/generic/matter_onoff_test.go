// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package generic

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/go-fabric/cluster/onoff"

	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestMatterOnOffFeedbackDoesNotDeadlockOnASynchronousEcho pins the
// property the chip-tool suite found missing: a CCU whose event for a
// write arrives before the write's reply confirms from inside the Sink,
// while onoff.Server holds its operation lock. A feedback that stored
// synchronously would block that confirmation — and with it the RPC
// callback goroutine that handles every event of the interface — until
// the command timed out. The Sink here plays that CCU: it confirms the
// value through the feedback before it returns.
func TestMatterOnOffFeedbackDoesNotDeadlockOnASynchronousEcho(t *testing.T) {
	t.Parallel()
	var (
		feedback *MatterOnOffFeedback
		tracker  hmtypes.DataVersionTracker
		echoed   = make(chan time.Duration, 1)
	)
	srv := MustMatterOnOffServer(MatterOnOffConfig{
		Lighting:    true,
		DataVersion: &tracker,
		Sink: func(_ context.Context, on bool) error {
			start := time.Now()
			feedback.Set(on) // the echo, from inside the Sink
			echoed <- time.Since(start)
			return nil
		},
	})
	feedback = NewMatterOnOffFeedback(srv)

	done := make(chan error, 1)
	go func() {
		_, err := srv.MatterInvoke(context.Background(), onoff.CmdOn, nil)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("On: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("On never returned: the synchronous echo deadlocked the server")
	}
	if waited := <-echoed; waited > time.Second {
		t.Fatalf("the confirming goroutine was held for %s; it must move on after the bounded wait", waited)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if v, _ := srv.MatterRead(onoff.AttrOnOff); v == true {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the confirmed value never landed on the server")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestMatterOnOffFeedbackStoresTheNewestValue pins the coalescing order:
// a burst of confirmations ends with the last one on the server, never
// an older value stored after a newer one.
func TestMatterOnOffFeedbackStoresTheNewestValue(t *testing.T) {
	t.Parallel()
	var tracker hmtypes.DataVersionTracker
	srv := MustMatterOnOffServer(MatterOnOffConfig{Lighting: true, DataVersion: &tracker})
	feedback := NewMatterOnOffFeedback(srv)
	for i := range 50 {
		feedback.Set(i%2 == 0)
	}
	feedback.Set(true)
	if v, _ := srv.MatterRead(onoff.AttrOnOff); v != true {
		t.Fatalf("OnOff after the burst = %v, want the last confirmed value true", v)
	}
}
