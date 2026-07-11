// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build chiptool

package chiptool

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/tests/chiptool/harness"
)

// TestSendReceive_WindowCovering exercises the WindowCovering (0x0102)
// SEND/RECEIVE matrix cells against HmIP-BROLL, the lift-only Cover
// variant that is actually present in godevccu's fleet today. The
// cluster has two sibling EndProductType variants — Blind (lift+tilt,
// HmIP-FBL) and Garage (HmIP-MOD-HO) — covered by
// [TestSendReceive_WindowCovering_Tilt] and
// [TestSendReceive_WindowCovering_Garage]; both currently skip because
// godevccu's DefaultDevices fleet does not yet embed those fixtures
// (see docs/matter/chiptool-send-receive-matrix.md WindowCovering
// godevccu_gap).
func TestSendReceive_WindowCovering(t *testing.T) {
	b := requireBridge(t)
	eps := discoverEndpointsWith(t, b, 0x0102, 1)
	if len(eps) == 0 {
		t.Skip("no WindowCovering endpoint — godevccu fleet lacks a Cover/Blind/Garage device")
	}
	ep := eps[0]
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	address, _, ok := b.ResolveCCUAddress(ctx, t, ep, 0x0102)
	if !ok {
		t.Fatalf("could not resolve CCU address for windowcovering endpoint %d", ep)
	}

	// SEND — GoToLiftPercentage(7500) must reach the CCU as
	// LEVEL = 1 - 7500/10000 = 0.25. Matter percent100ths is
	// 0=open/10000=closed while the CCU's LEVEL is 0=closed/1=open,
	// so the write inverts. Mirrors hmLevelToMatterPct100ths /
	// matterPct100thsToHMLevel in internal/model/custom/cover/matter.go.
	t.Run("send/go-to-lift-percentage", func(t *testing.T) {
		if _, err := b.SharedCtl.Invoke(ctx, t, "windowcovering", "go-to-lift-percentage", ep, "7500"); err != nil {
			t.Fatalf("invoke go-to-lift-percentage: %v", err)
		}
		// GoTo*Percentage commands are slider-gesture debounced: the
		// bridge acknowledges immediately and defers the radio write
		// (~400 ms gesture-start delay) so controller slider drags
		// coalesce into one duty-cycle-friendly write. Poll for the
		// deferred CCU write instead of a tight readback.
		// UpOrOpen/DownOrClose/Stop stay immediate writes.
		deadline := time.Now().Add(2500 * time.Millisecond)
		var got any
		var ok bool
		for {
			got, ok = b.CCU.GetDPValue(address, "LEVEL")
			if ok && valueNear(got, 0.25, 0.01) {
				return
			}
			if time.Now().After(deadline) {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if !ok {
			t.Fatalf("LEVEL absent on CCU after go-to-lift-percentage (waited 2.5s for the debounced write)")
		}
		t.Fatalf("CCU LEVEL = %v, want ~0.25 (waited 2.5s for the debounced write)", got)
	})

	// SEND — DownOrClose must drive LEVEL to fully closed (0.0).
	t.Run("send/down-or-close", func(t *testing.T) {
		if _, err := b.SharedCtl.Invoke(ctx, t, "windowcovering", "down-or-close", ep); err != nil {
			t.Fatalf("invoke down-or-close: %v", err)
		}
		got, ok := b.CCU.GetDPValue(address, "LEVEL")
		if !ok {
			t.Fatalf("LEVEL absent on CCU after down-or-close")
		}
		if !valueNear(got, 0.0, 0.01) {
			t.Fatalf("CCU LEVEL = %v, want ~0.0 (closed)", got)
		}
	})

	// SEND — UpOrOpen must drive LEVEL to fully open (1.0).
	t.Run("send/up-or-open", func(t *testing.T) {
		if _, err := b.SharedCtl.Invoke(ctx, t, "windowcovering", "up-or-open", ep); err != nil {
			t.Fatalf("invoke up-or-open: %v", err)
		}
		got, ok := b.CCU.GetDPValue(address, "LEVEL")
		if !ok {
			t.Fatalf("LEVEL absent on CCU after up-or-open")
		}
		if !valueNear(got, 1.0, 0.01) {
			t.Fatalf("CCU LEVEL = %v, want ~1.0 (open)", got)
		}
	})

	// SEND — StopMotion. STOP is an ACTION parameter: Cover.Stop fires
	// it as a hard-coded `true` write and the CCU never persists STOP
	// as a readable value, so there is no GetDPValue readback to
	// assert against — only that chip-tool's Invoke itself reported
	// SUCCESS. The harness has no OnSetValue hook / event-capture
	// primitive yet to observe the fired write directly; this cell is
	// therefore intentionally read-back-free.
	t.Run("send/stop-motion", func(t *testing.T) {
		out, err := b.SharedCtl.Invoke(ctx, t, "windowcovering", "stop-motion", ep)
		if err != nil {
			t.Fatalf("invoke stop-motion: %v", err)
		}
		if !harness.CommandSuccess(out) {
			t.Errorf("stop-motion did not report success:\n%s", out)
		}
	})

	// RECEIVE — an external LEVEL push (device dial / CCU program)
	// must reach the controller as a proactive
	// CurrentPositionLiftPercent100ths report. The preceding
	// send/up-or-open cell leaves LEVEL at 1.0 (pct 0); 0.5 is
	// distinct so the subscribe's own initial report cannot
	// pre-satisfy want().
	t.Run("receive/current-position-lift-percent100ths", func(t *testing.T) {
		out, err := harness.AwaitProactiveReport(ctx, t, b.SharedCtl,
			"windowcovering", "current-position-lift-percent100ths", ep,
			func() error { return b.CCU.FireDeviceEvent(address, "LEVEL", 0.5) },
			func(out string) bool {
				v, ok := harness.FindAttrUint(out, "CurrentPositionLiftPercent100ths")
				return ok && v == 5000
			},
			30*time.Second)
		if err != nil {
			t.Fatalf("await proactive CurrentPositionLiftPercent100ths=5000: %v\n%s", err, out)
		}
	})
}

// windowcoveringClassify reads the WindowCovering Type (0x0000) and
// EndProductType (0x000D) attributes off ep and reports which cover
// variant materialized the endpoint. Blind (HmIP-FBL) and Garage
// (HmIP-MOD-HO) project onto the *same* Matter cluster ID as the
// plain Shutter [TestSendReceive_WindowCovering] already exercises,
// so this attribute pair is the only way to pick the right endpoint
// out of a mixed fleet: Blind reports Type=8 (TiltBlindLiftAndTilt) +
// EndProductType=10 (InteriorBlind), Garage reports Type=0
// (RollerShade — the enum has no garage code) + EndProductType=0
// (RollerShade). A plain Shutter reports Type=6 + EndProductType=17
// and falls into the "cover" default. See the MatterRead methods on
// coverWCServer, blindWCServer, and garageWCServer in
// internal/model/custom/cover/matter.go (values sourced from
// matter.js WindowCovering.element.ts).
func windowcoveringClassify(ctx context.Context, t *testing.T, ctl *harness.Controller, ep uint16) string {
	t.Helper()
	typeOut, err := ctl.ReadAttr(ctx, t, "windowcovering", "type", ep)
	if err != nil {
		return "unknown"
	}
	epOut, err := ctl.ReadAttr(ctx, t, "windowcovering", "end-product-type", ep)
	if err != nil {
		return "unknown"
	}
	typ, okT := harness.FindAttrUint(typeOut, "Type")
	ept, okE := harness.FindAttrUint(epOut, "EndProductType")
	switch {
	case okE && ept == 10 && okT && typ == 8:
		return "blind"
	case okE && ept == 0 && okT && typ == 0:
		return "garage"
	default:
		return "cover"
	}
}

// windowcoveringFindByKind scans every WindowCovering endpoint and
// returns the first one [windowcoveringClassify] tags as kind
// ("blind" or "garage"). Used by the Tilt/Garage variant tests to
// pick their endpoint out of a fleet that also contains the plain
// Shutter [TestSendReceive_WindowCovering] exercises.
func windowcoveringFindByKind(ctx context.Context, t *testing.T, b *harness.Bridge, kind string) (uint16, bool) {
	t.Helper()
	eps := discoverEndpointsWith(t, b, 0x0102, 0)
	for _, ep := range eps {
		if windowcoveringClassify(ctx, t, b.SharedCtl, ep) == kind {
			return ep, true
		}
	}
	return 0, false
}

// TestSendReceive_WindowCovering_Tilt exercises the Blind (HmIP-FBL,
// lift+tilt) SEND cell and pins a documented model-layer RECEIVE gap:
// a LEVEL_2-only CCU push has no dedicated Matter change notifier.
// Blind inherits OnMatterValueChanged from the embedded
// *generic.Float bound to LEVEL only (see the "Cover and Blind
// inherit OnMatterValueChanged from the embedded *generic.Float"
// compile-time-assertion comment in
// internal/model/custom/cover/matter.go), so a tilt-only external
// change never dirty-marks the endpoint and therefore never reaches a
// live Subscribe as a proactive report — even though the value is
// correct and servable on demand via a plain read.
//
// HmIP-FBL is not yet part of godevccu's DefaultDevices fleet (see
// docs/matter/chiptool-send-receive-matrix.md WindowCovering
// godevccu_gap), so this currently skips; it is written to run
// unattended the moment the fixture lands.
func TestSendReceive_WindowCovering_Tilt(t *testing.T) {
	b := requireBridge(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	ep, found := windowcoveringFindByKind(ctx, t, b, "blind")
	if !found {
		t.Skip("no Blind (lift+tilt) WindowCovering endpoint — HmIP-FBL not yet in godevccu fleet")
	}

	address, _, ok := b.ResolveCCUAddress(ctx, t, ep, 0x0102)
	if !ok {
		t.Fatalf("could not resolve CCU address for blind endpoint %d", ep)
	}

	// SEND — GoToTiltPercentage(3000) must be accepted. Blind writes
	// LEVEL + LEVEL_2 atomically through a single COMBINED_PARAMETER
	// (HmIP) / LEVEL_COMBINED (HM) string rather than a plain LEVEL_2
	// paramset write (see Blind.sendCombined in
	// internal/model/custom/cover/blind.go), so a tight GetDPValue
	// readback here would pin godevccu's own COMBINED_PARAMETER
	// decode rather than the bridge's encoding — deferred until the
	// fixture exists and that behaviour can be observed directly.
	t.Run("send/go-to-tilt-percentage", func(t *testing.T) {
		out, err := b.SharedCtl.Invoke(ctx, t, "windowcovering", "go-to-tilt-percentage", ep, "3000")
		if err != nil {
			t.Fatalf("invoke go-to-tilt-percentage: %v", err)
		}
		if !harness.CommandSuccess(out) {
			t.Errorf("go-to-tilt-percentage did not report success:\n%s", out)
		}
	})

	// RECEIVE — GAP: LEVEL_2 rides on LEVEL's change notifier only, so
	// a tilt-only push must NOT surface as a proactive report. Assert
	// the timeout first (short — this must genuinely not arrive, not
	// merely arrive slowly), then confirm the value IS correct via an
	// explicit on-demand read. This pins the documented limitation
	// instead of silently tolerating it.
	t.Run("receive/current-position-tilt-percent100ths-gap", func(t *testing.T) {
		out, err := harness.AwaitProactiveReport(ctx, t, b.SharedCtl,
			"windowcovering", "current-position-tilt-percent100ths", ep,
			func() error { return b.CCU.FireDeviceEvent(address, "LEVEL_2", 0.4) },
			func(out string) bool {
				v, ok := harness.FindAttrUint(out, "CurrentPositionTiltPercent100ths")
				return ok && v == 6000
			},
			8*time.Second)
		if err == nil {
			t.Fatalf("expected AwaitProactiveReport to time out for a LEVEL_2-only push (no dedicated notifier — documented gap), but it matched:\n%s", out)
		}

		read, err := b.SharedCtl.ReadAttr(ctx, t, "windowcovering", "current-position-tilt-percent100ths", ep)
		if err != nil {
			t.Fatalf("on-demand read after LEVEL_2 push: %v", err)
		}
		if v, ok := harness.FindAttrUint(read, "CurrentPositionTiltPercent100ths"); !ok || v != 6000 {
			t.Fatalf("CurrentPositionTiltPercent100ths on-demand = %v (ok=%v), want 6000\n%s", v, ok, read)
		}
	})
}

// TestSendReceive_WindowCovering_Garage exercises the Garage
// (HmIP-MOD-HO) SEND/RECEIVE cells. Garage projects the WindowCovering
// cluster onto a synthetic position derived from the discrete
// DOOR_STATE enum (OPEN=1.0/pct 0, VENTILATION_POSITION=0.5/pct 5000,
// CLOSED=0.0/pct 10000) rather than a continuous LEVEL, and — unlike
// Blind's LEVEL_2 axis — DOOR_STATE genuinely has a dedicated Matter
// change notifier (Garage.OnMatterValueChanged fans in both
// doorStateDp and sectionDp; see internal/model/custom/cover/matter.go),
// so a wall-button / CCU-program door change does reach a live
// Subscribe.
//
// HmIP-MOD-HO is not yet part of godevccu's DefaultDevices fleet (see
// docs/matter/chiptool-send-receive-matrix.md WindowCovering
// godevccu_gap), so this currently skips; it is written to run
// unattended the moment the fixture lands.
func TestSendReceive_WindowCovering_Garage(t *testing.T) {
	b := requireBridge(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	ep, found := windowcoveringFindByKind(ctx, t, b, "garage")
	if !found {
		t.Skip("no Garage WindowCovering endpoint — HmIP-MOD-HO not yet in godevccu fleet")
	}

	address, _, ok := b.ResolveCCUAddress(ctx, t, ep, 0x0102)
	if !ok {
		t.Fatalf("could not resolve CCU address for garage endpoint %d", ep)
	}

	// SEND — UpOrOpen must issue DOOR_COMMAND=OPEN.
	t.Run("send/up-or-open", func(t *testing.T) {
		if _, err := b.SharedCtl.Invoke(ctx, t, "windowcovering", "up-or-open", ep); err != nil {
			t.Fatalf("invoke up-or-open: %v", err)
		}
		got, ok := b.CCU.GetDPValue(address, "DOOR_COMMAND")
		if !ok {
			t.Fatalf("DOOR_COMMAND absent on CCU after up-or-open")
		}
		if s, sok := got.(string); !sok || s != "OPEN" {
			t.Fatalf("CCU DOOR_COMMAND = %v (%T), want \"OPEN\"", got, got)
		}
	})

	// SEND — DownOrClose must issue DOOR_COMMAND=CLOSE.
	t.Run("send/down-or-close", func(t *testing.T) {
		if _, err := b.SharedCtl.Invoke(ctx, t, "windowcovering", "down-or-close", ep); err != nil {
			t.Fatalf("invoke down-or-close: %v", err)
		}
		got, ok := b.CCU.GetDPValue(address, "DOOR_COMMAND")
		if !ok {
			t.Fatalf("DOOR_COMMAND absent on CCU after down-or-close")
		}
		if s, sok := got.(string); !sok || s != "CLOSE" {
			t.Fatalf("CCU DOOR_COMMAND = %v (%T), want \"CLOSE\"", got, got)
		}
	})

	// RECEIVE — an external DOOR_STATE push (wall button / CCU
	// program) synthesizes the WindowCovering lift position:
	// VENTILATION_POSITION -> Position 0.5 -> pct 5000. Distinct from
	// the OPEN/CLOSE (pct 0 / pct 10000) commands issued above.
	t.Run("receive/current-position-lift-percent100ths", func(t *testing.T) {
		out, err := harness.AwaitProactiveReport(ctx, t, b.SharedCtl,
			"windowcovering", "current-position-lift-percent100ths", ep,
			func() error { return b.CCU.FireDeviceEvent(address, "DOOR_STATE", "VENTILATION_POSITION") },
			func(out string) bool {
				v, ok := harness.FindAttrUint(out, "CurrentPositionLiftPercent100ths")
				return ok && v == 5000
			},
			30*time.Second)
		if err != nil {
			t.Fatalf("await proactive CurrentPositionLiftPercent100ths=5000: %v\n%s", err, out)
		}
	})
}
