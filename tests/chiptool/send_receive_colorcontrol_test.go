// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build chiptool

package chiptool

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/tests/chiptool/harness"
)

// TestSendReceive_ColorControl exercises ColorControl (0x0300) against
// HmIP-LSC — the one godevccu fixture that runs hue/saturation AND
// colour-temperature simultaneously (the RGBW light's HS+CT server), so
// a single pairing covers every ColorControl command combination the
// matrix calls for.
//
//   - SEND: MoveToHue / MoveToSaturation / MoveToColorTemperature must
//     each land on the CCU side (GetDPValue ground truth), matching the
//     production hueToMatter/saturationToMatter/kelvinToMireds encoding.
//   - NEGATIVE: every ColorControl attribute is read-only per Matter
//     §3.2.6 — a WriteAttr must be rejected before it ever reaches the
//     cluster server.
//   - RECEIVE: a colour-temperature push from the CCU reaches the
//     server through its Set and is reported proactively.
func TestSendReceive_ColorControl(t *testing.T) {
	// The shared fleet leaves HmIP-LSC out on purpose (its endpoint
	// footprint pushes the wildcard server-list past chip-tool's report
	// truncation), so this test brings up its own single-device bridge
	// and enumerates the ColorControl endpoint itself, as
	// TestSendReceive_GenericSwitch does for the button channel.
	chipBin := harness.RequireChipTool(t)
	b := harness.Start(t, chipBin, harness.Options{
		CASEEnabled: true,
		Devices:     []string{"HmIP-LSC"},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	// Light up the exposures so the light channel materialises its
	// endpoint before the controller is commissioned.
	expCtx, expCancel := context.WithTimeout(ctx, 15*time.Second)
	if _, err := b.EnableAllExposures(expCtx); err != nil {
		t.Fatalf("enable exposures: %v", err)
	}
	expCancel()
	time.Sleep(1500 * time.Millisecond)

	ctl := harness.NewController(t, chipBin, 0x5301)
	out, err := ctl.PairFull(ctx, t, harness.PairTargetHost, b.MatterPort())
	if err != nil {
		t.Fatalf("commission: %v\n%s", err, out)
	}
	if !harness.PairingSuccess(out) {
		t.Fatalf("pairing did not report success:\n%s", out)
	}
	// [Bridge.ResolveCCUAddress] reads through b.SharedCtl.
	b.SharedCtl = ctl

	slOut, err := ctl.ReadAttr(ctx, t, "descriptor", "server-list", 0xFFFF)
	if err != nil {
		t.Fatalf("wildcard server-list: %v", err)
	}
	perEP := harness.ServerListIDsPerEndpoint(slOut)
	eps := make([]uint16, 0, len(perEP))
	for e := range perEP {
		eps = append(eps, e)
	}
	sort.Slice(eps, func(i, j int) bool { return eps[i] < eps[j] })
	var ep uint16
	found := false
	for _, e := range eps {
		if harness.HasCluster(perEP[e], 0x0300) {
			ep = e
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("no ColorControl (0x0300) endpoint materialised for HmIP-LSC; server lists: %v", perEP)
	}

	address, _, ok := b.ResolveCCUAddress(ctx, t, ep, 0x0300)
	if !ok {
		t.Fatalf("could not resolve CCU address for ColorControl endpoint %d", ep)
	}

	// SEND — MoveToHue(127) must land as HUE=180 (matterHueToHM(127) ==
	// 127*360/254 == 180 exactly). OptionsMask/OptionsOverride=1/1 force
	// execution regardless of the light's current on/off state — a
	// ColorControl move-to command silently no-ops while off unless the
	// effective ExecuteIfOff option is set (Matter §3.2.7.2), and this
	// test never touches OnOff.
	t.Run("send/move-to-hue", func(t *testing.T) {
		if _, err := b.SharedCtl.Invoke(ctx, t, "colorcontrol", "move-to-hue", ep,
			"127", "0", "0", "1", "1"); err != nil {
			t.Fatalf("invoke move-to-hue: %v", err)
		}
		got, ok := b.CCU.GetDPValue(address, "HUE")
		if !ok {
			t.Fatalf("HUE absent on CCU after move-to-hue")
		}
		if !valueNear(got, 180.0, 1.0) {
			t.Fatalf("CCU HUE = %v, want ~180", got)
		}
	})

	// SEND — MoveToSaturation(127) must land as SATURATION=0.5: Matter's
	// 127/254 is the HA-canonical 50 %, which ColorLight.SetColor scales
	// onto the wire's 0..1 SATURATION fraction (color.go).
	t.Run("send/move-to-saturation", func(t *testing.T) {
		if _, err := b.SharedCtl.Invoke(ctx, t, "colorcontrol", "move-to-saturation", ep,
			"127", "0", "1", "1"); err != nil {
			t.Fatalf("invoke move-to-saturation: %v", err)
		}
		got, ok := b.CCU.GetDPValue(address, "SATURATION")
		if !ok {
			t.Fatalf("SATURATION absent on CCU after move-to-saturation")
		}
		if !valueNear(got, 0.5, 0.01) {
			t.Fatalf("CCU SATURATION = %v, want ~0.5", got)
		}
		// The HmIP-LSC runs both axes at once: the echo of the hue /
		// saturation writes is the device in HS, so the server stays in
		// the HS mode although the colour-temperature axis carries a
		// value (RGBWLight.syncAxisTo).
		read, err := b.SharedCtl.ReadAttr(ctx, t, "colorcontrol", "color-mode", ep)
		if err != nil {
			t.Fatalf("read color-mode: %v", err)
		}
		if v, ok := harness.FindAttrUint(read, "ColorMode"); !ok || v != 0 {
			t.Fatalf("ColorMode after move-to-saturation = %v (ok=%v), want 0 (CurrentHueAndCurrentSaturation)\n%s", v, ok, read)
		}
	})

	// SEND — MoveToColorTemperature(250 mireds) must land as
	// COLOR_TEMPERATURE=4000 Kelvin (miredsToKelvin(250) ==
	// 1_000_000/250 == 4000 exactly), within HmIP-LSC's default
	// [2000,6500] Kelvin range so RGBWLight.SetKelvin does not clamp it.
	t.Run("send/move-to-color-temperature", func(t *testing.T) {
		if _, err := b.SharedCtl.Invoke(ctx, t, "colorcontrol", "move-to-color-temperature", ep,
			"250", "0", "1", "1"); err != nil {
			t.Fatalf("invoke move-to-color-temperature: %v", err)
		}
		got, ok := b.CCU.GetDPValue(address, "COLOR_TEMPERATURE")
		if !ok {
			t.Fatalf("COLOR_TEMPERATURE absent on CCU after move-to-color-temperature")
		}
		if !valueNear(got, 4000.0, 5.0) {
			t.Fatalf("CCU COLOR_TEMPERATURE = %v, want ~4000", got)
		}
	})

	// NEGATIVE — every ColorControl attribute is read-only (Matter
	// §3.2.6); the dispatcher's schema.AttributeWritable gate rejects
	// the write with UNSUPPORTED_WRITE (0x88) before it ever reaches the
	// server.
	t.Run("negative/write-color-temperature-mireds", func(t *testing.T) {
		// chip-tool's own exit status is not asserted here: a rejected
		// write may or may not turn into a non-zero process exit
		// depending on the chip-tool build, but Run() always returns the
		// captured merged stdout+stderr regardless — the IM status line
		// embedded in that output is the reliable signal.
		out, err := b.SharedCtl.WriteAttr(ctx, t, "colorcontrol", "color-temperature-mireds", "300", ep)
		if err != nil {
			t.Logf("write color-temperature-mireds returned an error (expected for a rejected write): %v", err)
		}
		status, ok := harness.WriteStatus(out)
		if !ok {
			t.Fatalf("no IM status parsed from rejected write:\n%s", out)
		}
		if status != "0x88" {
			t.Errorf("write color-temperature-mireds status = %s, want 0x88 (UNSUPPORTED_WRITE):\n%s", status, out)
		}
	})

	// RECEIVE — a colour-temperature push from the CCU (a wall transmitter,
	// a CCU program) reaches the ColorControl server through its Set and
	// is reported proactively: the daemon's confirmed-update hook stores
	// 3000 K as 333 mireds and the server's change notifier marks the
	// attribute dirty. Before the ADR 0084 ColorControl follow-up this
	// subtest pinned the opposite — no notifier fanned the colour data
	// points into the cluster — so a timeout here is the regression.
	t.Run("receive/color-temperature-proactive-report", func(t *testing.T) {
		out, err := harness.AwaitProactiveReport(ctx, t, b.SharedCtl,
			"colorcontrol", "color-temperature-mireds", ep,
			func() error { return b.CCU.FireDeviceEvent(address, "COLOR_TEMPERATURE", 3000) },
			func(out string) bool {
				v, ok := harness.FindAttrUint(out, "ColorTemperatureMireds")
				return ok && v == 333 // 1_000_000 / 3000
			},
			30*time.Second)
		if err != nil {
			t.Fatalf("no proactive ColorTemperatureMireds report after the CCU pushed 3000 K: %v\n%s", err, out)
		}
	})
}
