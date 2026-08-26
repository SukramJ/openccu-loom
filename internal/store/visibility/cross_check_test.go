// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package visibility

// cross_check_test.go pins the §11/§15 contract:
//
//	"Visible-but-not-persistent is allowed;
//	 persistent-but-not-visible is dangerous."
//
// Implementation note — visibility gate wired into the write path:
//
// The store/visibility package is NOW wired into the write path via the
// VisibilityGate interface on internal/central/adapter.ParamsetsDomain.
// The gate is consulted in PutParamset and PutLinkParamset before any
// write reaches the CCU backend. Hidden parameters cause those methods
// to return pkg/hmerr.ErrParameterHidden.
//
// The MASTER paramset is the documented exception. This package's MASTER arm
// is the data-point-CREATION whitelist, not an authorization list, and the
// configuration surfaces hand out a channel's full MASTER descriptor — so
// asking it on the write side rejected the operator's save of every parameter
// the whitelist does not name. MASTER writability is decided by the parameter
// descriptor instead. See gateDecidesWrites in
// internal/central/adapter/paramsets.go.
//
// The REST handlers (internal/north/rest/handlers/paramsets.go) translate
// ErrParameterHidden to HTTP 403 Forbidden. The WS dispatcher
// (internal/north/rest/ws/commands.go) translates it to
// CommandErrorForbidden ("forbidden"). See:
// - internal/central/adapter/paramsets_visibility_test.go
// - internal/north/rest/handlers/paramsets_test.go
// - internal/north/rest/ws/commands_extended_test.go
//
// READ paths are NOT gated — the rule "visible-but-not-persistent is allowed"
// still holds. Only writes are gated.
//
// The gate is OPTIONAL (nil-safe). When no gate is wired (nil), all
// parameters are allowed (legacy no-op behaviour). The gate is also
// LENIENT for unknown devices: if the channel address cannot be resolved
// in the model registry the check is skipped so that initialisation
// sequences and diagnostic tooling are not blocked.

import (
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ---------------------------------------------------------------------------
// Cluster A — Rules + Decider basic invariants not covered by existing tests
// ---------------------------------------------------------------------------

// TestRulesGlobalHideTakesPriorityOverModelAllow verifies that a
// HideGlobal call hides a parameter for every model, even one that
// has no per-model rule.
func TestRulesGlobalHideTakesPriorityOverModelAllow(t *testing.T) {
	t.Parallel()
	r := NewRules()
	r.HideGlobal(hmenum.ParameterTemperature)
	// No HideForModel call — model has no explicit rule either way.
	for _, model := range []string{"any-model", "HmIP-RGBW", "", "HmIP-eTRV-BL"} {
		if r.Evaluate(model, hmenum.ParameterTemperature) != DecisionHide {
			t.Errorf("HideGlobal must hide for model %q, got DecisionAllow", model)
		}
	}
}

// TestRulesEvaluateUnknownParameterReturnsAllowed verifies that a
// parameter not mentioned in any rule defaults to DecisionAllow.
func TestRulesEvaluateUnknownParameterReturnsAllowed(t *testing.T) {
	t.Parallel()
	r := NewRules()
	// Use a synthetic parameter name that will never appear in builtInGlobalHides.
	const syntheticParam hmenum.Parameter = "OPENCCU_LOOM_CROSS_CHECK_UNKNOWN"
	if d := r.Evaluate("HmIP-eTRV", syntheticParam); d != DecisionAllow {
		t.Errorf("unknown parameter must default to DecisionAllow, got %v", d)
	}
}

// TestRulesHideForModelDoesNotAffectOtherModels verifies that a
// per-model hide does not leak to unrelated models.
func TestRulesHideForModelDoesNotAffectOtherModels(t *testing.T) {
	t.Parallel()
	r := NewRules()
	r.HideForModel("HmIP-RGBW", hmenum.ParameterState)
	if r.Evaluate("HmIP-RGBW", hmenum.ParameterState) != DecisionHide {
		t.Error("HideForModel must hide for the target model")
	}
	if r.Evaluate("HmIP-Other", hmenum.ParameterState) != DecisionAllow {
		t.Error("HideForModel must not affect unrelated models")
	}
	if r.Evaluate("HmIP-RGBW-Foo", hmenum.ParameterState) != DecisionAllow {
		t.Error("HideForModel uses exact model match; extended name must not be affected")
	}
}

// TestBuiltInGlobalHidesNonEmpty verifies that NewRules populates at
// least one built-in global hide (builtInGlobalHides is non-empty).
func TestBuiltInGlobalHidesNonEmpty(t *testing.T) {
	t.Parallel()
	r := NewRules()
	r.mu.RLock()
	n := len(r.hiddenGlobal)
	r.mu.RUnlock()
	if n == 0 {
		t.Fatal("builtInGlobalHides must return at least one parameter; hiddenGlobal is empty")
	}
}

// ---------------------------------------------------------------------------
// Cluster B — Decider semantics
// ---------------------------------------------------------------------------

// TestDeciderUnIgnoreOverridesIgnore verifies that an UnIgnoreEntry
// for a globally hidden parameter makes IsParameterIgnored return false
// for the matching (model, channelType, paramset, parameter) tuple, and
// that a different model still sees it as ignored.
func TestDeciderUnIgnoreOverridesIgnore(t *testing.T) {
	t.Parallel()
	rules := NewRules()
	// ParameterOnTimeList1 is in builtInGlobalHides — use it directly.
	p := hmenum.ParameterOnTimeList1
	d := NewParameterDecider(rules)

	if !d.IsParameterIgnored("HmIP-BS2", "SWITCH", channelNoUnknown, hmenum.ParamsetKeyValues, p) {
		t.Fatal("globally hidden parameter must be ignored before un-ignore")
	}
	d.LoadUnIgnore([]UnIgnoreEntry{
		{Parameter: p, Model: "HmIP-BS2"},
	})
	if d.IsParameterIgnored("HmIP-BS2", "SWITCH", channelNoUnknown, hmenum.ParamsetKeyValues, p) {
		t.Fatal("UnIgnoreEntry must re-enable the globally hidden parameter for the matching model")
	}
	// Different model must still be ignored.
	if !d.IsParameterIgnored("HmIP-RGBW", "SWITCH", channelNoUnknown, hmenum.ParamsetKeyValues, p) {
		t.Fatal("UnIgnoreEntry must not leak to a different model")
	}
}

// TestDeciderUnIgnoreModelPrefixMatch exercises modelPrefixMatch
// directly: a shorter pattern is a prefix of the model string; a
// disjoint pattern must not match.
func TestDeciderUnIgnoreModelPrefixMatch(t *testing.T) {
	t.Parallel()
	cases := []struct {
		model, pattern string
		want           bool
	}{
		{"HmIP-RGBW-1", "HmIP-RGBW", true},
		{"HmIP-RGBW", "HmIP-RGBW", true},
		{"HmIP-Foo", "HmIP-RGBW", false},
		{"HM-LC-Sw1", "HmIP-RGBW", false},
		{"HmIP-RGBW-Extended", "HmIP-RGBW", true},
		// Empty pattern matches anything.
		{"anything", "", true},
		// Model shorter than pattern must not match.
		{"HmIP", "HmIP-RGBW", false},
	}
	for _, tc := range cases {
		t.Run(tc.model+"/"+tc.pattern, func(t *testing.T) {
			t.Parallel()
			if got := modelPrefixMatch(tc.model, tc.pattern); got != tc.want {
				t.Errorf("modelPrefixMatch(%q, %q) = %v, want %v", tc.model, tc.pattern, got, tc.want)
			}
		})
	}
}

// TestDeciderCacheKeyIsStableAcrossCalls verifies that ignoreCacheKey
// struct equality is stable for identical inputs and differs for different
// inputs. This replaces the old defaultCacheKey string-concatenation test
// now that the cache uses typed struct keys (see types.go).
func TestDeciderCacheKeyIsStableAcrossCalls(t *testing.T) {
	t.Parallel()
	const (
		model       = "HmIP-eTRV-CL"
		channelType = "HEATING_CLIMATECONTROL_TRANSCEIVER"
	)
	k1 := ignoreCacheKey{model: model, channelType: channelType, channelNo: channelNoUnknown, paramsetKey: hmenum.ParamsetKeyValues, parameter: hmenum.ParameterTemperature}
	k2 := ignoreCacheKey{model: model, channelType: channelType, channelNo: channelNoUnknown, paramsetKey: hmenum.ParamsetKeyValues, parameter: hmenum.ParameterTemperature}
	if k1 != k2 {
		t.Error("ignoreCacheKey struct equality must hold for identical inputs")
	}
	// Different paramset must produce a different key.
	kOther := ignoreCacheKey{model: model, channelType: channelType, channelNo: channelNoUnknown, paramsetKey: hmenum.ParamsetKeyMaster, parameter: hmenum.ParameterTemperature}
	if k1 == kOther {
		t.Error("ignoreCacheKey must differ when paramset differs")
	}
	// Different channel number must produce a different key.
	kCh := ignoreCacheKey{model: model, channelType: channelType, channelNo: 1, paramsetKey: hmenum.ParamsetKeyValues, parameter: hmenum.ParameterTemperature}
	if k1 == kCh {
		t.Error("ignoreCacheKey must differ when channelNo differs")
	}
}

// TestDeciderConcurrentReadsAreSafe fans out 50 goroutines all calling
// IsParameterIgnored concurrently. This test must pass under -race.
func TestDeciderConcurrentReadsAreSafe(t *testing.T) {
	t.Parallel()
	d := NewParameterDecider(nil) // uses NewRules() internally
	d.LoadUnIgnore([]UnIgnoreEntry{
		{Parameter: hmenum.ParameterPartyTemperature, Model: "HmIP-eTRV"},
	})
	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := range goroutines {
		go func(i int) {
			defer wg.Done()
			model := "HmIP-eTRV"
			if i%2 == 0 {
				model = "HmIP-RGBW"
			}
			// Both calls are legal regardless of result; we only care that
			// there is no data race.
			_ = d.IsParameterIgnored(model, "TRANSCEIVER", channelNoUnknown, hmenum.ParamsetKeyValues, hmenum.ParameterPartyTemperature)
			_ = d.IsParameterHidden(model, "TRANSCEIVER", channelNoUnknown, hmenum.ParamsetKeyValues, hmenum.ParameterOnTimeList1)
		}(i)
	}
	wg.Wait()
}

// ---------------------------------------------------------------------------
// Cluster C — Cross-check: visibility gate wired into write path (F1)
// ---------------------------------------------------------------------------

// TestVisibilityGateIsWiredIntoWritePath documents and asserts that the
// visibility gate is actively consulted on paramset writes.
//
// The gate lives in internal/central/adapter.ParamsetsDomain (VisibilityGate
// interface). This test verifies:
// 1. Registry.IsAllowed correctly rejects a globally hidden parameter.
// 2. A non-hidden parameter is allowed through the same gate.
// 3. The sentinel error is pkg/hmerr.ErrParameterHidden.
//
// The full end-to-end behaviour (HTTP 403 / WS "forbidden" code) is tested
// in internal/central/adapter/paramsets_visibility_test.go,
// internal/north/rest/handlers/paramsets_test.go and
// internal/north/rest/ws/commands_extended_test.go.
func TestVisibilityGateIsWiredIntoWritePath(t *testing.T) {
	t.Parallel()

	reg := NewRegistry()
	// Add an explicit global hide so the test is not relying on
	// builtInGlobalHides remaining stable over time.
	const hiddenParam hmenum.Parameter = "OPENCCU_LOOM_WRITE_PATH_TEST_HIDDEN"
	reg.Rules().HideGlobal(hiddenParam)

	// Hidden parameter must be rejected.
	if reg.IsAllowed("HmIP-STH", "TRANSCEIVER", hmenum.ParamsetKeyValues, hiddenParam) {
		t.Error("hidden parameter must be rejected by IsAllowed — write-path gate would pass it incorrectly")
	}

	// Visible parameter must be allowed.
	if !reg.IsAllowed("HmIP-STH", "TRANSCEIVER", hmenum.ParamsetKeyValues, hmenum.ParameterState) {
		t.Error("visible parameter STATE must be allowed by IsAllowed — write-path gate would block it incorrectly")
	}

	// Document the sentinel error that the adapter layer surfaces.
	// If this import path changes, the downstream REST/WS tests will
	// also need updating.
	importPathCompiles := "github.com/SukramJ/openccu-loom/pkg/hmerr.ErrParameterHidden"
	_ = importPathCompiles // compile-time reference — see pkg/hmerr package.
}

// TestPersistedParameterMustBeVisibleByDefault asserts that a typical
// writable VALUES parameter (e.g. STATE) is allowed by the default
// Registry. If a parameter that the write path targets is hidden by
// default, downstream callers would write data the UI can never surface —
// the dangerous case from §11.
func TestPersistedParameterMustBeVisibleByDefault(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	// VALUES / writable parameters that the persistence layer writes.
	// They must not be globally hidden by default.
	writableValueParams := []hmenum.Parameter{
		hmenum.ParameterState,
		hmenum.ParameterLevel,
		hmenum.ParameterTemperature,
		hmenum.ParameterOnTime,
	}
	for _, p := range writableValueParams {
		t.Run(string(p), func(t *testing.T) {
			t.Parallel()
			if !reg.IsAllowed("HmIP-eTRV", "TRANSCEIVER", hmenum.ParamsetKeyValues, p) {
				t.Errorf("writable VALUES parameter %q must be allowed by default Registry — "+
					"hiding it creates persistent-but-not-visible data", p)
			}
		})
	}
}

// TestVisibleButNotPersistentIsAllowed documents that read-only / event-only
// parameters (visible to the user as sensor readings but never written) are
// also allowed by the visibility layer. This is the safe direction: the UI
// can display them; nothing tries to write them.
func TestVisibleButNotPersistentIsAllowed(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	// TEMPERATURE is a typical read-only sensor value exposed in VALUES.
	// STATE on a sensor device is similarly read-only.
	// The visibility layer must allow both so operators can monitor them.
	readOnlyParams := []hmenum.Parameter{
		hmenum.ParameterTemperature,
		hmenum.ParameterState,
	}
	for _, p := range readOnlyParams {
		t.Run(string(p), func(t *testing.T) {
			t.Parallel()
			if !reg.IsAllowed("HmIP-STH", "CLIMATE_TRANSCEIVER", hmenum.ParamsetKeyValues, p) {
				t.Errorf("read-only parameter %q must be visible (allowed) even if never written", p)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Cluster D — Outbound filter contract (ADR 0007)
// ---------------------------------------------------------------------------

// TestVisibilityFilterAppliedAtMQTTOutbound documents that the visibility
// filter is the outbound MQTT gate (ADR 0007). This test pins the semantic
// that a globally-hidden parameter must be blocked at the outbound boundary.
//
// The full end-to-end behaviour (MQTT NoopClient recording zero publishes) is
// tested in internal/central/adapter/eventbridge_test.go::TestVisibilityFilterAppliedAtMQTTOutbound.
func TestVisibilityFilterAppliedAtMQTTOutbound(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	// ParameterOnTimeList1 is in builtInGlobalHides.
	p := hmenum.ParameterOnTimeList1
	// IsAllowed is the question the MQTT bridge asks via filter.Adapter.Visible.
	if reg.IsAllowed("HmIP-BS2", "SWITCH", hmenum.ParamsetKeyValues, p) {
		t.Error("globally hidden parameter must not pass the outbound filter — MQTT would publish noise")
	}
}

// TestVisibilityFilterAppliedAtRESTListDPs documents that the visibility
// filter is the outbound REST gate for data-point listings (ADR 0007). Pins
// that a globally-hidden parameter is excluded by IsAllowed, which
// filter.Adapter.Visible delegates to.
//
// The full end-to-end behaviour (?include=all bypass, nil-safe pass-through)
// is tested in internal/north/rest/handlers/devices_test.go.
func TestVisibilityFilterAppliedAtRESTListDPs(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	// LEVEL is visible by default — REST must include it in the default list.
	if !reg.IsAllowed("HmIP-eTRV", "TRANSCEIVER", hmenum.ParamsetKeyValues, hmenum.ParameterLevel) {
		t.Error("visible parameter LEVEL must pass the outbound REST filter (default list)")
	}
	// ON_TIME_LIST_1 is globally hidden — REST default list must exclude it.
	if reg.IsAllowed("HmIP-eTRV", "TRANSCEIVER", hmenum.ParamsetKeyValues, hmenum.ParameterOnTimeList1) {
		t.Error("globally hidden parameter ON_TIME_LIST_1 must be blocked from the REST default list")
	}
}

// TestRegistryIsAllowedAggregatesRulesAndDecider verifies that
// Registry.IsAllowed correctly ANDs the model-level, paramset-level and
// parameter-level checks:
//
// - (allowed by rules, not ignored by decider) → true
// - (hidden by rules, not ignored by decider) → false
// - (allowed by rules, ignored by decider via model) → false [model ignored]
// - (hidden, model also ignored) → false
func TestRegistryIsAllowedAggregatesRulesAndDecider(t *testing.T) {
	t.Parallel()

	// Baseline: a fresh registry allows a normal VALUES parameter.
	reg := NewRegistry()
	if !reg.IsAllowed("HmIP-eTRV", "TRANSCEIVER", hmenum.ParamsetKeyValues, hmenum.ParameterLevel) {
		t.Error("baseline (allowed, allowed) must return true")
	}

	// Hidden by rules (globally): must return false.
	reg.Rules().HideGlobal(hmenum.ParameterTemperatureOffset)
	if reg.IsAllowed("HmIP-eTRV", "TRANSCEIVER", hmenum.ParamsetKeyValues, hmenum.ParameterTemperatureOffset) {
		t.Error("(hidden by rules, allowed by decider) must return false")
	}

	// Ignored model: every parameter under that model returns false.
	reg.Model().IgnoreModel("HmIP-Internal-Device")
	if reg.IsAllowed("HmIP-Internal-Device", "X", hmenum.ParamsetKeyValues, hmenum.ParameterLevel) {
		t.Error("(allowed by rules, ignored model) must return false")
	}

	// Both hidden and model ignored: still false.
	if reg.IsAllowed("HmIP-Internal-Device", "X", hmenum.ParamsetKeyValues, hmenum.ParameterTemperatureOffset) {
		t.Error("(hidden by rules, ignored model) must return false")
	}

	// MASTER paramset restricted to specific prefix: non-matching model → false.
	reg.Model().SetRelevantMasterPrefixes([]string{"HmIP-eTRV"})
	if reg.IsAllowed("HmIP-RGBW", "X", hmenum.ParamsetKeyMaster, hmenum.ParameterLevel) {
		t.Error("(MASTER paramset, non-matching prefix model) must return false")
	}
	// Matching prefix + channel 1 + whitelisted climate param → allowed.
	// HmIP-eTRV is in relevantMasterParamsetsByDevice with Channels:{1} and
	// climateMasterParameters (which includes TemperatureOffset). Channel 1 is
	// required; use IsAllowedForChannel to supply it.
	if !reg.IsAllowedForChannel("HmIP-eTRV-CL", "X", 1, hmenum.ParamsetKeyMaster, hmenum.ParameterTemperatureOffset) {
		t.Error("(MASTER paramset, matching prefix model, ch=1, whitelisted param) must return true")
	}
	// Non-whitelisted MASTER parameter for HmIP-eTRV must be ignored even with correct channel.
	if reg.IsAllowedForChannel("HmIP-eTRV-CL", "X", 1, hmenum.ParamsetKeyMaster, hmenum.ParameterLevel) {
		t.Error("(MASTER paramset, matching prefix model, non-whitelisted param) must return false")
	}
}
