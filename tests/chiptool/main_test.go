// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build chiptool

package chiptool

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/tests/chiptool/harness"
)

// sharedBridge is the per-suite bridge handle. Tests reach for it via
// [requireBridge]; [TestMain] populates it once and the cleanup hook
// registered after `m.Run()` tears it down when the test binary
// exits.
var (
	sharedBridge        *harness.Bridge
	sharedBridgeOnce    sync.Once
	sharedBridgeErr     error
	sharedBridgeCleanup func()
)

// TestMain brings up the daemon + godevccu + chip-tool fabric for
// the entire suite. The bridge's storage dirs (daemon SQLite,
// chip-tool KVS, godevccu listeners) live for the lifetime of the
// `go test` binary so per-test cleanup hooks cannot tear them out
// from under another test.
//
// When chip-tool is unreachable, TestMain still runs m.Run() so
// individual tests can emit SKIP markers via [requireBridge] — the
// canonical Go-test UX for "missing prerequisite".
func TestMain(m *testing.M) {
	code := m.Run()
	if sharedBridgeCleanup != nil {
		sharedBridgeCleanup()
	}
	if sharedBridge != nil {
		sharedBridge.Stop()
	}
	os.Exit(code)
}

// requireBridge brings up the shared bridge on first call, then
// returns it on every subsequent call. If chip-tool is unavailable
// we t.Skip — every test that asks for the bridge implicitly
// requires chip-tool.
func requireBridge(t *testing.T) *harness.Bridge {
	t.Helper()
	chipBin := harness.RequireChipTool(t)
	sharedBridgeOnce.Do(func() {
		// Probe chip-tool's existence one more time before paying the
		// daemon-spawn cost — `RequireChipTool` already does this for
		// the calling test, but the shared bring-up needs an
		// equivalent guard so a per-test skip does not leave a
		// partially started daemon behind.
		if _, err := exec.LookPath("chip-tool"); err != nil && os.Getenv(harness.ChipToolBinaryEnv) == "" {
			return
		}
		b, cleanup, err := harness.StartShared(chipBin, harness.Options{
			CASEEnabled: true,
		})
		if err != nil {
			sharedBridgeErr = fmt.Errorf("bring up shared bridge: %w", err)
			return
		}
		sharedBridgeCleanup = cleanup

		// Best-effort: light up every mappable exposure so chip-tool
		// sees a non-empty PartsList under the Aggregator. Without it
		// the bridge stays empty (no exposures = no bridged
		// endpoints), and the per-cluster tests SKIP via
		// discoverEndpointsWith. A failure here (currently observed:
		// /matter/exposable returns 500 on some daemon builds) is
		// logged but does NOT abort the suite — the dependent tests
		// degrade to SKIP, the rest still run.
		expCtx, expCancel := context.WithTimeout(context.Background(), 15*time.Second)
		if n, err := b.EnableAllExposures(expCtx); err != nil {
			fmt.Fprintf(os.Stderr, "chiptool: enable exposures failed (%v) — cluster/subscribe/invoke tests will SKIP\n", err)
		} else if n > 0 {
			// Bridge reassembles asynchronously after the bulk
			// update; give it a brief settle window before chip-tool
			// commissions against a still-rebuilding topology.
			time.Sleep(1500 * time.Millisecond)
		}
		expCancel()

		// Commission the shared controller. PairFull exercises
		// PASE→AddNOC→CASE Sigma1; subsequent reads/subscribes ride
		// the resulting operational session.
		ctl, ctlCleanup, err := harness.NewControllerShared(chipBin, harness.SharedFabricNodeID)
		if err != nil {
			sharedBridgeErr = fmt.Errorf("controller: %w", err)
			cleanup()
			return
		}
		prevCleanup := sharedBridgeCleanup
		sharedBridgeCleanup = func() {
			ctlCleanup()
			prevCleanup()
		}

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		out, err := ctl.PairFull(ctx, t, harness.PairTargetHost, b.MatterPort())
		if err != nil {
			sharedBridgeErr = fmt.Errorf("commission shared fabric: %w\n%s", err, out)
			return
		}
		if !harness.PairingSuccess(out) {
			sharedBridgeErr = fmt.Errorf("shared-fabric pairing did not report success:\n%s", out)
			return
		}
		b.SharedCtl = ctl
		sharedBridge = b
	})
	if sharedBridgeErr != nil {
		t.Fatalf("shared bridge bring-up failed: %v", sharedBridgeErr)
	}
	if sharedBridge == nil {
		t.Skip("shared bridge not initialised — chip-tool missing or daemon failed to start")
	}
	return sharedBridge
}
