// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package central

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/registry"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/store/session"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ---------------------------------------------------------------------------
// Stub helpers
// ---------------------------------------------------------------------------

// stubPersistStore satisfies session.PersistStore in-memory.
type stubPersistStore struct {
	mu   sync.Mutex
	rows map[string][]session.LoadRow
}

func newStubPersistStore() *stubPersistStore {
	return &stubPersistStore{rows: make(map[string][]session.LoadRow)}
}

func (s *stubPersistStore) PersistAll(_ context.Context, centralName, slug string, rows []session.PersistRow) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := centralName + "/" + slug
	out := make([]session.LoadRow, 0, len(rows))
	for i := range rows {
		out = append(out, session.LoadRow{
			Method:     rows[i].Method,
			RecordedAt: rows[i].RecordedAt,
		})
	}
	s.rows[k] = out
	return nil
}

func (s *stubPersistStore) Load(_ context.Context, centralName, slug string, _ int) ([]session.LoadRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rows[centralName+"/"+slug], nil
}

// minimalDP is a bare-minimum ParameterDataPoint used for
// GetParameters / GetUnIgnoreCandidates / ReadableGenericDataPoints.
//
// forcedUsage emulates BaseDataPointFields.ForcedUsage(): nil means
// "no override → DP is visible by default".
type minimalDP struct {
	key         hmtypes.DataPointKey
	data        hmproto.ParameterData
	forcedUsage *hmenum.DataPointUsage
	operations  hmenum.Operations
}

func (d *minimalDP) DataPointKey() hmtypes.DataPointKey { return d.key }
func (d *minimalDP) Parameter() hmenum.Parameter        { return hmenum.Parameter(d.key.Parameter) }
func (d *minimalDP) ParameterData() hmproto.ParameterData {
	pd := d.data
	if d.operations != 0 {
		pd.Operations = d.operations
	}
	return pd
}
func (d *minimalDP) RawValue() (any, bool)             { return nil, false }
func (d *minimalDP) ModifiedAt() time.Time             { return time.Time{} }
func (d *minimalDP) OnAnyUpdate(func(any, any)) func() { return func() {} }
func (d *minimalDP) ForcedUsage() (hmenum.DataPointUsage, bool) {
	if d.forcedUsage == nil {
		return "", false
	}
	return *d.forcedUsage, true
}

// usagePtr is a small helper to take the address of a usage literal.
func usagePtr(u hmenum.DataPointUsage) *hmenum.DataPointUsage { return &u }

// pathDP is like minimalDP but also implements statePather so that
// GetStatePaths / GetStatePathEntries pick it up.
type pathDP struct {
	minimalDP
	path string
}

func (d *pathDP) StatePath() string { return d.path }

// fakeEvent satisfies device.AttachableEvent.
type fakeEvent struct {
	key  hmtypes.DataPointKey
	kind string
}

func (e *fakeEvent) DataPointKey() hmtypes.DataPointKey { return e.key }
func (e *fakeEvent) EventKind() string                  { return e.kind }

// stubHubStatePaths implements HubStatePathProvider.
type stubHubStatePaths struct {
	paths []string
}

func (s *stubHubStatePaths) HubStatePaths() []string { return s.paths }

// newModelWithDP builds a device + channel with dp attached.
func newModelWithDP(addr string, dp device.ParameterDataPoint) *device.Device {
	d := device.New(device.Config{
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     addr,
		Model:       "HmIP-Test",
		InterfaceID: "test-iface",
	})
	ch := d.AddChannel(addr+":1", 1, "SWITCH", hmenum.ParamsetKeyValues)
	ch.Put(dp)
	return d
}

// buildQFWithDevice returns a QueryFacade backed by a ModelRegistry holding d.
func buildQFWithDevice(d *device.Device) *QueryFacade {
	mr := registry.NewModelRegistry()
	mr.Put(d)
	dr := registry.NewDeviceRegistry()
	qf := newQueryFacadeWithModel("test", dr, mr, nil)
	return qf
}

// ---------------------------------------------------------------------------
// WireSessionRecorderPersistence — wired path
// ---------------------------------------------------------------------------

// TestWireSessionRecorderPersistence_WiredPath exercises the non-nil path
// (Recorder != nil, store != nil, interval > 0).
func TestWireSessionRecorderPersistence_WiredPath(t *testing.T) {
	c := newTestCentral(t)
	store := newStubPersistStore()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	unsub := c.WireSessionRecorderPersistence(ctx, store, "slug", 50*time.Millisecond)
	if unsub == nil {
		t.Fatal("expected non-nil unsub")
	}
	// Replace the wiring (exercises the re-wire path that calls prior unsub).
	unsub2 := c.WireSessionRecorderPersistence(ctx, store, "slug2", 50*time.Millisecond)
	if unsub2 == nil {
		t.Fatal("expected non-nil unsub2")
	}
	unsub2()
}

// TestWireSessionRecorderPersistence_DefaultInterval exercises the
// interval<=0 → defaults to 30s branch.
func TestWireSessionRecorderPersistence_DefaultInterval(t *testing.T) {
	c := newTestCentral(t)
	store := newStubPersistStore()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	unsub := c.WireSessionRecorderPersistence(ctx, store, "slug", 0)
	if unsub == nil {
		t.Fatal("expected non-nil unsub")
	}
	unsub()
}

// ---------------------------------------------------------------------------
// QueryFacade.Devices / HealthSnapshot / OverallHealth — non-nil paths
// ---------------------------------------------------------------------------

func TestQueryFacade_Devices_NonNil(t *testing.T) {
	c := newTestCentral(t)
	qf := c.QueryFacade()
	_ = qf.Devices()
}

func TestQueryFacade_HealthSnapshot_NonNil(t *testing.T) {
	c := newTestCentral(t)
	qf := c.QueryFacade()
	_ = qf.HealthSnapshot()
}

func TestQueryFacade_OverallHealth_NonNil(t *testing.T) {
	c := newTestCentral(t)
	qf := c.QueryFacade()
	_ = qf.OverallHealth()
}

// ---------------------------------------------------------------------------
// GetEventSources / GetEventGroup — channels with attached events
// ---------------------------------------------------------------------------

func TestGetEventSources_WithEvents(t *testing.T) {
	addr := "EV0001"
	d := device.New(device.Config{
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     addr,
		Model:       "HmIP-RC",
		InterfaceID: "test-iface",
	})
	ch := d.AddChannel(addr+":1", 1, "KEY", hmenum.ParamsetKeyValues)
	ev := &fakeEvent{
		key:  hmtypes.DataPointKey{ChannelAddress: addr + ":1", Parameter: "PRESS_SHORT"},
		kind: "keypress",
	}
	ch.AttachGenericEvent(ev)

	qf := buildQFWithDevice(d)
	sources := qf.GetEventSources("")
	if len(sources) == 0 {
		t.Error("GetEventSources with event must return at least one")
	}
}

func TestGetEventSources_InterfaceFilter_NoMatch(t *testing.T) {
	addr := "EV0002"
	d := device.New(device.Config{
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     addr,
		Model:       "HmIP-RC",
		InterfaceID: "test-iface",
	})
	ch := d.AddChannel(addr+":1", 1, "KEY", hmenum.ParamsetKeyValues)
	ev := &fakeEvent{
		key:  hmtypes.DataPointKey{ChannelAddress: addr + ":1", Parameter: "PRESS_SHORT"},
		kind: "keypress",
	}
	ch.AttachGenericEvent(ev)

	qf := buildQFWithDevice(d)
	sources := qf.GetEventSources(hmenum.InterfaceBidCosRF)
	if len(sources) != 0 {
		t.Errorf("filtered GetEventSources must be empty, got %d", len(sources))
	}
}

func TestGetEventGroup_FoundWithParameter(t *testing.T) {
	addr := "EV0003"
	d := device.New(device.Config{
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     addr,
		Model:       "HmIP-RC",
		InterfaceID: "test-iface",
	})
	ch := d.AddChannel(addr+":1", 1, "KEY", hmenum.ParamsetKeyValues)
	ev := &fakeEvent{
		key:  hmtypes.DataPointKey{ChannelAddress: addr + ":1", Parameter: "PRESS_SHORT"},
		kind: "keypress",
	}
	ch.AttachGenericEvent(ev)

	qf := buildQFWithDevice(d)
	got := qf.GetEventGroup(addr+":1", "PRESS_SHORT")
	if got == nil {
		t.Error("GetEventGroup must find the event by parameter name")
	}
}

func TestGetEventGroup_FoundFirstWithEmptyParameter(t *testing.T) {
	addr := "EV0004"
	d := device.New(device.Config{
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     addr,
		Model:       "HmIP-RC",
		InterfaceID: "test-iface",
	})
	ch := d.AddChannel(addr+":1", 1, "KEY", hmenum.ParamsetKeyValues)
	ev := &fakeEvent{
		key:  hmtypes.DataPointKey{ChannelAddress: addr + ":1", Parameter: "PRESS_LONG"},
		kind: "keypress",
	}
	ch.AttachGenericEvent(ev)

	qf := buildQFWithDevice(d)
	got := qf.GetEventGroup(addr+":1", "")
	if got == nil {
		t.Error("GetEventGroup with empty param must return first event")
	}
}

func TestGetEventGroup_NoMatchParameter(t *testing.T) {
	addr := "EV0005"
	d := device.New(device.Config{
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     addr,
		Model:       "HmIP-RC",
		InterfaceID: "test-iface",
	})
	ch := d.AddChannel(addr+":1", 1, "KEY", hmenum.ParamsetKeyValues)
	ev := &fakeEvent{
		key:  hmtypes.DataPointKey{ChannelAddress: addr + ":1", Parameter: "PRESS_SHORT"},
		kind: "keypress",
	}
	ch.AttachGenericEvent(ev)

	qf := buildQFWithDevice(d)
	got := qf.GetEventGroup(addr+":1", "NONEXISTENT")
	if got != nil {
		t.Error("GetEventGroup with non-matching param must return nil")
	}
}

// ---------------------------------------------------------------------------
// GetStatePaths / GetStatePathEntries — DPs implementing statePather
// ---------------------------------------------------------------------------

func TestGetStatePaths_WithPathDP(t *testing.T) {
	addr := "SP0001"
	dp := &pathDP{
		minimalDP: minimalDP{
			key:  hmtypes.DataPointKey{ChannelAddress: addr + ":1", Parameter: "LEVEL"},
			data: hmproto.ParameterData{Operations: hmenum.OperationsRead},
		},
		path: "hm/SP0001/1/LEVEL",
	}
	d := newModelWithDP(addr, dp)
	qf := buildQFWithDevice(d)

	paths := qf.GetStatePaths(nil)
	if len(paths) == 0 {
		t.Error("GetStatePaths must include path from statePather DP")
	}
}

func TestGetStatePaths_WithHub(t *testing.T) {
	qf := newQueryFacadeWithModel("test", registry.NewDeviceRegistry(), registry.NewModelRegistry(), nil)
	qf.SetHubStatePathProvider(&stubHubStatePaths{paths: []string{"hm/hub/sysvar1"}})

	paths := qf.GetStatePaths(nil)
	found := false
	for _, p := range paths {
		if p == "hm/hub/sysvar1" {
			found = true
		}
	}
	if !found {
		t.Error("GetStatePaths must include hub paths when provider is wired")
	}
}

func TestGetStatePaths_EmptyPath_Excluded(t *testing.T) {
	addr := "SP0002"
	dp := &pathDP{
		minimalDP: minimalDP{
			key:  hmtypes.DataPointKey{ChannelAddress: addr + ":1", Parameter: "LEVEL"},
			data: hmproto.ParameterData{},
		},
		path: "",
	}
	d := newModelWithDP(addr, dp)
	qf := buildQFWithDevice(d)
	paths := qf.GetStatePaths(nil)
	if len(paths) != 0 {
		t.Errorf("empty StatePath must be excluded, got %v", paths)
	}
}

func TestGetStatePathEntries_WithPathDP(t *testing.T) {
	addr := "SPE001"
	dp := &pathDP{
		minimalDP: minimalDP{
			key:  hmtypes.DataPointKey{ChannelAddress: addr + ":1", Parameter: "STATE"},
			data: hmproto.ParameterData{Operations: hmenum.OperationsRead},
		},
		path: "hm/SPE001/1/STATE",
	}
	d := newModelWithDP(addr, dp)
	qf := buildQFWithDevice(d)

	entries := qf.GetStatePathEntries()
	if len(entries) == 0 {
		t.Error("GetStatePathEntries must return entries for statePather DP")
	}
	if entries[0].Topic != "hm/SPE001/1/STATE" {
		t.Errorf("entry.Topic = %q, want hm/SPE001/1/STATE", entries[0].Topic)
	}
}

func TestGetStatePathEntries_WithHub(t *testing.T) {
	qf := newQueryFacadeWithModel("test", registry.NewDeviceRegistry(), registry.NewModelRegistry(), nil)
	qf.SetHubStatePathProvider(&stubHubStatePaths{paths: []string{"hm/sysvar/X"}})

	entries := qf.GetStatePathEntries()
	found := false
	for _, e := range entries {
		if e.Topic == "hm/sysvar/X" && e.Address == "" {
			found = true
		}
	}
	if !found {
		t.Error("GetStatePathEntries must add hub paths as Topic-only entries")
	}
}

// ---------------------------------------------------------------------------
// GetParameters / GetUnIgnoreCandidates — channels with VALUES DPs
// ---------------------------------------------------------------------------

func TestGetParameters_WithReadableDP(t *testing.T) {
	addr := "GP0001"
	dp := &minimalDP{
		key:  hmtypes.DataPointKey{ChannelAddress: addr + ":1", Parameter: "LEVEL"},
		data: hmproto.ParameterData{Operations: hmenum.OperationsRead},
	}
	d := newModelWithDP(addr, dp)
	qf := buildQFWithDevice(d)

	params := qf.GetParameters(hmenum.ParamsetKeyValues, 0)
	found := false
	for _, p := range params {
		if p == "LEVEL" {
			found = true
		}
	}
	if !found {
		t.Error("GetParameters must include LEVEL from the test DP")
	}
}

func TestGetParameters_OpsFilter_ExcludesNonReadable(t *testing.T) {
	addr := "GP0002"
	dp := &minimalDP{
		key:  hmtypes.DataPointKey{ChannelAddress: addr + ":1", Parameter: "WRITE_ONLY"},
		data: hmproto.ParameterData{Operations: hmenum.OperationsWrite},
	}
	d := newModelWithDP(addr, dp)
	qf := buildQFWithDevice(d)

	params := qf.GetParameters(hmenum.ParamsetKeyValues, hmenum.OperationsRead)
	for _, p := range params {
		if p == "WRITE_ONLY" {
			t.Error("non-readable DP must be excluded when ops filter is READ")
		}
	}
}

func TestGetParameters_OpsFilter_IncludesMatchingReadable(t *testing.T) {
	addr := "GP0003"
	dp := &minimalDP{
		key:  hmtypes.DataPointKey{ChannelAddress: addr + ":1", Parameter: "TEMP"},
		data: hmproto.ParameterData{Operations: hmenum.OperationsRead | hmenum.OperationsEvent},
	}
	d := newModelWithDP(addr, dp)
	qf := buildQFWithDevice(d)

	params := qf.GetParameters(hmenum.ParamsetKeyValues, hmenum.OperationsRead)
	found := false
	for _, p := range params {
		if p == "TEMP" {
			found = true
		}
	}
	if !found {
		t.Error("readable DP must be included when ops filter is READ")
	}
}

func TestGetUnIgnoreCandidates_WithIgnoredDP(t *testing.T) {
	addr := "UI0001"
	dp := &minimalDP{
		key:         hmtypes.DataPointKey{ChannelAddress: addr + ":1", Parameter: "HIDDEN"},
		forcedUsage: usagePtr(hmenum.DataPointUsageIgnored),
		operations:  hmenum.OperationsRead | hmenum.OperationsEvent,
	}
	d := newModelWithDP(addr, dp)
	qf := buildQFWithDevice(d)

	candidates := qf.GetUnIgnoreCandidates(hmenum.ParamsetKeyValues)
	if !sliceContains(candidates, "HIDDEN") {
		t.Errorf("GetUnIgnoreCandidates must include the simple name for Ignored DPs; got %v", candidates)
	}
	if !sliceContains(candidates, "HIDDEN:VALUES@HmIP-Test:all") {
		t.Errorf("GetUnIgnoreCandidates must include the wildcard variant; got %v", candidates)
	}
	if !sliceContains(candidates, "HIDDEN:VALUES@HmIP-Test:1") {
		t.Errorf("GetUnIgnoreCandidates must include the channel-specific variant; got %v", candidates)
	}
}

// sliceContains is a small test helper for checking presence in a slice.
func sliceContains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func TestGetUnIgnoreCandidates_ExcludesVisibleDP(t *testing.T) {
	addr := "UI0002"
	dp := &minimalDP{
		key: hmtypes.DataPointKey{ChannelAddress: addr + ":1", Parameter: "VISIBLE_PARAM"},
	}
	d := newModelWithDP(addr, dp)
	qf := buildQFWithDevice(d)

	candidates := qf.GetUnIgnoreCandidates(hmenum.ParamsetKeyValues)
	for _, c := range candidates {
		if c == "VISIBLE_PARAM" {
			t.Error("GetUnIgnoreCandidates must not include visible DPs")
		}
	}
}

// TestGetUnIgnoreCandidates_ExcludesCDPSecondary verifies that DPs routed
// through a parent custom DP are not listed as un-ignore candidates.
func TestGetUnIgnoreCandidates_ExcludesCDPSecondary(t *testing.T) {
	addr := "UI0004"
	dp := &minimalDP{
		key:         hmtypes.DataPointKey{ChannelAddress: addr + ":1", Parameter: "OWNED_BY_PARENT"},
		forcedUsage: usagePtr(hmenum.DataPointUsageCDPSecondary),
	}
	d := newModelWithDP(addr, dp)
	qf := buildQFWithDevice(d)

	candidates := qf.GetUnIgnoreCandidates(hmenum.ParamsetKeyValues)
	for _, c := range candidates {
		if c == "OWNED_BY_PARENT" {
			t.Error("CDPSecondary DPs must not appear as un-ignore candidates")
		}
	}
}

// TestGetUnIgnoreCandidates_SkipsTransportScopeParameters verifies that
// device-wide transport-state parameters are filtered from the candidate list.
func TestGetUnIgnoreCandidates_SkipsTransportScopeParameters(t *testing.T) {
	addr := "UI0003"
	chAddr := addr + ":1"
	d := device.New(device.Config{
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     addr,
		Model:       "HmIP-Test",
		InterfaceID: "test-iface",
	})
	ch := d.AddChannel(chAddr, 1, "SWITCH", hmenum.ParamsetKeyValues)
	for _, p := range []hmenum.Parameter{
		hmenum.ParameterConfigPending,
		hmenum.ParameterStickyUnreach,
		hmenum.ParameterUnreach,
	} {
		ch.Put(&minimalDP{
			key:         hmtypes.DataPointKey{ChannelAddress: chAddr, Parameter: string(p)},
			forcedUsage: usagePtr(hmenum.DataPointUsageIgnored),
			operations:  hmenum.OperationsRead | hmenum.OperationsEvent,
		})
	}
	ch.Put(&minimalDP{
		key:         hmtypes.DataPointKey{ChannelAddress: chAddr, Parameter: "REAL_HIDDEN"},
		forcedUsage: usagePtr(hmenum.DataPointUsageIgnored),
		operations:  hmenum.OperationsRead | hmenum.OperationsEvent,
	})
	qf := buildQFWithDevice(d)

	candidates := qf.GetUnIgnoreCandidates(hmenum.ParamsetKeyValues)
	hasReal := false
	for _, c := range candidates {
		switch c {
		case string(hmenum.ParameterConfigPending),
			string(hmenum.ParameterStickyUnreach),
			string(hmenum.ParameterUnreach):
			t.Errorf("transport-scope parameter %q must not appear in un-ignore candidates", c)
		case "REAL_HIDDEN":
			hasReal = true
		}
	}
	if !hasReal {
		t.Error("non-transport hidden DP must still surface as candidate")
	}
}

// ---------------------------------------------------------------------------
// ReadableGenericDataPoints — device with readable DPs
// ---------------------------------------------------------------------------

func TestReadableGenericDataPoints_WithReadableDP(t *testing.T) {
	c := newTestCentral(t)
	addr := "RGP001"
	dp := &minimalDP{
		key:  hmtypes.DataPointKey{ChannelAddress: addr + ":1", Parameter: "LEVEL"},
		data: hmproto.ParameterData{Operations: hmenum.OperationsRead},
	}
	d := newModelWithDP(addr, dp)
	c.ModelRegistry.Put(d)

	got := c.ReadableGenericDataPoints()
	if len(got) == 0 {
		t.Error("ReadableGenericDataPoints must return readable DP")
	}
}
