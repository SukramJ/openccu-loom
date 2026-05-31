// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build chiptool

package chiptool

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/tests/chiptool/harness"
)

// TestMDNS_DiscoverCommissionable is opt-in. The default suite runs
// with `mdns_advertise: noop` so chip-tool finds the bridge via
// explicit `pairing already-discovered`. Setting
// `OPENCCU_LOOM_CHIPTOOL_MDNS=1` flips the harness to the
// `zeroconf` advertiser and runs `chip-tool discover commissionables`
// to assert the `_matter._tcp` / `_matterc._udp` records appear with
// our discriminator + vendor markers.
//
// Why opt-in: real mDNS publishing contaminates the host's
// `_matter._tcp.local` namespace, requires avahi-daemon with IPv6
// enabled, and is flaky in CI runners that disable multicast on
// loopback. Mirrors the spirit of v9 capability report T1 without
// taking the same flakiness penalty on every run.
func TestMDNS_DiscoverCommissionable(t *testing.T) {
	if os.Getenv(harness.EnableMDNSDiscoveryEnv) != "1" {
		t.Skipf("opt-in: set %s=1 to enable mDNS discovery", harness.EnableMDNSDiscoveryEnv)
	}
	chipBin := harness.RequireChipTool(t)

	// Dedicated bridge with zeroconf advertiser so the assertion is
	// against THIS bridge's records, not the shared one's noop.
	b := harness.Start(t, chipBin, harness.Options{
		CASEEnabled: false, // commissioning window only — no fabric needed
		EnableMDNS:  true,
	})
	if b.MDNS != "zeroconf" {
		t.Fatalf("harness did not switch to zeroconf advertiser (got %q)", b.MDNS)
	}

	// Fresh chip-tool with its own KVS — `discover commissionables`
	// does not need a paired controller, but a clean KVS dodges
	// state from previous runs.
	ctl := harness.NewController(t, chipBin, 0x4000)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	out, _ := ctl.Discover(ctx, t, 15*time.Second)
	// chip-tool exits non-zero on timeout but still prints the
	// records it discovered. Check the markers rather than the exit
	// status.
	if !strings.Contains(out, "Discovered") && !strings.Contains(out, "Vendor ID:") {
		t.Errorf("chip-tool found no commissionable records:\n%s", out)
	}
	// Vendor ID 0xFFF1 = 65521 (matches harness config).
	if !strings.Contains(out, "65521") && !strings.Contains(out, "0xFFF1") {
		t.Errorf("commissionable record missing harness Vendor ID 0xFFF1:\n%s", out)
	}
}
