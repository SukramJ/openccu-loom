// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package generic

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/go-fabric/cluster/onoff"

	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestMatterOnOffServerAcceptsASynchronousEcho pins the go-fabric contract
// the profiles rely on: a CCU whose event for a write arrives before the
// write's reply confirms through [onoff.Server.Set] from inside the Sink,
// on the RPC callback goroutine that handles every event of the
// interface. go-fabric 0.3.0 ran the Sink under the lock Set takes and
// held that goroutine until the command timed out (the chip-tool suite
// found it; go-fabric #63); 0.3.1 moved the Sink outside that lock. The
// Sink here plays that CCU, and the test fails against a module that
// reintroduces the lock: On never returns and the echo is held.
func TestMatterOnOffServerAcceptsASynchronousEcho(t *testing.T) {
	t.Parallel()
	var (
		srv     *onoff.Server
		tracker hmtypes.DataVersionTracker
		echoed  = make(chan time.Duration, 1)
	)
	srv = MustMatterOnOffServer(MatterOnOffConfig{
		Lighting:    true,
		DataVersion: &tracker,
		Sink: func(_ context.Context, on bool) error {
			start := time.Now()
			srv.Set(on) // the echo, from inside the Sink
			echoed <- time.Since(start)
			return nil
		},
	})

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
		t.Fatalf("the confirming goroutine was held for %s inside the Sink", waited)
	}
	if v, _ := srv.MatterRead(onoff.AttrOnOff); v != true {
		t.Fatalf("OnOff after On = %v, want true", v)
	}
}
