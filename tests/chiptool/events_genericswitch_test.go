// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build chiptool

package chiptool

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/tests/chiptool/harness"
)

// TestSendReceive_GenericSwitch is the vertical slice for the
// GenericSwitch (Switch, 0x003B) cluster — HmIP button/action DPs
// surfaced through a button channel (HmIP-BSM ch1/2 or HmIP-BDT
// ch1/2; needs allowlist exposure, no dedicated device in the
// default fleet).
//
// This cluster diverges from every other row in the send/receive
// matrix: it is EVENT-driven, not attribute-driven. A button press
// has no persisted attribute to read back (CurrentPosition is a
// spec-mandated constant 0 for a momentary switch — see
// cluster/wire/genericswitch.go MatterRead), so the RECEIVE cell
// goes through chip-tool's `subscribe-event` path
// (Controller.SubscribeEventAndAwait) fed by the bridge's
// MatterEventEmitter, instead of harness.AwaitProactiveReport's
// attribute-report path.
func TestSendReceive_GenericSwitch(t *testing.T) {
	t.Skip("WIP: GenericSwitch button events are transient ACTION events. chip-tool's subscribe-event is a one-shot that exits before an event fired afterwards arrives, and — unlike an attribute — a fired event is not re-readable, so neither the fire-then-subscribe nor the subscribe-then-fire ordering captures it. Deferred to a follow-up that drives a persistent Matter event listener.")
	b := requireBridge(t)
	eps := discoverEndpointsWith(t, b, 0x003B, 1)
	if len(eps) == 0 {
		t.Skip("no GenericSwitch (0x003B) endpoint — button channel needs allowlist exposure; godevccu fleet has no exposed HmIP-BSM/BDT button channel")
	}
	ep := eps[0]
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	address, _, ok := b.ResolveCCUAddress(ctx, t, ep)
	if !ok {
		t.Fatalf("could not resolve CCU address for GenericSwitch endpoint %d", ep)
	}

	// SEND — negative: Switch (0x003B) is server-to-client only. Every
	// attribute is read-only per Matter §1.13; genericswitch.go
	// MatterWrite rejects unconditionally with a "read-only" error,
	// which the dispatcher's writeErrorStatus heuristic maps to IM
	// StatusUnsupportedWrite (0x88). chip-tool itself always offers a
	// `write` subcommand per attribute (the ZAP template does not know
	// about server-side read-only enforcement), so this exercises the
	// bridge's own rejection, not a client-side refusal to issue the
	// write.
	t.Run("send/current-position-rejected", func(t *testing.T) {
		out, err := b.SharedCtl.WriteAttr(ctx, t, "switch", "current-position", "1", ep)
		if err == nil {
			t.Fatalf("expected write rejection for Switch.CurrentPosition, chip-tool reported success:\n%s", out)
		}
		status, found := harness.WriteStatus(out)
		if !found {
			t.Fatalf("could not parse write status from output:\n%s", out)
		}
		if status != "0x88" {
			t.Fatalf("CurrentPosition write status = %s, want 0x88 (UNSUPPORTED_WRITE)\n%s", status, out)
		}
	})

	// RECEIVE — initial-press: a simulated PRESS_SHORT must reach the
	// controller as a proactive InitialPress event. genericSwitchAwaitEvent
	// mirrors harness.AwaitProactiveReport's subscribe-then-fire
	// ordering (subscribe first, let it settle, THEN inject) so a
	// broken/unwired MatterEventEmitter is caught rather than masked by
	// some other coincidental report.
	//
	// PRESS_SHORT is an ACTION-typed param: godevccu's PutParamset
	// hard-codes the fired value to true and never persists it (matrix
	// gap G6), so FireDeviceEvent is fire-only — there is no GetDPValue
	// readback for this direction.
	t.Run("receive/initial-press", func(t *testing.T) {
		out, err := genericSwitchAwaitEvent(ctx, t, b.SharedCtl, "initial-press", ep,
			func() error { return b.CCU.FireDeviceEvent(address, "PRESS_SHORT", true) },
			genericSwitchWantEvent("InitialPress", "NewPosition"),
			30*time.Second)
		if err != nil {
			t.Fatalf("await proactive InitialPress event: %v\n%s", err, out)
		}
	})

	// RECEIVE — short-release: model/generic/button.go's
	// WireMatterSwitchHandler fans PRESS_SHORT out to BOTH
	// FireInitialPress and FireShortRelease on the same rising edge;
	// assert the release half independently so a regression that drops
	// just the MSR (Momentary Switch Release) event is caught even if
	// InitialPress still fires.
	t.Run("receive/short-release", func(t *testing.T) {
		out, err := genericSwitchAwaitEvent(ctx, t, b.SharedCtl, "short-release", ep,
			func() error { return b.CCU.FireDeviceEvent(address, "PRESS_SHORT", true) },
			genericSwitchWantEvent("ShortRelease", "PreviousPosition"),
			30*time.Second)
		if err != nil {
			t.Fatalf("await proactive ShortRelease event: %v\n%s", err, out)
		}
	})
}

// genericSwitchAwaitEvent is the GenericSwitch (event-report)
// counterpart to harness.AwaitProactiveReport, which only knows how
// to drive chip-tool's attribute-report `subscribe` path. It
// subscribes to the event FIRST via Controller.SubscribeEventAndAwait,
// waits for the subscription to settle, THEN fires inject() to drive
// the simulated device-originated press — so the only way want() can
// be satisfied is the bridge's own MatterEventEmitter wiring, not a
// race against the subscribe's own setup.
func genericSwitchAwaitEvent(
	ctx context.Context, t *testing.T, ctl *harness.Controller,
	evt string, endpointID uint16,
	inject func() error,
	want func(out string) bool,
	timeout time.Duration,
) (string, error) {
	t.Helper()
	go func() {
		select {
		case <-time.After(3 * time.Second):
		case <-ctx.Done():
			return
		}
		if err := inject(); err != nil {
			t.Logf("genericSwitchAwaitEvent: inject failed: %v", err)
		}
	}()
	// maxIntervalSec is set well beyond the timeout so no heartbeat
	// report can satisfy want() before the injected press does.
	maxIntervalSec := int(timeout/time.Second) + 60
	return ctl.SubscribeEventAndAwait(ctx, t, "switch", evt, endpointID, 0, maxIntervalSec, want, timeout)
}

// genericSwitchWantEvent builds a want() predicate for
// genericSwitchAwaitEvent that requires BOTH the event's chip-tool
// label (e.g. "InitialPress") and its payload field name (e.g.
// "NewPosition") to appear in the scanned output — chip-tool's
// DataModelLogger prints the event as "<Label>: {" followed by the
// nested "<Field>: <value>" line, so requiring both rules out a
// coincidental substring match against an unrelated log line.
func genericSwitchWantEvent(label, field string) func(out string) bool {
	return func(out string) bool {
		return strings.Contains(out, label) && strings.Contains(out, field)
	}
}
