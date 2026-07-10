// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build chiptool

package chiptool

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/tests/chiptool/harness"
)

// TestReceive_MeasurementAnalog is the RECEIVE-direction slice for the
// analog measurement clusters that map onto a single float-valued CCU
// parameter: TemperatureMeasurement (0x0402), RelativeHumidityMeasurement
// (0x0405), IlluminanceMeasurement (0x0400), and the CO2 leg of
// CarbonDioxideConcentrationMeasurement (0x040D). Every one of these
// clusters is read-only on the Matter side — SEND is a NEGATIVE case
// covered by send_stub_reject_test.go, not repeated here.
//
// HmIP-STHO (standalone temperature/humidity sensor), HmIP-SMI
// (motion + illuminance), and HmIP-SCTH230 (CO2 + temperature/humidity)
// are the representative godevccu fixtures; each covers one or two of
// the rows below.
func TestReceive_MeasurementAnalog(t *testing.T) {
	b := requireBridge(t)
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	t.Run("temperature", func(t *testing.T) {
		eps := discoverEndpointsWith(t, b, 0x0402, 1)
		if len(eps) == 0 {
			t.Skip("no TemperatureMeasurement endpoint — godevccu fleet lacks HmIP-STHO")
		}
		ep := eps[0]
		address, _, ok := b.ResolveCCUAddress(ctx, t, ep, 0x0402)
		if !ok {
			t.Fatalf("could not resolve CCU address for temperaturemeasurement endpoint %d", ep)
		}

		// RECEIVE — ACTUAL_TEMPERATURE (degrees C) must reach the
		// controller as MeasuredValue (int16, hundredths of a degree C).
		out, err := harness.AwaitProactiveReport(ctx, t, b.SharedCtl,
			"temperaturemeasurement", "measured-value", ep,
			func() error { return b.CCU.FireDeviceEvent(address, "ACTUAL_TEMPERATURE", 22.0) },
			func(out string) bool {
				v, ok := harness.FindAttrInt(out, "MeasuredValue")
				return ok && v == 2200
			},
			30*time.Second)
		if err != nil {
			t.Fatalf("await proactive MeasuredValue=2200: %v\n%s", err, out)
		}
	})

	t.Run("relative-humidity", func(t *testing.T) {
		eps := discoverEndpointsWith(t, b, 0x0405, 1)
		if len(eps) == 0 {
			t.Skip("no RelativeHumidityMeasurement endpoint — godevccu fleet lacks HmIP-STHO")
		}
		ep := eps[0]
		address, _, ok := b.ResolveCCUAddress(ctx, t, ep, 0x0405)
		if !ok {
			t.Fatalf("could not resolve CCU address for relativehumiditymeasurement endpoint %d", ep)
		}

		// RECEIVE — HUMIDITY (percent, 0-100) must reach the controller as
		// MeasuredValue (uint16, hundredths of a percent).
		out, err := harness.AwaitProactiveReport(ctx, t, b.SharedCtl,
			"relativehumiditymeasurement", "measured-value", ep,
			func() error { return b.CCU.FireDeviceEvent(address, "HUMIDITY", 55) },
			func(out string) bool {
				v, ok := harness.FindAttrUint(out, "MeasuredValue")
				return ok && v == 5500
			},
			30*time.Second)
		if err != nil {
			t.Fatalf("await proactive MeasuredValue=5500: %v\n%s", err, out)
		}
	})

	t.Run("illuminance", func(t *testing.T) {
		eps := discoverEndpointsWith(t, b, 0x0400, 1)
		if len(eps) == 0 {
			t.Skip("no IlluminanceMeasurement endpoint — godevccu fleet lacks HmIP-SMI")
		}
		ep := eps[0]
		address, dpKey, ok := b.ResolveCCUAddress(ctx, t, ep, 0x0400)
		if !ok {
			t.Fatalf("could not resolve CCU address for illuminancemeasurement endpoint %d", ep)
		}

		// RECEIVE — the endpoint's lux source (ILLUMINATION or
		// CURRENT_ILLUMINATION, whichever the device exposes — fire the
		// dp_key the resolver reports, not a hard-coded name the endpoint
		// may not be backed by) must reach the controller as MeasuredValue
		// using Matter's log-encoded lux representation
		// (10000 * log10(lux) + 1, rounded). Computed here rather than
		// pinned as a literal so the row documents the encoding it
		// exercises.
		want := analogMeasurementLuxToMatter(300)
		out, err := harness.AwaitProactiveReport(ctx, t, b.SharedCtl,
			"illuminancemeasurement", "measured-value", ep,
			func() error { return b.CCU.FireDeviceEvent(address, dpKey, 300) },
			func(out string) bool {
				v, ok := harness.FindAttrUint(out, "MeasuredValue")
				return ok && v == want
			},
			30*time.Second)
		if err != nil {
			t.Fatalf("await proactive MeasuredValue=%d: %v\n%s", want, err, out)
		}
	})

	t.Run("co2-concentration", func(t *testing.T) {
		eps := discoverEndpointsWith(t, b, 0x040D, 1)
		if len(eps) == 0 {
			t.Skip("no CarbonDioxideConcentrationMeasurement endpoint — godevccu fleet lacks HmIP-SCTH230")
		}
		ep := eps[0]
		address, _, ok := b.ResolveCCUAddress(ctx, t, ep, 0x040D)
		if !ok {
			t.Fatalf("could not resolve CCU address for carbondioxideconcentrationmeasurement endpoint %d", ep)
		}

		// RECEIVE — CONCENTRATION (ppm) must reach the controller as
		// MeasuredValue, a float passthrough with no unit conversion.
		out, err := harness.AwaitProactiveReport(ctx, t, b.SharedCtl,
			"carbondioxideconcentrationmeasurement", "measured-value", ep,
			func() error { return b.CCU.FireDeviceEvent(address, "CONCENTRATION", 800) },
			func(out string) bool {
				v, ok := harness.FindAttrUint(out, "MeasuredValue")
				return ok && v == 800
			},
			30*time.Second)
		if err != nil {
			t.Fatalf("await proactive MeasuredValue=800: %v\n%s", err, out)
		}
	})

	// PressureMeasurement (0x0403) and PM2.5/PM10 (0x042A / 0x042D) have
	// no godevccu fixture exposing AIR_PRESSURE or
	// MASS_CONCENTRATION_PM_{2_5,10}_24H — see
	// docs/parity/by_design.md / matrix gap G8. Deferred until a fixture
	// lands rather than silently omitted.
	t.Run("pressure", func(t *testing.T) {
		t.Skip("no godevccu fixture exposes AIR_PRESSURE — deferred pending PressureMeasurement fixture (matrix gap G8)")
	})
	t.Run("pm2.5-pm10", func(t *testing.T) {
		t.Skip("no godevccu fixture exposes MASS_CONCENTRATION_PM_2_5_24H/PM_10_24H — deferred pending PM2.5/PM10 fixture (matrix gap G8)")
	})
}

// analogMeasurementLuxToMatter reproduces the bridge's lux-to-Matter
// log encoding (MeasuredValue = round(10000 * log10(lux) + 1)) so the
// illuminance RECEIVE cell can assert the exact wire value instead of
// merely presence.
func analogMeasurementLuxToMatter(lux float64) int64 {
	if lux < 1 {
		lux = 1
	}
	return int64(math.Round(10000*math.Log10(lux) + 1))
}
