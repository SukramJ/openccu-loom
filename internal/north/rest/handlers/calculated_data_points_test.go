// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/calculated"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// stubCalculatedDP is a minimal AttachableDataPoint that also exposes
// RawValue / ModifiedAt so toCalculatedDPSummary can extract state.
type stubCalculatedDP struct {
	key        hmtypes.DataPointKey
	category   hmenum.DataPointCategory
	rawValue   float64
	hasValue   bool
	modifiedAt time.Time
	// multiplier feeds the DisplayValue projection (toCalculatedDPSummary).
	// Every shipping calculated data point reports the trivial 1.0 (see
	// calcMultiplier in internal/model/calculated/data_point.go), so this
	// stub is the only way to exercise the non-trivial branch without
	// inventing a production multiplier that does not exist yet.
	multiplier float64
}

func (s *stubCalculatedDP) DataPointKey() hmtypes.DataPointKey { return s.key }
func (s *stubCalculatedDP) Category() hmenum.DataPointCategory { return s.category }
func (s *stubCalculatedDP) RawValue() (any, bool) {
	if !s.hasValue {
		return nil, false
	}
	return s.rawValue, true
}
func (s *stubCalculatedDP) ModifiedAt() time.Time { return s.modifiedAt }
func (s *stubCalculatedDP) Multiplier() float64   { return s.multiplier }

// addCalculatedDP attaches a stubCalculatedDP to channel channelNo of device d.
func addCalculatedDP(d *device.Device, addr, param string, no int, cat hmenum.DataPointCategory, value float64) {
	chAddr := addr + ":0"
	if no > 0 {
		chAddr = addr + ":" + string(rune('0'+no)) //nolint:gosec // G115: no is a small channel number (0..9) in test fixtures; '0'+no is 48..57
	}
	// Ensure channel exists.
	ch := d.Channel(chAddr)
	if ch == nil {
		ch = d.AddChannel(chAddr, no, "SENSOR", hmenum.ParamsetKeyValues)
	}
	dp := &stubCalculatedDP{
		key: hmtypes.DataPointKey{
			ChannelAddress: chAddr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      param,
		},
		category:   cat,
		rawValue:   value,
		hasValue:   true,
		modifiedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}
	ch.AttachCalculatedDataPoint(dp)
}

// --- tests: ListCalculatedDataPoints ---

func TestListCalculatedDataPoints_HappyPath(t *testing.T) {
	t.Parallel()
	d := newTestDevice("DEV0010", "HmIP-STE2")
	addCalculatedDP(d, "DEV0010", "DEW_POINT", 1, hmenum.DataPointCategorySensor, 12.5)
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"DEV0010": d}}

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV0010", "no": "1"}))
	w := httptest.NewRecorder()
	ListCalculatedDataPoints(idx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var out []CalculatedDPSummary
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 calculated DP, got %d", len(out))
	}
	if out[0].Name != "DEW_POINT" {
		t.Fatalf("expected name=DEW_POINT, got %q", out[0].Name)
	}
	if !out[0].Observed {
		t.Fatal("expected observed=true")
	}
}

// TestCalculatedDPSummary_NonTrivialMultiplierProjectsDisplayValue pins
// the calculated-DP side of the DisplayValue projection through the real
// toCalculatedDPSummary converter. No shipping calculated data point
// currently reports a non-trivial multiplier (calcMultiplier in
// internal/model/calculated/data_point.go always returns 1.0), but the
// projection has to fire correctly the moment one does — a stub
// multiplier lets this call site be tested now instead of staying dark
// until a real non-trivial calculated DP exists.
func TestCalculatedDPSummary_NonTrivialMultiplierProjectsDisplayValue(t *testing.T) {
	t.Parallel()
	d := newTestDevice("DEV0099", "HmIP-STE2")
	ch := d.AddChannel("DEV0099:1", 1, "SENSOR", hmenum.ParamsetKeyValues)
	dp := &stubCalculatedDP{
		key:        hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: "OPERATING_VOLTAGE_LEVEL"},
		rawValue:   0.5,
		hasValue:   true,
		multiplier: 100,
	}
	ch.AttachCalculatedDataPoint(dp)

	s := toCalculatedDPSummary(dp, ch, nil, "")
	if fv, ok := s.Value.(float64); !ok || fv != 0.5 {
		t.Fatalf("value = %#v, want the untouched raw 0.5", s.Value)
	}
	if fv, ok := s.DisplayValue.(float64); !ok || fv != 50 {
		t.Fatalf("display_value = %#v, want 50 (0.5 * multiplier 100)", s.DisplayValue)
	}
}

// TestCalculatedDPSummary_TrivialMultiplierOmitsDisplayValue pins the
// current, always-true state: every shipping calculated data point
// reports the identity multiplier, so display_value must stay absent —
// the same non-trivial-only gate the REST generic-DP summary applies.
func TestCalculatedDPSummary_TrivialMultiplierOmitsDisplayValue(t *testing.T) {
	t.Parallel()
	d := newTestDevice("DEV0098", "HmIP-STE2")
	addCalculatedDP(d, "DEV0098", "DEW_POINT", 1, hmenum.DataPointCategorySensor, 12.5)
	ch := d.Channel("DEV0098:1")
	dps := ch.CalculatedDataPoints()
	if len(dps) != 1 {
		t.Fatalf("expected 1 calculated DP, got %d", len(dps))
	}
	s := toCalculatedDPSummary(dps[0], ch, nil, "")
	if s.DisplayValue != nil {
		t.Fatalf("display_value = %#v, want absent for the trivial multiplier", s.DisplayValue)
	}
}

func TestListCalculatedDataPoints_DeviceNotFound_Returns404(t *testing.T) {
	t.Parallel()
	idx := &stubDeviceIndex{devices: map[string]*device.Device{}}
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "MISSING", "no": "1"}))
	w := httptest.NewRecorder()
	ListCalculatedDataPoints(idx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestListCalculatedDataPoints_ChannelNotFound_Returns404(t *testing.T) {
	t.Parallel()
	d := newTestDevice("DEV0011", "HmIP-STE2")
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"DEV0011": d}}

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV0011", "no": "99"}))
	w := httptest.NewRecorder()
	ListCalculatedDataPoints(idx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestListCalculatedDataPoints_EmptyChannel_ReturnsEmptyList(t *testing.T) {
	t.Parallel()
	d := newTestDevice("DEV0012", "HmIP-STE2")
	d.AddChannel("DEV0012:1", 1, "SENSOR", hmenum.ParamsetKeyValues)
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"DEV0012": d}}

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV0012", "no": "1"}))
	w := httptest.NewRecorder()
	ListCalculatedDataPoints(idx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var out []CalculatedDPSummary
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if len(out) != 0 {
		t.Fatalf("expected empty list, got %d items", len(out))
	}
}

// --- tests: GetCalculatedDataPoint ---

func TestGetCalculatedDataPoint_HappyPath(t *testing.T) {
	t.Parallel()
	d := newTestDevice("DEV0013", "HmIP-STE2")
	addCalculatedDP(d, "DEV0013", "FROST_POINT", 1, hmenum.DataPointCategorySensor, -2.3)
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"DEV0013": d}}

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV0013", "no": "1", "name": "FROST_POINT"}))
	w := httptest.NewRecorder()
	GetCalculatedDataPoint(idx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var out CalculatedDPDetail
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Name != "FROST_POINT" {
		t.Fatalf("expected name=FROST_POINT, got %q", out.Name)
	}
	if !out.Observed {
		t.Fatal("expected observed=true")
	}
}

func TestGetCalculatedDataPoint_NotFound_Returns404(t *testing.T) {
	t.Parallel()
	d := newTestDevice("DEV0014", "HmIP-STE2")
	d.AddChannel("DEV0014:1", 1, "SENSOR", hmenum.ParamsetKeyValues)
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"DEV0014": d}}

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV0014", "no": "1", "name": "MISSING"}))
	w := httptest.NewRecorder()
	GetCalculatedDataPoint(idx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestGetCalculatedDataPoint_DeviceNotFound_Returns404(t *testing.T) {
	t.Parallel()
	idx := &stubDeviceIndex{devices: map[string]*device.Device{}}

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "MISSING", "no": "1", "name": "DEW_POINT"}))
	w := httptest.NewRecorder()
	GetCalculatedDataPoint(idx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// TestGetCalculatedDataPoint_DependsOnPopulated exercises the handler
// end-to-end against a real DewPointSensor wired through Subscribe, so
// depends_on in the JSON response reflects the model's actual resolved
// source set rather than a stub.
func TestGetCalculatedDataPoint_DependsOnPopulated(t *testing.T) {
	t.Parallel()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "DEV0015"})
	ch := d.AddChannel("DEV0015:1", 1, "WEATHER_TRANSCEIVER", hmenum.ParamsetKeyValues)
	temp := generic.NewFloatSensor(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: string(hmenum.ParameterActualTemperature)},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead | hmenum.OperationsEvent},
	})
	hum := generic.NewFloatSensor(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: string(hmenum.ParameterHumidity)},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead | hmenum.OperationsEvent},
	})
	ch.Put(temp)
	ch.Put(hum)

	sensor := calculated.NewDewPointSensor()
	ch.AttachCalculatedDataPoint(sensor)

	idx := &stubDeviceIndex{devices: map[string]*device.Device{"DEV0015": d}}

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV0015", "no": "1", "name": "DEW_POINT"}))
	w := httptest.NewRecorder()
	GetCalculatedDataPoint(idx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var out CalculatedDPDetail
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	want := map[string]bool{
		string(hmenum.ParameterActualTemperature): true,
		string(hmenum.ParameterHumidity):          true,
	}
	if len(out.DependsOn) != len(want) {
		t.Fatalf("expected %d deps, got %d: %v", len(want), len(out.DependsOn), out.DependsOn)
	}
	for _, p := range out.DependsOn {
		if !want[p] {
			t.Errorf("unexpected dependency %q in %v", p, out.DependsOn)
		}
	}
}

func TestGetCalculatedDataPoint_InvalidChannelNo_Returns400(t *testing.T) {
	t.Parallel()
	d := newTestDevice("DEV0016", "HmIP-STE2")
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"DEV0016": d}}

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV0016", "no": "abc", "name": "DEW_POINT"}))
	w := httptest.NewRecorder()
	GetCalculatedDataPoint(idx, nil).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// --- dependsOn branches ---

// TestDependsOn_NoSourceParameterProvider_ReturnsNil pins the fallback for a
// calculated data point that does not expose the model's resolved source
// set (e.g. a stub used in unrelated tests): dependsOn must not guess.
func TestDependsOn_NoSourceParameterProvider_ReturnsNil(t *testing.T) {
	t.Parallel()
	dp := &stubCalculatedDP{key: hmtypes.DataPointKey{Parameter: "DEW_POINT"}}
	if got := dependsOn(dp); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

// TestDependsOn_ReadsModelResolvedSources pins depends_on to whatever the
// model's Subscribe wiring actually registered, not a name-based guess.
// ApparentTemperatureSensor's own doc says it depends on temperature +
// humidity + WIND_SPEED (internal/model/calculated/subscribe.go), so the
// dependency set here must include WIND_SPEED even though this handler's
// old dependsOnForKey mapping for that parameter never listed it.
func TestDependsOn_ReadsModelResolvedSources(t *testing.T) {
	t.Parallel()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "CALCDEP01"})
	ch := d.AddChannel("CALCDEP01:1", 1, "WEATHER_TRANSCEIVER", hmenum.ParamsetKeyValues)
	temp := generic.NewFloatSensor(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: string(hmenum.ParameterActualTemperature)},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead | hmenum.OperationsEvent},
	})
	hum := generic.NewFloatSensor(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: string(hmenum.ParameterHumidity)},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead | hmenum.OperationsEvent},
	})
	wind := generic.NewFloatSensor(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: string(hmenum.ParameterWindSpeed)},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead | hmenum.OperationsEvent},
	})
	ch.Put(temp)
	ch.Put(hum)
	ch.Put(wind)

	sensor := calculated.NewApparentTemperatureSensor()
	ch.AttachCalculatedDataPoint(sensor)

	got := dependsOn(sensor)
	want := map[string]bool{
		string(hmenum.ParameterActualTemperature): true,
		string(hmenum.ParameterHumidity):          true,
		string(hmenum.ParameterWindSpeed):         true,
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d deps, got %d: %v", len(want), len(got), got)
	}
	for _, p := range got {
		if !want[p] {
			t.Errorf("unexpected dependency %q in %v", p, got)
		}
	}
}

// --- tests: translated_name resolution ---

// TestCalculatedDPTranslatedName_Fallback pins the title-cased
// fallback: the OCCU catalogue carries no entries for the synthetic
// calculated parameters, so the entity name must match the reference
// stack's `parameter.title().replace("_", " ")` fallback.
func TestCalculatedDPTranslatedName_Fallback(t *testing.T) {
	t.Parallel()
	d := newTestDevice("DEV0020", "HmIP-STHD")
	addCalculatedDP(d, "DEV0020", "DEW_POINT", 1, hmenum.DataPointCategorySensor, 12.5)
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"DEV0020": d}}
	lab := translatorLabeler{entries: map[string]string{}}

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV0020", "no": "1"}))
	w := httptest.NewRecorder()
	ListCalculatedDataPoints(idx, lab).ServeHTTP(w, req)

	var out []CalculatedDPSummary
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out) != 1 || out[0].TranslatedName != "Dew Point" {
		t.Fatalf("TranslatedName = %+v, want Dew Point", out)
	}
}

// TestCalculatedDPTranslatedName_LocaleLabelWins pins that a
// channel-typed OCCU translation overrides the fallback — the same
// chain generic data points resolve through.
func TestCalculatedDPTranslatedName_LocaleLabelWins(t *testing.T) {
	t.Parallel()
	d := newTestDevice("DEV0021", "HmIP-STHD")
	addCalculatedDP(d, "DEV0021", "DEW_POINT", 1, hmenum.DataPointCategorySensor, 12.5)
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"DEV0021": d}}
	lab := translatorLabeler{entries: map[string]string{"SENSOR|DEW_POINT": "Taupunkt"}}

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV0021", "no": "1"}))
	w := httptest.NewRecorder()
	ListCalculatedDataPoints(idx, lab).ServeHTTP(w, req)

	var out []CalculatedDPSummary
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out) != 1 || out[0].TranslatedName != "Taupunkt" {
		t.Fatalf("TranslatedName = %+v, want Taupunkt", out)
	}
}

// TestCalculatedDPTranslatedName_CombinedDuration pins the combined-DP
// path: the synthetic DURATION parameter of a combined timer resolves
// to "Duration" — identical to the reference stack's fallback for
// combined data points.
func TestCalculatedDPTranslatedName_CombinedDuration(t *testing.T) {
	t.Parallel()
	d := newTestDevice("DEV0022", "HmIP-BSM")
	addCalculatedDP(d, "DEV0022", "DURATION", 4, hmenum.DataPointCategoryNumber, 30)
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"DEV0022": d}}

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "DEV0022", "no": "4", "name": "DURATION"}))
	w := httptest.NewRecorder()
	GetCalculatedDataPoint(idx, translatorLabeler{}).ServeHTTP(w, req)

	var out CalculatedDPDetail
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.TranslatedName != "Duration" {
		t.Fatalf("TranslatedName = %q, want Duration", out.TranslatedName)
	}
}

// TestToCalculatedDPSummary_UniqueID verifies that a non-empty serialSuffix
// stamps a loom_-prefixed unique_id on the calculated DP summary, and that an
// empty suffix yields an empty field.
func TestToCalculatedDPSummary_UniqueID(t *testing.T) {
	t.Parallel()
	dp := &stubCalculatedDP{
		key: hmtypes.DataPointKey{
			ChannelAddress: "DEV0030:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "DEW_POINT",
		},
		category: hmenum.DataPointCategorySensor,
		rawValue: 12.5,
		hasValue: true,
	}
	ch := &device.Channel{Type: "SENSOR"}

	t.Run("with serialSuffix produces loom_ prefix", func(t *testing.T) {
		t.Parallel()
		s := toCalculatedDPSummary(dp, ch, nil, "vccu0000000")
		if s.UniqueID == "" {
			t.Fatal("UniqueID must not be empty when serialSuffix is set")
		}
		if !strings.HasPrefix(s.UniqueID, "loom_") {
			t.Errorf("UniqueID = %q, want loom_ prefix", s.UniqueID)
		}
	})

	t.Run("empty serialSuffix yields empty UniqueID", func(t *testing.T) {
		t.Parallel()
		s := toCalculatedDPSummary(dp, ch, nil, "")
		if s.UniqueID != "" {
			t.Errorf("UniqueID = %q, want empty string when serialSuffix is empty", s.UniqueID)
		}
	})
}

// TestCalculatedDPSummaryAvailabilityFollowsSources pins the `available` flag
// on the REST record. `observed` stays true while a source is faulted, so a
// client that restores a previous state for unavailable entities has to read
// `available` — which folds in the validity of every source the value derives
// from.
func TestCalculatedDPSummaryAvailabilityFollowsSources(t *testing.T) {
	t.Parallel()

	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "CALCAV01"})
	ch := d.AddChannel("CALCAV01:1", 1, "WEATHER_TRANSCEIVER", hmenum.ParamsetKeyValues)
	temp := generic.NewFloatSensor(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: string(hmenum.ParameterActualTemperature)},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead | hmenum.OperationsEvent},
	})
	hum := generic.NewFloatSensor(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: string(hmenum.ParameterHumidity)},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead | hmenum.OperationsEvent},
	})
	ch.Put(temp)
	ch.Put(hum)

	sensor := calculated.NewDewPointSensor()
	ch.AttachCalculatedDataPoint(sensor)
	temp.OnEvent(20.0)
	hum.OnEvent(50.0)

	if s := toCalculatedDPSummary(sensor, ch, nil, "vccu0000000"); !s.Available {
		t.Fatal("expected available=true while both sources are healthy")
	}

	temp.UpdateStatus(hmenum.ParameterStatusOverflow)

	s := toCalculatedDPSummary(sensor, ch, nil, "vccu0000000")
	if s.Available {
		t.Fatal("expected available=false once the temperature source reports OVERFLOW")
	}
	if !s.Observed {
		t.Fatal("observed must stay true — it is why `available` has to be carried separately")
	}
}
