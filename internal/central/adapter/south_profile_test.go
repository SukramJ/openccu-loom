// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// scriptedProbe reports ready from the readyAfter-th call on.
type scriptedProbe struct {
	calls      atomic.Int32
	readyAfter int32 // 0 = never ready
}

func (p *scriptedProbe) Probe(context.Context) (ready bool, reason string) {
	n := p.calls.Add(1)
	if p.readyAfter > 0 && n >= p.readyAfter {
		return true, ""
	}
	return false, "scripted: not yet"
}

func (*scriptedProbe) Target() string { return "scripted" }

func TestWaitReadyHonoursUnboundedAndBoundedTimeouts(t *testing.T) {
	t.Parallel()
	const tick = 2 * time.Millisecond

	t.Run("ready on first probe", func(t *testing.T) {
		t.Parallel()
		p := &scriptedProbe{readyAfter: 1}
		if !waitReady(context.Background(), "c", p, CCUReadinessConfig{Timeout: time.Second, Interval: tick}, nil) {
			t.Fatal("waitReady = false for a probe that is ready at once")
		}
		if got := p.calls.Load(); got != 1 {
			t.Errorf("probe calls = %d, want 1", got)
		}
	})

	t.Run("bounded wait gives up", func(t *testing.T) {
		t.Parallel()
		p := &scriptedProbe{}
		start := time.Now()
		if waitReady(context.Background(), "c", p, CCUReadinessConfig{Timeout: 30 * time.Millisecond, Interval: tick}, nil) {
			t.Fatal("waitReady = true for a probe that never becomes ready")
		}
		if elapsed := time.Since(start); elapsed > 2*time.Second {
			t.Errorf("bounded wait took %v; the timeout was not honoured", elapsed)
		}
	})

	t.Run("unbounded wait outlasts the default budget's cadence", func(t *testing.T) {
		t.Parallel()
		// Ready only on the 20th probe: far more probes than a 30 ms bounded
		// budget would allow at this cadence, so only an unbounded wait sees it.
		p := &scriptedProbe{readyAfter: 20}
		if !waitReady(context.Background(), "c", p, CCUReadinessConfig{Timeout: -1, Interval: tick}, nil) {
			t.Fatal("unbounded waitReady = false for a probe that eventually becomes ready")
		}
		if got := p.calls.Load(); got != 20 {
			t.Errorf("probe calls = %d, want 20", got)
		}
	})

	t.Run("unbounded wait stops on cancel", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		defer cancel()
		if waitReady(ctx, "c", &scriptedProbe{}, CCUReadinessConfig{Timeout: -1, Interval: tick}, nil) {
			t.Fatal("unbounded waitReady = true after ctx cancel")
		}
	})
}

func TestCCUProfileProbesTheBootMarker(t *testing.T) {
	t.Parallel()
	cc := &config.CentralConfig{Name: "c", Host: "ccu.example", JSONRPCPort: 8181}
	p, err := southProfileFor(cc, nil)
	if err != nil {
		t.Fatalf("southProfileFor: %v", err)
	}
	if got := p.SystemType(); got != hmenum.SystemTypeCCU {
		t.Errorf("SystemType = %q, want ccu", got)
	}
	if got, want := p.Readiness().Target(), "http://ccu.example:8181/ise/checkrega.cgi"; got != want {
		t.Errorf("readiness target = %q, want %q", got, want)
	}
	if p.Liveness() == nil {
		t.Error("a CCU with a host must carry the hub-plane liveness probe")
	}
	noHost, err := southProfileFor(&config.CentralConfig{Name: "c"}, nil)
	if err != nil {
		t.Fatalf("southProfileFor: %v", err)
	}
	if noHost.Liveness() != nil {
		t.Error("a CCU without a host has nothing to poll; Liveness must be nil")
	}
}

// TestUnsupportedSystemTypeIsVisibleAndNotBroughtUp pins what a central of a
// system type this build cannot drive yet looks like: the profile selection
// refuses it with errSystemTypeNotSupported, and the bring-up manager keeps
// it registered with a degraded startup component naming the reason instead
// of starting a half bring-up.
func TestUnsupportedSystemTypeIsVisibleAndNotBroughtUp(t *testing.T) {
	t.Parallel()
	cc := &config.CentralConfig{Name: "box", Host: "box.local", SystemType: hmenum.SystemTypeAuto}
	if _, err := southProfileFor(cc, nil); !errors.Is(err, errSystemTypeNotSupported) {
		t.Fatalf("auto: southProfileFor = %v, want errSystemTypeNotSupported", err)
	}
	if _, err := southProfileFor(&config.CentralConfig{Name: "x", SystemType: "homegear"}, nil); err == nil {
		t.Fatal("an unknown system type was accepted")
	}

	reg, unit := registryWithUnit(t, "box")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	mgr, err := WireCentrals(ctx, &config.Config{}, reg, WireDeps{}, nil)
	if err != nil {
		t.Fatalf("WireCentrals: %v", err)
	}
	t.Cleanup(mgr.Teardown)
	auto := config.CentralConfig{Name: "box", Host: "box.local", SystemType: hmenum.SystemTypeAuto}
	if !mgr.AddCentral(&auto, unit) {
		t.Fatal("AddCentral refused the central")
	}
	comp, ok := unit.Health.Get(startupHealthComponent("box"))
	if !ok || !strings.Contains(comp.LastSample.Note, "not supported") {
		t.Fatalf("startup component = %+v (present %v), want a note naming the unsupported system type", comp, ok)
	}
	if unit.IsSouthboundReady() {
		t.Error("an unsupported central reported southbound ready")
	}
}
