// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/model/calculated"
	"github.com/SukramJ/openccu-loom/internal/model/custom/climate"
	"github.com/SukramJ/openccu-loom/internal/model/custom/cover"
	"github.com/SukramJ/openccu-loom/internal/model/custom/light"
	"github.com/SukramJ/openccu-loom/internal/model/custom/lock"
	"github.com/SukramJ/openccu-loom/internal/model/custom/siren"
	switchdev "github.com/SukramJ/openccu-loom/internal/model/custom/switch"
	"github.com/SukramJ/openccu-loom/internal/model/custom/textdisplay"
	"github.com/SukramJ/openccu-loom/internal/model/custom/valve"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/internal/payload"
)

// TestSourceCompletenessAcrossModelLayers pins ADR 0007's contract:
// every domain object listed in the layer-by-layer scope MUST satisfy
// payload.Source. Adding a new model type without a payload.go costs
// a recompile here.
//
// The test does not exercise the methods — it only enforces the
// type-system constraint at build time via `var _ payload.Source = …`
// declarations. Empty-bodied because the work happens at compile.
//
// When this test fails, the offending type lacks one of:
// - Info() InfoPayload
// - Config() ConfigPayload
// - State() StatePayload
// - ServiceMethodNames() []string (provided by embedding payload.ServiceRegistry)
// - Invoke(ctx, name, params, priority) error (same)
func TestSourceCompletenessAcrossModelLayers(t *testing.T) {
	t.Parallel()

	// Channel — internal/model/device
	var _ payload.Source = (*device.Channel)(nil)

	// Generic data points — internal/model/generic
	var (
		_ payload.Source = (*generic.Switch)(nil)
		_ payload.Source = (*generic.BinarySensor)(nil)
		_ payload.Source = (*generic.Float)(nil)
		_ payload.Source = (*generic.Integer)(nil)
		_ payload.Source = (*generic.Select)(nil)
		_ payload.Source = (*generic.Button)(nil)
		_ payload.Source = (*generic.Text)(nil)
	)

	// Calculated sensors — internal/model/calculated
	var (
		_ payload.Source = (*calculated.DewPointSensor)(nil)
		_ payload.Source = (*calculated.DewPointSpreadSensor)(nil)
		_ payload.Source = (*calculated.FrostPointSensor)(nil)
		_ payload.Source = (*calculated.VaporConcentrationSensor)(nil)
		_ payload.Source = (*calculated.EnthalpySensor)(nil)
		_ payload.Source = (*calculated.ApparentTemperatureSensor)(nil)
		_ payload.Source = (*calculated.DerivedBinarySensor)(nil)
		_ payload.Source = (*calculated.OperatingVoltageLevelSensor)(nil)
	)

	// Custom data points — internal/model/custom/*
	var (
		_ payload.Source = (*climate.Climate)(nil)
		_ payload.Source = (*cover.Cover)(nil)
		_ payload.Source = (*cover.Blind)(nil)
		_ payload.Source = (*cover.Garage)(nil)
		_ payload.Source = (*lock.Lock)(nil)
		_ payload.Source = (*siren.Siren)(nil)
		_ payload.Source = (*siren.SmokeSiren)(nil)
		_ payload.Source = (*siren.SoundPlayer)(nil)
		_ payload.Source = (*valve.Irrigation)(nil)
		_ payload.Source = (*valve.Modulating)(nil)
		_ payload.Source = (*textdisplay.TextDisplay)(nil)
		_ payload.Source = (*switchdev.Switch)(nil)
	)

	// Light family — multiple wrapper types around the embedded
	// *generic.Float that share the Source contract by promotion.
	var (
		_ payload.Source = (*light.Light)(nil)
		_ payload.Source = (*light.ColorLight)(nil)
		_ payload.Source = (*light.ColorTempLight)(nil)
		_ payload.Source = (*light.FixedColorLight)(nil)
		_ payload.Source = (*light.EffectLight)(nil)
		_ payload.Source = (*light.DRGDaliLight)(nil)
	)

	// Hub data points — internal/model/hub
	var (
		_ payload.Source = (*hub.Program)(nil)
		_ payload.Source = (*hub.Sysvar)(nil)
		_ payload.Source = (*hub.Update)(nil)
		_ payload.Source = (*hub.AlarmMessages)(nil)
		_ payload.Source = (*hub.ServiceMessages)(nil)
		_ payload.Source = (*hub.InstallMode)(nil)
		_ payload.Source = (*hub.Connectivity)(nil)
		_ payload.Source = (*hub.Inbox)(nil)
		_ payload.Source = (*hub.Metrics)(nil)
		_ payload.Source = (*hub.Hub)(nil)
	)

	// Top-level services
	var (
		_ payload.Source = (*central.CentralUnit)(nil)
		_ payload.Source = (*client.InterfaceClient)(nil)
	)

	// The compile-time assertions above are the entire contract. A
	// non-nil sentinel marks the test as exercised so coverage tools
	// see the function body.
	if testing.Short() {
		t.Skip()
	}
}

// TestHADiscoveryPayloadBuilderCompleteness pins ADR 0010's contract:
// every Custom-DP type that surfaces as an HA-MQTT-Discovery entity
// MUST implement [payload.HADiscoveryPayloadBuilder] so the bridge
// can dispatch through the model layer rather than fall back to the
// (deleted) per-builder code in `discovery_aggregate.go`.
//
// Generic / calculated / hub / channel / top-level types are NOT
// expected to surface as HA-Discovery custom-domain entities —
// they go through the per-parameter `classifyComponent` path in
// `discovery.go`, which does not require this interface.
//
// When this test fails, the offending Custom-DP lacks an explicit
// `HADiscoveryPayload(ctx) (string, map[string]any)` method. Add one
// to the type's `payload.go` (see
// `internal/model/custom/climate/payload.go` for the pattern).
func TestHADiscoveryPayloadBuilderCompleteness(t *testing.T) {
	t.Parallel()

	var (
		_ payload.HADiscoveryPayloadBuilder = (*climate.Climate)(nil)
		_ payload.HADiscoveryPayloadBuilder = (*cover.Cover)(nil)
		_ payload.HADiscoveryPayloadBuilder = (*cover.Blind)(nil)
		_ payload.HADiscoveryPayloadBuilder = (*cover.Garage)(nil)
		_ payload.HADiscoveryPayloadBuilder = (*lock.Lock)(nil)
		_ payload.HADiscoveryPayloadBuilder = (*siren.Siren)(nil)
		_ payload.HADiscoveryPayloadBuilder = (*siren.SmokeSiren)(nil)
		_ payload.HADiscoveryPayloadBuilder = (*siren.SoundPlayer)(nil)
		_ payload.HADiscoveryPayloadBuilder = (*valve.Irrigation)(nil)
		_ payload.HADiscoveryPayloadBuilder = (*valve.Modulating)(nil)
		_ payload.HADiscoveryPayloadBuilder = (*textdisplay.TextDisplay)(nil)
		_ payload.HADiscoveryPayloadBuilder = (*light.Light)(nil)
		_ payload.HADiscoveryPayloadBuilder = (*light.ColorLight)(nil)
		_ payload.HADiscoveryPayloadBuilder = (*light.ColorTempLight)(nil)
		_ payload.HADiscoveryPayloadBuilder = (*light.FixedColorLight)(nil)
		_ payload.HADiscoveryPayloadBuilder = (*light.EffectLight)(nil)
		_ payload.HADiscoveryPayloadBuilder = (*light.DRGDaliLight)(nil)
	)

	if testing.Short() {
		t.Skip()
	}
}
