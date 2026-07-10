// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build chiptool

package chiptool

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/tests/chiptool/harness"
)

// TestSendReceive_GenericSwitch is the vertical slice for the GenericSwitch
// (Switch, 0x003B) cluster — HmIP button/action DPs surfaced through a button
// channel (HmIP-BSM ch1/2). It diverges from every other row in the send/
// receive matrix in two ways:
//
//   - It is EVENT-driven, not attribute-driven. A button press has no persisted
//     attribute to read back (CurrentPosition is a spec-mandated constant 0 for
//     a momentary switch), so the read-reflection AwaitProactiveReport path
//     cannot observe it. The RECEIVE cells instead drive a PERSISTENT chip-tool
//     interactive event listener (Controller.SubscribeEventInteractiveAndAwait):
//     the single-command subscribe-event is a one-shot Subscribe-Init that exits
//     before an event fired afterwards arrives, so the subscription must live in
//     an interactive session across the injected press.
//   - The button channel's GenericSwitch endpoint is beyond the shared bridge's
//     wildcard-discovery truncation point, so this test brings up its own
//     isolated single-device bridge and enumerates the endpoint itself.
func TestSendReceive_GenericSwitch(t *testing.T) {
	chipBin := harness.RequireChipTool(t)
	b := harness.Start(t, chipBin, harness.Options{
		CASEEnabled: true,
		Devices:     []string{"HmIP-BSM"},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Light up the exposures so the button channel materialises a GenericSwitch
	// endpoint, then commission so the controller can subscribe to it.
	expCtx, expCancel := context.WithTimeout(ctx, 15*time.Second)
	if _, err := b.EnableAllExposures(expCtx); err != nil {
		t.Fatalf("enable exposures: %v", err)
	}
	expCancel()
	time.Sleep(1500 * time.Millisecond)

	ctl := harness.NewController(t, chipBin, 0x5201)
	out, err := ctl.PairFull(ctx, t, harness.PairTargetHost, b.MatterPort())
	if err != nil {
		t.Fatalf("commission: %v\n%s", err, out)
	}
	if !harness.PairingSuccess(out) {
		t.Fatalf("pairing did not report success:\n%s", out)
	}
	// [Bridge.ResolveCCUAddress] reads through b.SharedCtl.
	b.SharedCtl = ctl

	// Find the lowest-ID GenericSwitch (0x003B) endpoint via one wildcard
	// server-list read (per-EP reads accumulate CASE sessions).
	slOut, err := ctl.ReadAttr(ctx, t, "descriptor", "server-list", 0xFFFF)
	if err != nil {
		t.Fatalf("wildcard server-list: %v", err)
	}
	perEP := harness.ServerListIDsPerEndpoint(slOut)
	eps := make([]uint16, 0, len(perEP))
	for ep := range perEP {
		eps = append(eps, ep)
	}
	sort.Slice(eps, func(i, j int) bool { return eps[i] < eps[j] })
	var ep uint16
	found := false
	for _, e := range eps {
		if harness.HasCluster(perEP[e], 0x003B) {
			ep = e
			found = true
			break
		}
	}
	if !found {
		t.Skip("no GenericSwitch (0x003B) endpoint materialised for HmIP-BSM's button channel")
	}

	address, dpKey, ok := b.ResolveCCUAddress(ctx, t, ep, 0x003B, "PRESS_SHORT")
	if !ok {
		t.Fatalf("could not resolve CCU address for GenericSwitch endpoint %d", ep)
	}

	// SEND — negative: Switch (0x003B) is server-to-client only. Every attribute
	// is read-only per Matter §1.13; the bridge rejects the write with a
	// "read-only" error, mapped to IM StatusUnsupportedWrite (0x88).
	t.Run("send/current-position-rejected", func(t *testing.T) {
		out, _ := ctl.WriteAttr(ctx, t, "switch", "current-position", "1", ep)
		status, ok := harness.WriteStatus(out)
		if !ok {
			t.Fatalf("could not parse write status from output:\n%s", out)
		}
		if status != "0x88" {
			t.Fatalf("CurrentPosition write status = %s, want 0x88 (UNSUPPORTED_WRITE)\n%s", status, out)
		}
	})

	// RECEIVE — a simulated PRESS_SHORT must reach the controller as a proactive
	// InitialPress event. The persistent interactive listener subscribes, fires
	// the press only after the subscription is live, and scans the live stream.
	t.Run("receive/initial-press", func(t *testing.T) {
		out, err := ctl.SubscribeEventInteractiveAndAwait(ctx, t, "switch", "initial-press", ep, 0, 90,
			func() error { return b.CCU.FireDeviceEvent(address, dpKey, true) },
			genericSwitchWantEvent("InitialPress", "NewPosition"),
			30*time.Second)
		if err != nil {
			t.Fatalf("await proactive InitialPress event: %v\n%s", err, out)
		}
	})

	// RECEIVE — model/generic/button.go's WireMatterSwitchHandler fans PRESS_SHORT
	// out to BOTH FireInitialPress and FireShortRelease on the same rising edge;
	// assert the release half independently so a regression that drops just the
	// MSR (Momentary Switch Release) event is caught even if InitialPress fires.
	t.Run("receive/short-release", func(t *testing.T) {
		out, err := ctl.SubscribeEventInteractiveAndAwait(ctx, t, "switch", "short-release", ep, 0, 90,
			func() error { return b.CCU.FireDeviceEvent(address, dpKey, true) },
			genericSwitchWantEvent("ShortRelease", "PreviousPosition"),
			30*time.Second)
		if err != nil {
			t.Fatalf("await proactive ShortRelease event: %v\n%s", err, out)
		}
	})
}

// genericSwitchWantEvent builds a want() predicate that requires BOTH the
// event's chip-tool label (e.g. "InitialPress") and its payload field name
// (e.g. "NewPosition") to appear — chip-tool's DataModelLogger prints the event
// as "<Label>: {" followed by the nested "<Field>: <value>" line, so requiring
// both rules out a coincidental substring match against an unrelated log line.
func genericSwitchWantEvent(label, field string) func(out string) bool {
	return func(out string) bool {
		return strings.Contains(out, label) && strings.Contains(out, field)
	}
}
