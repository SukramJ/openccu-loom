// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build chiptool

package chiptool

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/tests/chiptool/harness"
)

// TestSendReceive_ColorControl exercises ColorControl (0x0300) against
// HmIP-LSC — the one godevccu fixture that runs hue/saturation AND
// colour-temperature simultaneously (rgbwColorServer's HS+CT
// projection), so a single pairing covers every ColorControl command
// combination the matrix calls for.
//
//   - SEND: MoveToHue / MoveToSaturation / MoveToColorTemperature must
//     each land on the CCU side (GetDPValue ground truth), matching the
//     production hueToMatter/saturationToMatter/kelvinToMireds encoding.
//   - NEGATIVE: every ColorControl attribute is read-only per Matter
//     §3.2.6 — a WriteAttr must be rejected before it ever reaches the
//     cluster server.
//   - RECEIVE (documented gap): a colour-temperature-only device push
//     has no dedicated notifier (RGBWLight only wires LEVEL changes
//     into OnMatterValueChanged; HUE/SATURATION/COLOR_TEMPERATURE ride
//     on nothing), so AwaitProactiveReport must time out — followed by
//     a plain ReadAttr proving the value is still served on demand.
func TestSendReceive_ColorControl(t *testing.T) {
	b := requireBridge(t)
	eps := discoverEndpointsWith(t, b, 0x0300, 1)
	if len(eps) == 0 {
		t.Skip("no ColorControl endpoint — godevccu fleet lacks an HmIP-LSC")
	}
	ep := eps[0]
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

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

	// SEND — MoveToSaturation(127) must land as SATURATION=50.0
	// (matterSaturationToHM(127) == 127/254*100 == 50.0 exactly).
	t.Run("send/move-to-saturation", func(t *testing.T) {
		if _, err := b.SharedCtl.Invoke(ctx, t, "colorcontrol", "move-to-saturation", ep,
			"127", "0", "1", "1"); err != nil {
			t.Fatalf("invoke move-to-saturation: %v", err)
		}
		got, ok := b.CCU.GetDPValue(address, "SATURATION")
		if !ok {
			t.Fatalf("SATURATION absent on CCU after move-to-saturation")
		}
		if !valueNear(got, 50.0, 1.0) {
			t.Fatalf("CCU SATURATION = %v, want ~50.0", got)
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
	// the write with UNSUPPORTED_WRITE (0x88) before it ever reaches
	// rgbwColorServer.MatterWrite (which would itself reject with
	// errMatterUnknownAttribute — the gate gets there first for every
	// attribute in this cluster's read-only set).
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

	// RECEIVE — KNOWN LOOM GAP (pin it, do not "fix" by loosening the
	// assertion): a colour-temperature-only device push has no dedicated
	// change-notifier. RGBWLight embeds *Light (-> *generic.Float) and
	// only wires LEVEL confirmations into OnMatterValueChanged; the
	// HUE/SATURATION/COLOR_TEMPERATURE wire DPs sit on the same channel
	// but are never fanned into a dirty-mark, so an isolated
	// COLOR_TEMPERATURE push produces no proactive ColorTemperatureMireds
	// report. AwaitProactiveReport must time out here — a green result
	// would mean the notifier gap silently closed and this test should
	// be promoted to a positive assertion (see notes/parity/by_design.md
	// for the model-layer notifier-gap catalogue).
	t.Run("receive/color-temperature-only-no-proactive-report", func(t *testing.T) {
		const gapTimeout = 12 * time.Second
		out, err := harness.AwaitProactiveReport(ctx, t, b.SharedCtl,
			"colorcontrol", "color-temperature-mireds", ep,
			func() error { return b.CCU.FireDeviceEvent(address, "COLOR_TEMPERATURE", 3000) },
			func(out string) bool {
				v, ok := harness.FindAttrUint(out, "ColorTemperatureMireds")
				return ok && v == 333 // miredsToKelvin inverse: 1_000_000/3000 == 333
			},
			gapTimeout)
		if err == nil {
			t.Fatalf("expected AwaitProactiveReport to time out (documented ColorControl CT-only notifier gap), but it observed a proactive report:\n%s", out)
		}
		t.Logf("confirmed documented gap: no proactive ColorTemperatureMireds report within %s: %v", gapTimeout, err)

		// The value is nonetheless served on demand — the gap is in the
		// change-notifier, not in the read path.
		readOut, rerr := b.SharedCtl.ReadAttr(ctx, t, "colorcontrol", "color-temperature-mireds", ep)
		if rerr != nil {
			t.Fatalf("read-on-demand color-temperature-mireds after gap: %v", rerr)
		}
		if !harness.AttrReadOK(readOut) {
			t.Errorf("read-on-demand did not report success:\n%s", readOut)
		}
		if v, ok := harness.FindAttrUint(readOut, "ColorTemperatureMireds"); !ok || v != 333 {
			t.Errorf("read-on-demand ColorTemperatureMireds = %v (ok=%v), want 333\n%s", v, ok, readOut)
		}
	})
}
