// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

func TestCCUValueSeederDecodesKeysAndISOStrings(t *testing.T) {
	t.Parallel()
	raw := map[string]json.RawMessage{
		"HmIP-RF.VCU1%3A1.STATE":        json.RawMessage(`true`),
		"HmIP-RF.VCU1%3A1.LEVEL":        json.RawMessage(`0.5`),
		"HmIP-RF.VCU1%3A0.IP_ADDRESS":   json.RawMessage(`"192%2E0%2E2%2E40"`),
		"HmIP-RF.VCU1%3A0.LABEL":        json.RawMessage(`"Sp%FCle"`),
		"HmIP-RF.VCU1%3A1":              json.RawMessage(`1`),       // two parts: dropped
		"HmIP-RF.VCU1%ZZ1.STATE":        json.RawMessage(`1`),       // bad escape: dropped
		"HmIP-RF.VCU1%3A1.NOT_JSON_VAL": json.RawMessage(`{broken`), // not JSON: dropped
		"HmIP-RF.VCU2%3A1.PRESS_SHORT":  json.RawMessage(`true`),    // kept; the pipeline filters edge triggers
		"HmIP-RF.VCU2%3A1.SUB.PARAM":    json.RawMessage(`"x%2Ey"`), // third part keeps its dots
	}
	got := decodeFetchAllDeviceData(raw)

	want := map[string]map[string]any{
		"VCU1:1": {"STATE": true, "LEVEL": 0.5},
		"VCU1:0": {"IP_ADDRESS": "192.0.2.40", "LABEL": "Spüle"},
		"VCU2:1": {"PRESS_SHORT": true, "SUB.PARAM": "x.y"},
	}
	if len(got) != len(want) {
		t.Fatalf("channels = %v, want %v", got, want)
	}
	for ch, params := range want {
		for k, v := range params {
			if got[ch][k] != v {
				t.Errorf("%s/%s = %#v, want %#v", ch, k, got[ch][k], v)
			}
		}
		if len(got[ch]) != len(params) {
			t.Errorf("%s carries %v, want exactly %v", ch, got[ch], params)
		}
	}
}

// fixedSeeder serves one interface's values from memory.
type fixedSeeder struct {
	values map[string]map[string]any
	depths []SeedDepth
}

func (s *fixedSeeder) SeedValues(_ context.Context, _ hmenum.Interface, depth SeedDepth) (map[string]map[string]any, error) {
	s.depths = append(s.depths, depth)
	return s.values, nil
}

func TestCCUHubSessionWithoutRunnerOffersNoSeederOrRestorer(t *testing.T) {
	t.Parallel()
	unit, err := central.New(central.Config{Name: "no-runner"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	cc := config.CentralConfig{Name: "no-runner", Host: "ccu.example"}
	s := newCCUHubSession(cc, unit, nil, HubData{}, nil, newCCUReadinessProbe(cc, nil), nil)
	if s.ValueSeeder() != nil {
		t.Error("ValueSeeder must be a nil interface without a runner, or the pipeline would call through it")
	}
	if s.Restorer() != nil {
		t.Error("Restorer must be nil without a JSON-RPC session")
	}
	if s.Transports().JSONCaller() != nil {
		t.Error("JSONCaller must be a nil interface without a runner")
	}
	if err := s.RefreshMetadata(context.Background()); err != nil {
		t.Errorf("RefreshMetadata without a runner = %v, want nil", err)
	}
	ep, err := s.Transports().Endpoint(hmenum.InterfaceHmIPRF)
	if err != nil {
		t.Fatalf("Endpoint: %v", err)
	}
	if ep.URL == "" || ep.HTTPClient != nil {
		t.Errorf("CCU endpoint = %+v, want the interface URL and the default HTTP client", ep)
	}
}

// TestReseedAppliesAnySeederAndFiltersEdgeTriggers pins that the apply side
// belongs to the pipeline, not to the CCU seeder: a seeder that is not the
// ReGa script feeds the model the same way, edge-trigger values are dropped
// whoever delivers them, and the depth reaches the seeder.
func TestReseedAppliesAnySeederAndFiltersEdgeTriggers(t *testing.T) {
	t.Parallel()
	f := buildBoost7Fixture(t)
	p := NewDevicePipeline(f.unit)
	ch := f.dev.Channel("DEV002:0")
	if ch == nil {
		t.Fatal("fixture channel DEV002:0 missing")
	}
	press := generic.NewDataPoint[bool](generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID: "HmIP-RF", ChannelAddress: "DEV002:0",
			ParamsetKey: hmenum.ParamsetKeyValues, Parameter: "PRESS_SHORT",
		},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeAction, Operations: hmenum.OperationsWrite | hmenum.OperationsEvent},
	})
	ch.Put(press)
	state := generic.NewDataPoint[bool](generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID: "HmIP-RF", ChannelAddress: "DEV002:0",
			ParamsetKey: hmenum.ParamsetKeyValues, Parameter: "STATE",
		},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeBool, Operations: hmenum.OperationsRead | hmenum.OperationsEvent},
	})
	ch.Put(state)

	seeder := &fixedSeeder{values: map[string]map[string]any{
		"DEV002:0": {"PRESS_SHORT": true, "STATE": true},
		"GONE:1":   {"STATE": true}, // not in the model: ignored
	}}
	if err := p.Reseed(context.Background(), "HmIP-RF", seeder, SeedCheap, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatalf("Reseed: %v", err)
	}
	if _, observed := press.Value(); observed {
		t.Error("PRESS_SHORT was seeded — edge triggers must be dropped by the pipeline for every seeder")
	}
	if v, observed := state.Value(); !observed || !v {
		t.Errorf("STATE = %v (observed %v), want true from the seeder", v, observed)
	}
	if len(seeder.depths) != 1 || seeder.depths[0] != SeedCheap {
		t.Errorf("seeder saw depths %v, want [SeedCheap]", seeder.depths)
	}
}

func TestFeatureTablesCoverEveryKey(t *testing.T) {
	t.Parallel()
	for _, model := range []string{"", "CCU", "OpenCCU"} {
		f := ccuFeatures(model)
		for _, k := range hmenum.AllFeatures() {
			s := f.State(k)
			if !s.Available && s.Reason == "" {
				t.Errorf("CCU(%q) table: %s unavailable without a reason", model, k)
			}
		}
	}
}

func TestCCUFeatureSetIsAllAvailableExceptTaxonomyTree(t *testing.T) {
	t.Parallel()
	f := ccuFeatures("OpenCCU")
	if f.SystemType() != hmenum.SystemTypeCCU {
		t.Errorf("SystemType = %q, want ccu", f.SystemType())
	}
	for _, k := range hmenum.AllFeatures() {
		want := k != hmenum.FeatureTaxonomyTree
		if got := f.Available(k); got != want {
			t.Errorf("%s available = %v, want %v", k, got, want)
		}
	}
	// Recovery mode keeps today's product rule.
	if ccuFeatures("CCU").Available(hmenum.FeatureSystemRecoveryMode) {
		t.Error("a stock CCU3 must not offer recovery mode")
	}
	if s := ccuFeatures("").State(hmenum.FeatureSystemRecoveryMode); s.Available || s.Reason != hmenum.FeatureReasonNotReady {
		t.Errorf("unknown product: recovery = %+v, want not offered (not_ready)", s)
	}
}
