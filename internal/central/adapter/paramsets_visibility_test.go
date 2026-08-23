// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/store/visibility"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// noopParamsetBackend is a backend stub that records calls and
// succeeds by default.
type noopParamsetBackend struct {
	putCalled     bool
	putLinkCalled bool
}

func (b *noopParamsetBackend) GetParamset(_ context.Context, _ string, _ hmenum.ParamsetKey) (map[string]any, error) {
	return map[string]any{}, nil
}

func (b *noopParamsetBackend) PutParamset(_ context.Context, _ string, _ hmenum.ParamsetKey, _ map[string]any) error {
	b.putCalled = true
	return nil
}

func (b *noopParamsetBackend) GetLinkParamset(_ context.Context, _, _ string) (map[string]any, error) {
	return map[string]any{}, nil
}

func (b *noopParamsetBackend) ActivateLinkParamset(context.Context, string, string, bool) error {
	return nil
}

func (b *noopParamsetBackend) PutLinkParamset(_ context.Context, _, _ string, _ map[string]any) error {
	b.putLinkCalled = true
	return nil
}

// registryWithDeviceAndChannel builds a test registry containing one
// device with one channel of the given channelType.
func registryWithDeviceAndChannel(t *testing.T, model, channelAddr, channelType string) (*ParamsetsDomain, *noopParamsetBackend) {
	t.Helper()

	reg, dev := registryWithDevice(t)
	// Replace the existing device with one matching the requested model.
	dev2 := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     dev.Address,
		Model:       model,
		Name:        dev.Name(),
	})
	dev2.AddChannel(channelAddr, 1, channelType, hmenum.ParamsetKeyValues)
	// Overwrite device in the registry.
	for _, c := range reg.List() {
		c.ModelRegistry.Put(dev2)
		break
	}

	domain := NewParamsetsDomain(reg, nil)
	return domain, nil
}

// TestParamsetsDomainVisibilityGateBlocksHiddenParam asserts that
// SetVisibilityGate rejects a write containing a globally-hidden
// parameter with ErrParameterHidden.
//
// Note: HideGlobal applies to the VALUES paramset path via Rules.Evaluate.
// For MASTER paramsets, the channel-whitelist gating takes precedence and
// Rules.Evaluate is intentionally skipped (a parameter can be "hidden" in
// the UI but still be whitelisted for MASTER data-point creation). To
// exercise the gate on MASTER, we use a parameter that is NOT in the MASTER
// whitelist for the given channel. Here we test with VALUES, which is where
// HideGlobal has effect.
func TestParamsetsDomainVisibilityGateBlocksHiddenParam(t *testing.T) {
	t.Parallel()

	domain, _ := registryWithDeviceAndChannel(t, "HmIP-STH", "0001ABCD:1", "CLIMATE_TRANSCEIVER")

	// Wire a real visibility registry with one extra global hide.
	gate := visibility.NewRegistry()
	gate.Rules().HideGlobal(hmenum.ParameterTemperatureOffset)
	domain.SetVisibilityGate(gate)

	// Use ParamsetKeyValues — HideGlobal is enforced on the VALUES path.
	// (For MASTER, channel-whitelist gating takes precedence and the
	// Rules.Evaluate hidden-flag is skipped by design.)
	err := domain.checkVisibility("0001ABCD:1", hmenum.ParamsetKeyValues,
		map[string]any{"TEMPERATURE_OFFSET": 0.5})
	if err == nil {
		t.Fatal("expected ErrParameterHidden, got nil")
	}
	if !errors.Is(err, hmerr.ErrParameterHidden) {
		t.Fatalf("expected errors.Is(err, hmerr.ErrParameterHidden) to be true, got: %v", err)
	}
}

// TestParamsetsDomainVisibilityGateAllowsVisibleParam asserts that a
// non-hidden parameter passes the gate without error.
func TestParamsetsDomainVisibilityGateAllowsVisibleParam(t *testing.T) {
	t.Parallel()

	domain, _ := registryWithDeviceAndChannel(t, "HmIP-STH", "0001ABCD:1", "CLIMATE_TRANSCEIVER")

	gate := visibility.NewRegistry()
	domain.SetVisibilityGate(gate)

	err := domain.checkVisibility("0001ABCD:1", hmenum.ParamsetKeyValues,
		map[string]any{"TEMPERATURE": 21.5})
	if err != nil {
		t.Fatalf("expected nil error for visible parameter, got: %v", err)
	}
}

// TestParamsetsDomainNilGateIsNoOp asserts that when no gate is set
// (nil), the check is a no-op regardless of the parameter name.
func TestParamsetsDomainNilGateIsNoOp(t *testing.T) {
	t.Parallel()

	domain, _ := registryWithDeviceAndChannel(t, "HmIP-STH", "0001ABCD:1", "CLIMATE_TRANSCEIVER")
	// No gate wired — domain.gate == nil.

	// ParameterPartyModeSubmit is globally hidden by NewRules(). Without
	// a gate the check must be a no-op.
	err := domain.checkVisibility("0001ABCD:1", hmenum.ParamsetKeyValues,
		map[string]any{string(hmenum.ParameterPartyModeSubmit): "2025-01-01 12:00"})
	if err != nil {
		t.Fatalf("nil gate must not block anything; got: %v", err)
	}
}

// TestParamsetsDomainVisibilityGateBuiltInHides asserts that the
// builtInGlobalHides (e.g. PARTY_MODE_SUBMIT) are blocked by the
// default NewRegistry gate.
func TestParamsetsDomainVisibilityGateBuiltInHides(t *testing.T) {
	t.Parallel()

	// registryWithDevice creates a device at address "0001ABCD".
	// The channel address must be "0001ABCD:1" to match.
	domain, _ := registryWithDeviceAndChannel(t, "HmIP-eTRV", "0001ABCD:1", "HEATING_CLIMATECONTROL_TRANSCEIVER")

	gate := visibility.NewRegistry() // pre-wired with builtInGlobalHides
	domain.SetVisibilityGate(gate)

	// PARTY_MODE_SUBMIT is in builtInGlobalHides.
	err := domain.checkVisibility("0001ABCD:1", hmenum.ParamsetKeyValues,
		map[string]any{string(hmenum.ParameterPartyModeSubmit): "submit"})
	if err == nil {
		t.Fatal("builtInGlobalHide parameter must be blocked; got nil error")
	}
	if !errors.Is(err, hmerr.ErrParameterHidden) {
		t.Fatalf("expected ErrParameterHidden, got: %v", err)
	}
}

// TestParamsetsDomainCheckVisibilityUnknownDeviceIsLenient asserts
// that when the channel address cannot be resolved in the registry
// (e.g. diagnostic tooling, unknown device), the gate check is
// skipped rather than panic-ing or blocking.
func TestParamsetsDomainCheckVisibilityUnknownDeviceIsLenient(t *testing.T) {
	t.Parallel()

	domain := NewParamsetsDomain(nil, nil)
	gate := visibility.NewRegistry()
	gate.Rules().HideGlobal(hmenum.ParameterTemperatureOffset)
	domain.SetVisibilityGate(gate)

	// Unknown address — should not block.
	err := domain.checkVisibility("UNKNOWN:1", hmenum.ParamsetKeyMaster,
		map[string]any{"TEMPERATURE_OFFSET": 0.5})
	if err != nil {
		t.Fatalf("unknown device must not be blocked by gate; got: %v", err)
	}
}

// TestParamsetsDomainEmptyValuesSkipsGate asserts that the check
// short-circuits when the values map is empty (no parameters to check).
func TestParamsetsDomainEmptyValuesSkipsGate(t *testing.T) {
	t.Parallel()

	domain, _ := registryWithDeviceAndChannel(t, "HmIP-STH", "0001ABCD:1", "CLIMATE_TRANSCEIVER")
	gate := visibility.NewRegistry()
	gate.Rules().HideGlobal(hmenum.ParameterTemperatureOffset)
	domain.SetVisibilityGate(gate)

	err := domain.checkVisibility("0001ABCD:1", hmenum.ParamsetKeyMaster, map[string]any{})
	if err != nil {
		t.Fatalf("empty values must not trigger gate; got: %v", err)
	}
}

// TestParamsetsDomainVisibleValuesDropsHiddenParam pins the read-side
// half of the same gate. The configuration export uses it so the
// snapshot it hands out contains only parameters PutParamset accepts —
// otherwise the export produces a file its own import endpoint refuses
// wholesale on the first hidden name.
func TestParamsetsDomainVisibleValuesDropsHiddenParam(t *testing.T) {
	t.Parallel()

	domain, _ := registryWithDeviceAndChannel(t, "HmIP-STH", "0001ABCD:1", "CLIMATE_TRANSCEIVER")
	gate := visibility.NewRegistry()
	gate.Rules().HideGlobal(hmenum.ParameterTemperatureOffset)
	domain.SetVisibilityGate(gate)

	in := map[string]any{"TEMPERATURE_OFFSET": 0.5, "LEVEL": 1.0}
	out := domain.VisibleValues("0001ABCD:1", hmenum.ParamsetKeyValues, in)

	if _, still := out["TEMPERATURE_OFFSET"]; still {
		t.Error("a hidden parameter survived the filter")
	}
	if _, kept := out["LEVEL"]; !kept {
		t.Error("a visible parameter was dropped")
	}
	if err := domain.checkVisibility("0001ABCD:1", hmenum.ParamsetKeyValues, out); err != nil {
		t.Fatalf("the filtered set must pass the write gate, got %v", err)
	}
}

// TestParamsetsDomainMasterWriteIsNotGatedByTheCreationWhitelist pins the
// asymmetry the write gate has to respect.
//
// The gate's MASTER arm is the data-point-creation whitelist: it decides which
// of a channel's configuration parameters become north-bound entities, and it
// default-denies everything else. The configuration surfaces do not use it —
// paramset.get MASTER, the channel UI schema and the edit session all offer
// the channel's full MASTER descriptor. Asking the same list on the write side
// therefore rejected the operator's save of every parameter it does not name,
// with "parameter is hidden and may not be written", on a form whose every
// field it had just handed out.
//
// POWERUP_JUMPTARGET on an HmIP-BSM switch channel is one of those: writable
// per its descriptor, offered by every read surface, named by no whitelist.
// The VALUES control below keeps the gate itself wired — this is a MASTER-only
// exemption, not a disabled gate.
func TestParamsetsDomainMasterWriteIsNotGatedByTheCreationWhitelist(t *testing.T) {
	t.Parallel()

	domain, _ := registryWithDeviceAndChannel(t, "HmIP-BSM", "0001ABCD:4", "SWITCH_VIRTUAL_RECEIVER")
	domain.SetVisibilityGate(visibility.NewRegistry())

	offered := map[string]any{"POWERUP_JUMPTARGET": 1, "LOGIC_COMBINATION": 2}
	if err := domain.checkVisibility("0001ABCD:4", hmenum.ParamsetKeyMaster, offered); err != nil {
		t.Fatalf("MASTER write rejected: %v — the read surfaces offer these parameters, so the write "+
			"surface has to accept them", err)
	}
	if out := domain.VisibleValues("0001ABCD:4", hmenum.ParamsetKeyMaster, offered); len(out) != len(offered) {
		t.Errorf("VisibleValues dropped %d of %d MASTER parameters — the read side must offer exactly "+
			"what the write side accepts", len(offered)-len(out), len(offered))
	}

	// Control: the VALUES arm of the same gate still refuses a hidden parameter.
	gate := visibility.NewRegistry()
	gate.Rules().HideGlobal(hmenum.ParameterTemperatureOffset)
	domain.SetVisibilityGate(gate)
	err := domain.checkVisibility("0001ABCD:4", hmenum.ParamsetKeyValues,
		map[string]any{"TEMPERATURE_OFFSET": 0.5})
	if !errors.Is(err, hmerr.ErrParameterHidden) {
		t.Fatalf("VALUES write of a hidden parameter: got %v, want ErrParameterHidden", err)
	}
}

// TestParamsetsDomainVisibleValuesWithoutGate keeps the ungated daemon
// unaffected: nothing is filtered when no gate is wired.
func TestParamsetsDomainVisibleValuesWithoutGate(t *testing.T) {
	t.Parallel()

	domain, _ := registryWithDeviceAndChannel(t, "HmIP-STH", "0001ABCD:1", "CLIMATE_TRANSCEIVER")
	in := map[string]any{"TEMPERATURE_OFFSET": 0.5}
	if out := domain.VisibleValues("0001ABCD:1", hmenum.ParamsetKeyValues, in); len(out) != 1 {
		t.Fatalf("out = %v, want the input unchanged", out)
	}
}
