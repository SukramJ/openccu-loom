// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"log/slog"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/store/visibility"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// backendWithParams returns a paramsetFakeOps that lists one device
// (model=deviceModel, address=devAddr) with one channel (devAddr+":1",
// channelType), and returns the supplied paramset descriptions for VALUES
// and empty for MASTER.
func backendWithParams(devAddr, deviceModel, channelType string, params map[string]hmproto.ParameterData) *paramsetFakeOps {
	channelAddr := devAddr + ":1"
	return &paramsetFakeOps{
		listDevicesFn: func(_ context.Context) ([]hmproto.DeviceDescription, error) {
			return []hmproto.DeviceDescription{
				{Address: devAddr, Type: deviceModel},
				{Address: channelAddr, Parent: devAddr, Type: channelType},
			}, nil
		},
		getParamsetDescriptionFn: func(_ context.Context, _ string, key hmenum.ParamsetKey) (map[string]hmproto.ParameterData, error) {
			if key == hmenum.ParamsetKeyValues {
				return params, nil
			}
			return nil, nil
		},
		getParamsetFn: func(_ context.Context, _ string, _ hmenum.ParamsetKey) (map[string]any, error) {
			return map[string]any{}, nil
		},
	}
}

// TestHydrateChannelRespectsIgnoredParameter verifies that a parameter present
// in IGNORED_PARAMETERS (INHIBIT) is stored as a data point but force-marked
// to NoCreate when the visibility gate is wired and no required-parameter
// whitelist overrides it. Mirrors openccu-loom's "every parameter becomes a
// DP" architecture: the ignored marker controls UI / MQTT visibility, not
// the existence of the DP itself.
func TestHydrateChannelRespectsIgnoredParameter(t *testing.T) {
	t.Parallel()

	c, _ := central.New(central.Config{Name: "ccu-vis-01"})
	p := NewDevicePipeline(c)

	gate := visibility.NewRegistry()
	// No required-parameter whitelist → INHIBIT stays ignored.
	p.WithVisibility(gate)

	b := backendWithParams("AABBCC01", "HmIP-STH", "SOME_CHANNEL", map[string]hmproto.ParameterData{
		string(hmenum.ParameterInhibit): {
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	})

	if err := p.IngestFromBackend(
		context.Background(), "HmIP-RF", hmenum.InterfaceHmIPRF,
		b, &fakeWriter{}, nil, slog.Default(),
	); err != nil {
		t.Fatalf("IngestFromBackend: %v", err)
	}

	dev, ok := c.ModelRegistry.Get("AABBCC01")
	if !ok {
		t.Fatal("device not in registry")
	}
	ch := dev.Channel("AABBCC01:1")
	if ch == nil {
		t.Fatal("channel not found")
	}
	// INHIBIT is in IGNORED_PARAMETERS → DP is stored but force-marked
	// NoCreate so UI / MQTT layers skip it.
	dp := ch.Parameter(hmenum.ParameterInhibit)
	if dp == nil {
		t.Fatal("INHIBIT must be stored as a DP — every wire parameter becomes a DP under the new architecture")
	}
	assertForcedUsageIgnored(t, dp, "INHIBIT")
}

// assertForcedUsageIgnored fails the test when dp does not carry a
// forced_usage=Ignored mark. Helper for the visibility-mark
// post-create tests — the gate paths in operation_mode.go all
// produce Ignored after ADR 0015.
func assertForcedUsageIgnored(t *testing.T, dp device.ParameterDataPoint, label string) {
	t.Helper()
	f, ok := dp.(interface {
		ForcedUsage() (hmenum.DataPointUsage, bool)
	})
	if !ok {
		t.Fatalf("%s: DP does not expose ForcedUsage()", label)
	}
	u, set := f.ForcedUsage()
	if !set || u != hmenum.DataPointUsageIgnored {
		t.Errorf("%s: forced_usage=%v set=%v want Ignored", label, u, set)
	}
}

// TestHydrateChannelRequiredParameterOverridesIgnore verifies that INHIBIT
// (normally ignored) IS stored as a data point when it is in the required-
// parameter whitelist.
func TestHydrateChannelRequiredParameterOverridesIgnore(t *testing.T) {
	t.Parallel()

	c, _ := central.New(central.Config{Name: "ccu-vis-02"})
	p := NewDevicePipeline(c)

	gate := visibility.NewRegistry()
	// Add INHIBIT to the required-parameter whitelist.
	gate.SetRequiredParameters([]hmenum.Parameter{hmenum.ParameterInhibit})
	p.WithVisibility(gate)

	b := backendWithParams("CCDDEE02", "HmIP-STH", "SOME_CHANNEL", map[string]hmproto.ParameterData{
		string(hmenum.ParameterInhibit): {
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	})

	if err := p.IngestFromBackend(
		context.Background(), "HmIP-RF", hmenum.InterfaceHmIPRF,
		b, &fakeWriter{}, nil, slog.Default(),
	); err != nil {
		t.Fatalf("IngestFromBackend: %v", err)
	}

	dev, ok := c.ModelRegistry.Get("CCDDEE02")
	if !ok {
		t.Fatal("device not in registry")
	}
	ch := dev.Channel("CCDDEE02:1")
	if ch == nil {
		t.Fatal("channel not found")
	}
	// INHIBIT is in whitelist → the data point MUST be created.
	if dp := ch.Parameter(hmenum.ParameterInhibit); dp == nil {
		t.Error("INHIBIT with required-parameter whitelist override must be stored as a DP")
	}
}

// TestHydrateChannelNoGateAllowsEverything verifies that without a visibility
// gate all parameters pass through, including normally-ignored ones.
func TestHydrateChannelNoGateAllowsEverything(t *testing.T) {
	t.Parallel()

	c, _ := central.New(central.Config{Name: "ccu-vis-03"})
	p := NewDevicePipeline(c) // no WithVisibility call

	b := backendWithParams("EEFF0003", "HmIP-STH", "SOME_CHANNEL", map[string]hmproto.ParameterData{
		string(hmenum.ParameterInhibit): {
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	})

	if err := p.IngestFromBackend(
		context.Background(), "HmIP-RF", hmenum.InterfaceHmIPRF,
		b, &fakeWriter{}, nil, slog.Default(),
	); err != nil {
		t.Fatalf("IngestFromBackend: %v", err)
	}

	dev, ok := c.ModelRegistry.Get("EEFF0003")
	if !ok {
		t.Fatal("device not in registry")
	}
	ch := dev.Channel("EEFF0003:1")
	if ch == nil {
		t.Fatal("channel not found")
	}
	// Without gate, INHIBIT passes through.
	if dp := ch.Parameter(hmenum.ParameterInhibit); dp == nil {
		t.Error("without a visibility gate, INHIBIT must still be stored as a DP")
	}
}

// TestHydrateChannelMasterChannelGating verifies that a MASTER parameter
// whose channel number is not in the MASTER whitelist for the device is NOT
// stored.
//
// HmIP-STH has a device entry in relevantMasterParamsetsByDevice with channel
// set {1}. On channel 99 (not in the whitelist), TEMPERATURE_OFFSET must be
// gated out.
func TestHydrateChannelMasterChannelGating(t *testing.T) {
	t.Parallel()

	c, _ := central.New(central.Config{Name: "ccu-vis-04"})
	p := NewDevicePipeline(c)

	gate := visibility.NewRegistry()
	p.WithVisibility(gate)

	// HmIP-STH has Channels:{1} in relevantMasterParamsetsByDevice.
	// Channel 99 is outside that set → TEMPERATURE_OFFSET must be gated.
	b := &paramsetFakeOps{
		listDevicesFn: func(_ context.Context) ([]hmproto.DeviceDescription, error) {
			return []hmproto.DeviceDescription{
				{Address: "11220004", Type: "HmIP-STH"},
				// channel number 99 is outside the whitelist channel {1}
				{Address: "11220004:99", Parent: "11220004", Type: "HEATING_CLIMATECONTROL_TRANSCEIVER"},
			}, nil
		},
		getParamsetDescriptionFn: func(_ context.Context, _ string, key hmenum.ParamsetKey) (map[string]hmproto.ParameterData, error) {
			if key == hmenum.ParamsetKeyMaster {
				return map[string]hmproto.ParameterData{
					string(hmenum.ParameterTemperatureOffset): {
						Type:       hmenum.ParameterTypeFloat,
						Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
					},
				}, nil
			}
			return nil, nil
		},
		getParamsetFn: func(_ context.Context, _ string, _ hmenum.ParamsetKey) (map[string]any, error) {
			return map[string]any{}, nil
		},
	}

	if err := p.IngestFromBackend(
		context.Background(), "HmIP-RF", hmenum.InterfaceHmIPRF,
		b, &fakeWriter{}, nil, slog.Default(),
	); err != nil {
		t.Fatalf("IngestFromBackend: %v", err)
	}

	dev, ok := c.ModelRegistry.Get("11220004")
	if !ok {
		t.Fatal("device not in registry")
	}
	ch := dev.Channel("11220004:99")
	if ch == nil {
		t.Fatal("channel not found")
	}
	// TEMPERATURE_OFFSET on channel 99 for HmIP-STH → channel not whitelisted
	// → DP is stored (every parameter becomes a DP) but force-marked
	// NoCreate by the visibility-mark pass so consumers skip it.
	dp := ch.MasterParameter(hmenum.ParameterTemperatureOffset)
	if dp == nil {
		t.Fatal("TEMPERATURE_OFFSET on ch99 must be stored — every wire parameter becomes a DP")
	}
	assertForcedUsageIgnored(t, dp, "TEMPERATURE_OFFSET ch99")
}

// TestHydrateChannelWildcardIgnoredParameterGated verifies that a wildcard-
// matched parameter (PARTY_MODE_SUBMIT ends with _SUBMIT) is not stored
// when the gate is active.
func TestHydrateChannelWildcardIgnoredParameterGated(t *testing.T) {
	t.Parallel()

	c, _ := central.New(central.Config{Name: "ccu-vis-05"})
	p := NewDevicePipeline(c)

	gate := visibility.NewRegistry()
	// No whitelist — wildcard rule applies.
	p.WithVisibility(gate)

	b := backendWithParams("33440005", "HmIP-STH", "SOME_CHANNEL", map[string]hmproto.ParameterData{
		string(hmenum.ParameterPartyModeSubmit): {
			Type:       hmenum.ParameterTypeString,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
		},
	})

	if err := p.IngestFromBackend(
		context.Background(), "HmIP-RF", hmenum.InterfaceHmIPRF,
		b, &fakeWriter{}, nil, slog.Default(),
	); err != nil {
		t.Fatalf("IngestFromBackend: %v", err)
	}

	dev, ok := c.ModelRegistry.Get("33440005")
	if !ok {
		t.Fatal("device not in registry")
	}
	ch := dev.Channel("33440005:1")
	if ch == nil {
		t.Fatal("channel not found")
	}
	dp := ch.Parameter(hmenum.ParameterPartyModeSubmit)
	if dp == nil {
		t.Fatal("wildcard-ignored PARTY_MODE_SUBMIT must be stored — every wire parameter becomes a DP")
	}
	assertForcedUsageIgnored(t, dp, "PARTY_MODE_SUBMIT")
}

// TestClickEventMarksPreserveEventSuppression verifies the pass ordering
// between the event-suppression gate and the click-event marking: HmIP-PS
// click parameters are suppressed via IGNORE_DEVICES_FOR_DATA_POINT_EVENTS
// (forced Ignored), and applyClickEventMarks must NOT overwrite that mark
// with usage=event — in the reference stack those parameters never spawn an
// event, so no keypress group may surface for them.
func TestClickEventMarksPreserveEventSuppression(t *testing.T) {
	t.Parallel()

	pressShort := map[string]hmproto.ParameterData{
		string(hmenum.ParameterPressShort): {
			Type:       hmenum.ParameterTypeAction,
			Operations: hmenum.OperationsEvent,
		},
	}

	run := func(t *testing.T, model string) device.ParameterDataPoint {
		t.Helper()
		c, _ := central.New(central.Config{Name: "ccu-vis-click"})
		p := NewDevicePipeline(c)
		p.WithVisibility(visibility.NewRegistry())
		b := backendWithParams("AABBCC02", model, "KEY_TRANSCEIVER", pressShort)
		if err := p.IngestFromBackend(
			context.Background(), "HmIP-RF", hmenum.InterfaceHmIPRF,
			b, &fakeWriter{}, nil, slog.Default(),
		); err != nil {
			t.Fatalf("IngestFromBackend: %v", err)
		}
		dev, ok := c.ModelRegistry.Get("AABBCC02")
		if !ok {
			t.Fatal("device not in registry")
		}
		dp := dev.Channel("AABBCC02:1").Parameter(hmenum.ParameterPressShort)
		if dp == nil {
			t.Fatal("PRESS_SHORT DP missing")
		}
		return dp
	}

	t.Run("suppressed model keeps Ignored", func(t *testing.T) {
		t.Parallel()
		dp := run(t, "HmIP-PSM")
		assertForcedUsageIgnored(t, dp, "HmIP-PSM PRESS_SHORT")
	})

	t.Run("configurable channel without op-mode keeps event, no button", func(t *testing.T) {
		t.Parallel()
		// KEY_TRANSCEIVER is a configurable channel; with CHANNEL_OPERATION_MODE
		// unobserved the gated press parameters carry no button entity but still
		// fire keypress events — mirroring the reference stack, where the button
		// falls through to its no-create base usage while the click event keeps
		// its event base usage. usage=event withholds the button (event-gate)
		// yet stays in the keypress event group (event is not a suppressed
		// event-group usage).
		dp := run(t, "HmIP-WRC2")
		u, set := dp.(interface {
			ForcedUsage() (hmenum.DataPointUsage, bool)
		}).ForcedUsage()
		if !set || u != hmenum.DataPointUsageEvent {
			t.Errorf("HmIP-WRC2 PRESS_SHORT (no op-mode): forced_usage=%v set=%v want Event", u, set)
		}
	})

	t.Run("plain KEY channel gets a button for writable primary press", func(t *testing.T) {
		t.Parallel()
		// A non-configurable KEY channel is not op-mode gated: a WRITABLE press
		// (OPS=WRITE+EVENT, e.g. a HM-PB KEY channel's PRESS_SHORT) becomes a
		// button (usage=data_point), while a purely event-driven press
		// (OPS=EVENT, e.g. PRESS_CONT) stays an event source only.
		params := map[string]hmproto.ParameterData{
			string(hmenum.ParameterPressShort): {Type: hmenum.ParameterTypeAction, Operations: hmenum.OperationsWrite | hmenum.OperationsEvent},
			string(hmenum.ParameterPressCont):  {Type: hmenum.ParameterTypeAction, Operations: hmenum.OperationsEvent},
		}
		c, _ := central.New(central.Config{Name: "ccu-vis-click-key"})
		p := NewDevicePipeline(c)
		p.WithVisibility(visibility.NewRegistry())
		b := backendWithParams("AABBCC04", "HM-PB-2-WM55", "KEY", params)
		if err := p.IngestFromBackend(
			context.Background(), "HmIP-RF", hmenum.InterfaceHmIPRF,
			b, &fakeWriter{}, nil, slog.Default(),
		); err != nil {
			t.Fatalf("IngestFromBackend: %v", err)
		}
		dev, _ := c.ModelRegistry.Get("AABBCC04")
		short := dev.Channel("AABBCC04:1").Parameter(hmenum.ParameterPressShort)
		cont := dev.Channel("AABBCC04:1").Parameter(hmenum.ParameterPressCont)
		if short == nil || cont == nil {
			t.Fatal("press DP missing")
		}
		if u, set := short.(interface {
			ForcedUsage() (hmenum.DataPointUsage, bool)
		}).ForcedUsage(); !set || u != hmenum.DataPointUsageDataPoint {
			t.Errorf("PRESS_SHORT: forced_usage=%v set=%v want DataPoint (button)", u, set)
		}
		if u, set := cont.(interface {
			ForcedUsage() (hmenum.DataPointUsage, bool)
		}).ForcedUsage(); !set || u != hmenum.DataPointUsageEvent {
			t.Errorf("PRESS_CONT: forced_usage=%v set=%v want Event (no button)", u, set)
		}
	})
}
