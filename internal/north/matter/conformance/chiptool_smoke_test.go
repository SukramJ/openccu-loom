// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build chiptool

package conformance_test

import (
	"context"
	"errors"
	"os/exec"
	"testing"
	"time"
)

// TestChipToolPairingOnNetworkLong is the ship-blocker: chip-tool
// MUST be able to commission the bridge end-to-end via PASE +
// CASE. The test runs `chip-tool pairing onnetwork-long` with a
// pre-known passcode against a locally-booted bridge instance.
//
// Build-tag-gated (`-tags chiptool`) because:
//
//   - chip-tool is not a Go dependency; CI installs it out-of-band.
//   - The bridge must be already running on the target host's
//     loopback / link-local mDNS zone — the test orchestrator is
//     responsible for that.
//   - The test consumes 5–10 s and emits multicast traffic.
//
// Skipped automatically if chip-tool is not in $PATH.
func TestChipToolPairingOnNetworkLong(t *testing.T) {
	if _, err := exec.LookPath("chip-tool"); err != nil {
		t.Skip("chip-tool not found in PATH")
	}

	const (
		nodeID   = "0x1234"
		passcode = "20202021"
		timeout  = 30 * time.Second
	)

	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	defer cancel()

	cmd := exec.CommandContext(
		ctx,
		"chip-tool",
		"pairing", "onnetwork-long",
		nodeID, passcode, "3840", // 3840 = the long discriminator the bridge advertises
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			t.Fatalf("chip-tool pairing timed out after %s\nstdout/err:\n%s", timeout, out)
		}
		t.Fatalf("chip-tool pairing failed: %v\nstdout/err:\n%s", err, out)
	}

	t.Logf("chip-tool pairing succeeded:\n%s", out)
}
