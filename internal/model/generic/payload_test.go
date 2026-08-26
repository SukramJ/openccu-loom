// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package generic

import (
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ---------------------------------------------------------------------------
// payload.go — InfoPayload / ConfigPayload / StatePayload
// ---------------------------------------------------------------------------

func TestInfoPayload_Nil(t *testing.T) {
	t.Parallel()
	var dp *DataPoint[bool]
	if dp.Info() != nil {
		t.Error("nil DataPoint InfoPayload must return nil")
	}
}

func TestInfoPayload_WithCentralAndModel(t *testing.T) {
	t.Parallel()
	cfg := baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead)
	cfg.CentralName = "ccu-01"
	cfg.DeviceModel = "HmIP-STH"
	dp := NewDataPoint[bool](cfg)
	m, ok := dp.Info().(*payload.GenericDataPointInfo)
	if !ok || m == nil {
		t.Fatalf("InfoPayload must return *payload.GenericDataPointInfo, got %T", dp.Info())
	}
	if m.Central != "ccu-01" {
		t.Errorf("expected central=ccu-01, got %v", m.Central)
	}
	if m.DeviceModel != "HmIP-STH" {
		t.Errorf("expected device_model=HmIP-STH, got %v", m.DeviceModel)
	}
}

func TestConfigPayload_Nil(t *testing.T) {
	t.Parallel()
	var dp *DataPoint[float64]
	if dp.Config() != nil {
		t.Error("nil DataPoint ConfigPayload must return nil")
	}
}

func TestConfigPayload_WithMinMaxUnit(t *testing.T) {
	t.Parallel()
	cfg := baseCfg(hmenum.ParameterActualTemperature, hmenum.ParameterTypeFloat, hmenum.OperationsRead)
	cfg.Descriptor.Unit = "°C"
	cfg.Descriptor.Min = json.RawMessage(`-10`)
	cfg.Descriptor.Max = json.RawMessage(`60`)
	cfg.Descriptor.Default = json.RawMessage(`20`)
	cfg.Descriptor.Special = json.RawMessage(`[{"ID":"S","VALUE":0}]`)
	dp := NewDataPoint[float64](cfg)
	m, ok := dp.Config().(*payload.GenericDataPointConfig)
	if !ok || m == nil {
		t.Fatalf("ConfigPayload must return *payload.GenericDataPointConfig, got %T", dp.Config())
	}
	if m.Unit != "°C" {
		t.Errorf("unit: got %v", m.Unit)
	}
	if m.Min == "" {
		t.Error("min should be present")
	}
	if m.Max == "" {
		t.Error("max should be present")
	}
	if m.Default == "" {
		t.Error("default should be present")
	}
	if len(m.Special) == 0 {
		t.Error("special should be present")
	}
}

// TestConfigPayload_SpecialMarshalsAsJSONNotBase64 pins the encoding of the
// SPECIAL blob on the retained config topic: consumers of the raw plane read
// the declared sentinel set from it, and a []byte-typed field would render as
// a base64 string that nothing downstream decodes.
func TestConfigPayload_SpecialMarshalsAsJSONNotBase64(t *testing.T) {
	t.Parallel()
	cfg := baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead)
	cfg.Descriptor.Special = json.RawMessage(`{"NOT_USED":0.0}`)
	dp := NewDataPoint[float64](cfg)

	body, err := json.Marshal(dp.Config())
	if err != nil {
		t.Fatalf("marshal config payload: %v", err)
	}
	var decoded struct {
		Special map[string]float64 `json:"special"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("special is not embedded as JSON (%s): %v", body, err)
	}
	if got, ok := decoded.Special["NOT_USED"]; !ok || got != 0 {
		t.Fatalf("special = %v, want the declared sentinel set", decoded.Special)
	}
}

// TestConfigPayload_InvalidSpecialIsDropped covers the descriptor whose
// SPECIAL never parsed: normalization keeps such bytes verbatim, and embedding
// them would make the whole config payload unmarshalable rather than lose the
// one field.
func TestConfigPayload_InvalidSpecialIsDropped(t *testing.T) {
	t.Parallel()
	cfg := baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead)
	cfg.Descriptor.Special = json.RawMessage(`{not-json`)
	dp := NewDataPoint[float64](cfg)

	body, err := json.Marshal(dp.Config())
	if err != nil {
		t.Fatalf("marshal config payload: %v", err)
	}
	if !json.Valid(body) {
		t.Fatalf("config payload is not valid JSON: %s", body)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("unmarshal config payload: %v", err)
	}
	if _, present := decoded["special"]; present {
		t.Fatalf("an unparsable SPECIAL blob must be omitted, got %s", body)
	}
}

func TestConfigPayload_WithValueList(t *testing.T) {
	t.Parallel()
	cfg := baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeInteger, hmenum.OperationsRead)
	cfg.Descriptor.ValueList = []string{"A", "B", "C"}
	dp := NewDataPoint[int32](cfg)
	m, ok := dp.Config().(*payload.GenericDataPointConfig)
	if !ok || m == nil {
		t.Fatalf("ConfigPayload must return *payload.GenericDataPointConfig, got %T", dp.Config())
	}
	if len(m.ValueList) != 3 {
		t.Errorf("value_list: got %v", m.ValueList)
	}
}

func TestStatePayload_Nil(t *testing.T) {
	t.Parallel()
	var dp *DataPoint[bool]
	if dp.State() != nil {
		t.Error("nil DataPoint StatePayload must return nil")
	}
}

func TestStatePayload_Unobserved(t *testing.T) {
	t.Parallel()
	dp := NewDataPoint[bool](baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead))
	m, ok := dp.State().(*payload.GenericDataPointState)
	if !ok || m == nil {
		t.Fatalf("StatePayload must return *payload.GenericDataPointState, got %T", dp.State())
	}
	if m.Available {
		t.Errorf("unobserved: available should be false, got %v", m.Available)
	}
	if m.Value != nil {
		t.Error("unobserved: value must not be set")
	}
}

func TestStatePayload_Observed(t *testing.T) {
	t.Parallel()
	dp := NewDataPoint[bool](baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead))
	dp.OnEvent(true)
	m, ok := dp.State().(*payload.GenericDataPointState)
	if !ok || m == nil {
		t.Fatalf("StatePayload must return *payload.GenericDataPointState, got %T", dp.State())
	}
	if !m.Available {
		t.Errorf("observed: available should be true, got %v", m.Available)
	}
	if m.Value != true {
		t.Errorf("observed: value should be true, got %v", m.Value)
	}
}

func TestDataPointPayloadMethods(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	cfg := baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead|hmenum.OperationsWrite)
	cfg.Writer = w
	sw := NewSwitch(cfg)

	info, ok := sw.Info().(*payload.GenericDataPointInfo)
	if !ok || info == nil {
		t.Fatal("InfoPayload must return *payload.GenericDataPointInfo")
	}
	if info.Parameter != string(hmenum.ParameterState) {
		t.Fatalf("parameter = %v, want %s", info.Parameter, hmenum.ParameterState)
	}

	cfg2 := sw.Config()
	if cfg2 == nil {
		t.Fatal("ConfigPayload must not be nil")
	}

	state := sw.State()
	if state == nil {
		t.Fatal("StatePayload must not be nil")
	}
}

// ---------------------------------------------------------------------------
// payload.go — CanonicalUniqueID
// ---------------------------------------------------------------------------

func TestCanonicalUniqueIDCarriesForcedSensorSuffix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		forced bool
		want   string
	}{
		{
			name: "plain parameter keeps the bare routing key",
			want: "loom_a_1_level",
		},
		{
			// The HmIP-eTRV LEVEL surface is forced to a read-only sensor, and
			// the reference disambiguates it with a "_sensor" suffix on the
			// unique_id (model/data_point.py:1023). A consumer
			// keying its entity registry on this string spawns a duplicate
			// beside the migrated entity when the suffix goes missing.
			name:   "forced sensor is disambiguated",
			forced: true,
			want:   "loom_a_1_level_sensor",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dp := NewDataPoint[float64](baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead|hmenum.OperationsWrite))
			if tc.forced {
				dp.MarkForcedSensor()
			}
			if got := dp.CanonicalUniqueID(""); got != tc.want {
				t.Errorf("CanonicalUniqueID() = %q, want %q", got, tc.want)
			}
		})
	}
}
