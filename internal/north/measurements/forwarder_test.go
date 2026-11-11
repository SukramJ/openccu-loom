// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package measurements

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// openMainMu serialises sqlite.Open calls in this package's tests. The
// goose migration step inside Open writes package-level globals that are
// not concurrency-safe when multiple callers race.
var openMainMu sync.Mutex

// openMainDB opens a fresh, migrated, file-backed main database for one
// test — the database the recording overrides live in.
func openMainDB(t *testing.T) *sql.DB {
	t.Helper()
	openMainMu.Lock()
	db, err := sqlite.Open(context.Background(), sqlite.FileDSN(filepath.Join(t.TempDir(), "main.db")))
	openMainMu.Unlock()
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// fakeExporter records every sample the forwarder hands it.
type fakeExporter struct {
	mu        sync.Mutex
	samples   []Sample
	shutdowns int
}

func (e *fakeExporter) Export(s Sample) {
	e.mu.Lock()
	e.samples = append(e.samples, s)
	e.mu.Unlock()
}

func (e *fakeExporter) Shutdown(context.Context) error {
	e.mu.Lock()
	e.shutdowns++
	e.mu.Unlock()
	return nil
}

func (e *fakeExporter) got() []Sample {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]Sample(nil), e.samples...)
}

// centralWithDevice builds a ready central.Unit + central.Registry containing
// one device with one channel. Returns (registry, unit, device, channel).
func centralWithDevice(t *testing.T, centralName, devAddr, chanAddr string) (
	*central.Registry, *central.Unit, *device.Device, *device.Channel,
) {
	t.Helper()
	u, err := central.New(central.Config{Name: centralName})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(u); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	d := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     devAddr,
		Model:       "HmIP-STH",
		Name:        "TestDev",
	})
	ch := d.AddChannel(chanAddr, 1, "CLIMATE", hmenum.ParamsetKeyValues)
	u.ModelRegistry.Put(d)
	return reg, u, d, ch
}

// publishValueEvent fires a DataPointValueChangedEvent on unit u's bus.
func publishValueEvent(u *central.Unit, chanAddr, param string, psk hmenum.ParamsetKey, newVal hmtypes.ParamValue) {
	u.EventBus.Publish(hmevent.DataPointValueChangedEvent{
		Base: hmevent.NewBaseAt(time.Now()),
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: chanAddr,
			ParamsetKey:    psk,
			Parameter:      param,
		},
		NewValue: newVal,
	})
}

// ============================================================
// allow()
// ============================================================

func TestAllow_ExcludeWinsOverInclude(t *testing.T) {
	t.Parallel()
	if allow("POWER", []string{"POWER"}, []string{"POWER"}) {
		t.Error("allow should return false when parameter is in both include and exclude (exclude wins)")
	}
}

func TestAllow_EmptyIncludeAllowsEverything(t *testing.T) {
	t.Parallel()
	if !allow("ANYTHING", nil, nil) {
		t.Error("allow with empty include and exclude should return true")
	}
	// not in exclude → allowed
	if !allow("ANYTHING", nil, []string{"OTHER"}) {
		t.Error("allow should return true when param is not matched by any exclude pattern")
	}
	// matched by exclude → denied
	if allow("OTHER", nil, []string{"OTHER"}) {
		t.Error("allow should return false when param is matched by exclude pattern")
	}
}

func TestAllow_GlobPatterns(t *testing.T) {
	t.Parallel()
	cases := []struct {
		param   string
		include []string
		want    bool
	}{
		{"ENERGY_POWER", []string{"*POWER*"}, true},
		{"POWER_FACTOR", []string{"*POWER*"}, true},
		{"ACTUAL_HUMIDITY", []string{"ACTUAL_*"}, true},
		{"ACTUAL_TEMPERATURE", []string{"ACTUAL_*"}, true},
		{"TEMPERATURE", []string{"TEMPERATURE"}, true},
		// non-matching include → rejected
		{"HUMIDITY", []string{"TEMPERATURE"}, false},
		{"OTHER", []string{"ACTUAL_*"}, false},
	}
	for _, tc := range cases {
		got := allow(tc.param, tc.include, nil)
		if got != tc.want {
			t.Errorf("allow(%q, include=%v) = %v, want %v", tc.param, tc.include, got, tc.want)
		}
	}
}

// ============================================================
// numericValue()
// ============================================================

func TestNumericValue(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		v       hmtypes.ParamValue
		wantVal float64
		wantOK  bool
	}{
		{"int", hmtypes.IntValue(5), 5.0, true},
		{"float", hmtypes.FloatValue(1.5), 1.5, true},
		{"bool", hmtypes.BoolValue(true), 0, false},
		{"string", hmtypes.StringValue("x"), 0, false},
		{"none", hmtypes.ParamValue{}, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := numericValue(tc.v)
			if ok != tc.wantOK {
				t.Errorf("numericValue(%v) ok=%v, want %v", tc.v.Kind, ok, tc.wantOK)
			}
			if ok && got != tc.wantVal {
				t.Errorf("numericValue(%v) = %v, want %v", tc.v.Kind, got, tc.wantVal)
			}
		})
	}
}

// ============================================================
// Wire nil-safety
// ============================================================

func TestWireNilSafety(t *testing.T) {
	t.Parallel()
	// nil receiver
	var nilFwd *Forwarder
	stop := nilFwd.Wire(nil)
	if stop == nil {
		t.Fatal("Wire on nil *Forwarder returned nil closer")
	}
	stop() // must not panic

	// nil exporter
	f := New(nil, Options{})
	stop2 := f.Wire(central.NewRegistry())
	if stop2 == nil {
		t.Fatal("Wire with nil exporter returned nil closer")
	}
	stop2() // must not panic

	// WireCentral shares the guards and must tolerate the same nils.
	if unwire := nilFwd.WireCentral(nil); unwire != nil {
		t.Error("WireCentral on a nil *Forwarder returned a non-nil unwire")
	}
	_, u, _, _ := centralWithDevice(t, "ccu-01", "DEV000", "DEV000:1")
	if unwire := f.WireCentral(u); unwire != nil {
		t.Error("WireCentral with a nil exporter returned a non-nil unwire")
	}
}

// TestWireCloserShutsTheExporterDown pins that stopping the forwarder
// flushes the exporter's buffered tail, once, however often it is called.
func TestWireCloserShutsTheExporterDown(t *testing.T) {
	t.Parallel()
	exp := &fakeExporter{}
	stop := New(exp, Options{}).Wire(central.NewRegistry())
	stop()
	stop()
	if exp.shutdowns != 1 {
		t.Errorf("exporter Shutdown called %d times, want 1", exp.shutdowns)
	}
}

// TestWireCentralForwardsACentralThatAppearedAfterWire pins the entry point
// the live-adopt path uses: a walk of the registry at Wire time sees only
// the centrals present then, so a CCU adopted at runtime would forward
// nothing at all — which reads exactly like a CCU whose data points never
// change.
func TestWireCentralForwardsACentralThatAppearedAfterWire(t *testing.T) {
	t.Parallel()
	exp := &fakeExporter{}

	// Wire against an empty registry: the boot-time walk sees nothing.
	f := New(exp, Options{})
	stop := f.Wire(central.NewRegistry())
	t.Cleanup(stop)

	_, u, _, ch := centralWithDevice(t, "adopted", "DEV009", "DEV009:1")
	dp := floatDP("DEV009:1", "TEMPERATURE")
	ch.Put(dp)
	dp.OnEvent(19.5) // source → live

	// Without the per-central attach the event below reaches no subscriber.
	publishValueEvent(u, "DEV009:1", "TEMPERATURE", hmenum.ParamsetKeyValues, hmtypes.FloatValue(19.5))
	if got := exp.got(); len(got) != 0 {
		t.Fatalf("samples before WireCentral = %d, want 0 (the assertion below would be vacuous)", len(got))
	}

	unwire := f.WireCentral(u)
	if unwire == nil {
		t.Fatal("WireCentral returned a nil unwire for a forwardable central")
	}
	t.Cleanup(unwire)

	dp.OnEvent(20.5)
	publishValueEvent(u, "DEV009:1", "TEMPERATURE", hmenum.ParamsetKeyValues, hmtypes.FloatValue(20.5))

	got := exp.got()
	if len(got) != 1 {
		t.Fatalf("samples = %d, want 1 (the adopted central's value must be forwarded)", len(got))
	}
	if got[0].CentralName != "adopted" || got[0].Value != 20.5 {
		t.Errorf("sample = %+v, want central adopted, value 20.5", got[0])
	}
	if m := f.Metrics(); m.Forwarded != 1 {
		t.Errorf("Metrics.Forwarded = %d, want 1", m.Forwarded)
	}
}

// TestWireCentralSkipsADisabledCentral pins EnabledFor: a central the
// operator excluded subscribes nothing.
func TestWireCentralSkipsADisabledCentral(t *testing.T) {
	t.Parallel()
	exp := &fakeExporter{}
	f := New(exp, Options{EnabledFor: func(name string) bool { return name != "off" }})
	_, u, _, _ := centralWithDevice(t, "off", "DEV010", "DEV010:1")
	if unwire := f.WireCentral(u); unwire != nil {
		unwire()
		t.Error("WireCentral subscribed a central EnabledFor rejects")
	}
}

// ============================================================
// Provenance guard tests
// ============================================================

// floatDP creates a new float64 DataPoint and registers it on ch.
func floatDP(chanAddr, param string) *generic.DataPoint[float64] {
	return generic.NewDataPoint[float64](generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: chanAddr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      param,
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
}

// boolDP creates a new bool DataPoint for a channel address.
func boolDP(chanAddr, param string) *generic.DataPoint[bool] {
	return generic.NewDataPoint[bool](generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: chanAddr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      param,
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
}

// TestProvenanceGuard_LiveValueForwarded: DP source=live → one sample
// carrying the event's identity and value.
func TestProvenanceGuard_LiveValueForwarded(t *testing.T) {
	t.Parallel()
	exp := &fakeExporter{}
	reg, u, _, ch := centralWithDevice(t, "ccu-01", "DEV001", "DEV001:1")

	dp := floatDP("DEV001:1", "TEMPERATURE")
	ch.Put(dp)
	dp.OnEvent(21.5) // source → live

	stop := New(exp, Options{}).Wire(reg)
	publishValueEvent(u, "DEV001:1", "TEMPERATURE", hmenum.ParamsetKeyValues, hmtypes.FloatValue(21.5))
	stop()

	got := exp.got()
	if len(got) != 1 {
		t.Fatalf("samples = %d, want 1 (live value must be forwarded)", len(got))
	}
	want := Sample{
		CentralName: "ccu-01", InterfaceID: "HmIP-RF", ChannelAddress: "DEV001:1",
		Parameter: "TEMPERATURE", Value: 21.5,
	}
	g := got[0]
	if g.CentralName != want.CentralName || g.InterfaceID != want.InterfaceID ||
		g.ChannelAddress != want.ChannelAddress || g.Parameter != want.Parameter || g.Value != want.Value {
		t.Errorf("sample = %+v, want %+v", g, want)
	}
	if g.TS.IsZero() {
		t.Error("sample carries a zero timestamp, want the event's")
	}
}

// TestProvenanceGuard_LiveZeroForwarded: a genuine live 0.0 must be
// forwarded, not filtered out by any value-magnitude check.
func TestProvenanceGuard_LiveZeroForwarded(t *testing.T) {
	t.Parallel()
	exp := &fakeExporter{}
	reg, u, _, ch := centralWithDevice(t, "ccu-01", "DEV002", "DEV002:1")

	dp := floatDP("DEV002:1", "TEMPERATURE")
	ch.Put(dp)
	dp.OnEvent(0.0) // source → live with zero value

	stop := New(exp, Options{}).Wire(reg)
	publishValueEvent(u, "DEV002:1", "TEMPERATURE", hmenum.ParamsetKeyValues, hmtypes.FloatValue(0.0))
	stop()

	if got := exp.got(); len(got) != 1 {
		t.Errorf("samples = %d, want 1 (live 0.0 must be forwarded)", len(got))
	}
}

// TestProvenanceGuard_CacheValueSkipped: DP source=cache (boot replay) → nothing.
func TestProvenanceGuard_CacheValueSkipped(t *testing.T) {
	t.Parallel()
	exp := &fakeExporter{}
	reg, u, _, ch := centralWithDevice(t, "ccu-01", "DEV003", "DEV003:1")

	dp := floatDP("DEV003:1", "TEMPERATURE")
	ch.Put(dp)
	dp.RestoreCachedValue(21.5, time.Now(), time.Now()) // source → cache

	stop := New(exp, Options{}).Wire(reg)
	publishValueEvent(u, "DEV003:1", "TEMPERATURE", hmenum.ParamsetKeyValues, hmtypes.FloatValue(21.5))
	stop()

	if got := exp.got(); len(got) != 0 {
		t.Errorf("samples = %d, want 0 (cache-sourced value must not be forwarded)", len(got))
	}
}

// TestProvenanceGuard_UnobservedValueSkipped: DP never had a value
// (source=unobserved) → nothing.
func TestProvenanceGuard_UnobservedValueSkipped(t *testing.T) {
	t.Parallel()
	exp := &fakeExporter{}
	reg, u, _, ch := centralWithDevice(t, "ccu-01", "DEV004", "DEV004:1")

	dp := floatDP("DEV004:1", "TEMPERATURE")
	ch.Put(dp)
	// do not call OnEvent or RestoreCachedValue → source remains unobserved

	stop := New(exp, Options{}).Wire(reg)
	publishValueEvent(u, "DEV004:1", "TEMPERATURE", hmenum.ParamsetKeyValues, hmtypes.FloatValue(21.5))
	stop()

	if got := exp.got(); len(got) != 0 {
		t.Errorf("samples = %d, want 0 (unobserved DP must not be forwarded)", len(got))
	}
}

// TestProvenanceGuard_BoolLiveValueSkipped: bool DP is live but not numeric → nothing.
func TestProvenanceGuard_BoolLiveValueSkipped(t *testing.T) {
	t.Parallel()
	exp := &fakeExporter{}
	reg, u, _, ch := centralWithDevice(t, "ccu-01", "DEV005", "DEV005:1")

	dp := boolDP("DEV005:1", "STATE")
	ch.Put(dp)
	dp.OnEvent(true) // source → live

	stop := New(exp, Options{}).Wire(reg)
	publishValueEvent(u, "DEV005:1", "STATE", hmenum.ParamsetKeyValues, hmtypes.BoolValue(true))
	stop()

	if got := exp.got(); len(got) != 0 {
		t.Errorf("samples = %d, want 0 (bool value must not be forwarded)", len(got))
	}
}

// TestProvenanceGuard_MasterParamsetSkipped: only VALUES is forwarded.
func TestProvenanceGuard_MasterParamsetSkipped(t *testing.T) {
	t.Parallel()
	exp := &fakeExporter{}
	reg, u, _, ch := centralWithDevice(t, "ccu-01", "DEV006", "DEV006:1")

	dp := floatDP("DEV006:1", "TEMPERATURE")
	ch.Put(dp)
	dp.OnEvent(21.5) // source → live

	stop := New(exp, Options{}).Wire(reg)
	// publish with MASTER paramset — should be ignored
	publishValueEvent(u, "DEV006:1", "TEMPERATURE", hmenum.ParamsetKeyMaster, hmtypes.FloatValue(21.5))
	stop()

	if got := exp.got(); len(got) != 0 {
		t.Errorf("samples = %d, want 0 (MASTER paramset must not be forwarded)", len(got))
	}
}

// TestProvenanceGuard_DeviceNotInRegistry: DP address unknown → nothing.
func TestProvenanceGuard_DeviceNotInRegistry(t *testing.T) {
	t.Parallel()
	exp := &fakeExporter{}
	reg, u, _, _ := centralWithDevice(t, "ccu-01", "DEV007", "DEV007:1")

	stop := New(exp, Options{}).Wire(reg)
	// publish for an address that has no device in the registry
	publishValueEvent(u, "UNKNOWN:1", "TEMPERATURE", hmenum.ParamsetKeyValues, hmtypes.FloatValue(21.5))
	stop()

	if got := exp.got(); len(got) != 0 {
		t.Errorf("samples = %d, want 0 (unknown device must not be forwarded)", len(got))
	}
}

// TestForwarder_ExcludeGlobDropsLiveValue pins that the glob policy is
// applied on the hot path, not only by allow() in isolation.
func TestForwarder_ExcludeGlobDropsLiveValue(t *testing.T) {
	t.Parallel()
	exp := &fakeExporter{}
	reg, u, _, ch := centralWithDevice(t, "ccu-01", "DEV008", "DEV008:1")

	dp := floatDP("DEV008:1", "TEMPERATURE")
	ch.Put(dp)
	dp.OnEvent(21.5)

	stop := New(exp, Options{Exclude: []string{"TEMP*"}}).Wire(reg)
	publishValueEvent(u, "DEV008:1", "TEMPERATURE", hmenum.ParamsetKeyValues, hmtypes.FloatValue(21.5))
	stop()

	if got := exp.got(); len(got) != 0 {
		t.Errorf("samples = %d, want 0 (an excluded parameter must not be forwarded)", len(got))
	}
}
