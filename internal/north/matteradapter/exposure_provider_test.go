// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package matteradapter_test

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/go-fabric-ui/fabricui"
	"github.com/SukramJ/go-fabric/eligibility"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/north/matteradapter"
	matterendpoint "github.com/SukramJ/openccu-loom/internal/store/matterendpoint"
)

// memExposures is an in-memory allowlist with the two methods the
// provider reads and writes.
type memExposures struct {
	rows map[matterendpoint.SourceKey]matterendpoint.ExposureRecord
	err  error
}

func (m *memExposures) ListExposures(context.Context, string) ([]matterendpoint.ExposureRecord, error) {
	if m.err != nil {
		return nil, m.err
	}
	out := make([]matterendpoint.ExposureRecord, 0, len(m.rows))
	for k := range m.rows {
		out = append(out, m.rows[k])
	}
	return out, nil
}

func (m *memExposures) UpsertExposure(_ context.Context, rec matterendpoint.ExposureRecord) error {
	if m.rows == nil {
		m.rows = map[matterendpoint.SourceKey]matterendpoint.ExposureRecord{}
	}
	m.rows[rec.Key] = rec
	return nil
}

type memAudit struct{ entries []audit.Entry }

func (a *memAudit) Record(e audit.Entry)   { a.entries = append(a.entries, e) }
func (a *memAudit) List(int) []audit.Entry { return a.entries }

type staticLabels map[string]string

func (l staticLabels) ChannelTypedParameterLabel(channelType, parameter string) string {
	return l[channelType+"/"+parameter]
}

var (
	keyLamp = matterendpoint.SourceKey{CentralName: "ccu", DeviceAddress: "000A", ChannelNo: 4, DPKind: matterendpoint.DPKindCustom, DPKey: "switch"}
	keyTemp = matterendpoint.SourceKey{CentralName: "ccu", DeviceAddress: "000B", ChannelNo: 1, DPKind: matterendpoint.DPKindGeneric, DPKey: "ACTUAL_TEMPERATURE"}
	keyBad  = matterendpoint.SourceKey{CentralName: "ccu", DeviceAddress: "000C", ChannelNo: 1, DPKind: matterendpoint.DPKindGeneric, DPKey: "LEVEL"}
)

func candidates() []matteradapter.Candidate {
	// Deliberately out of order: the list is sorted by the provider.
	return []matteradapter.Candidate{
		{Key: keyTemp, DisplayName: "Sensor", ChannelType: "WEATHER_TRANSMITTER", Verdict: eligibility.Verdict{State: eligibility.StateMappable, DeviceType: 0x0302}},
		{Key: keyLamp, DisplayName: "Lamp", ChannelType: "SWITCH_VIRTUAL_RECEIVER", Verdict: eligibility.Verdict{State: eligibility.StateMappable, DeviceType: 0x010A}},
		{Key: keyBad, DisplayName: "Blind", ChannelType: "BLIND_TRANSMITTER", Verdict: eligibility.Verdict{State: eligibility.StateUnmappable, Reason: "no cover profile"}},
	}
}

func newProvider(store *memExposures, rec *memAudit) *matteradapter.ExposureProvider {
	return matteradapter.NewExposureProvider(matteradapter.ExposureProviderConfig{
		Candidates: func(context.Context) []matteradapter.Candidate { return candidates() },
		Store:      store,
		Labels:     staticLabels{"WEATHER_TRANSMITTER/ACTUAL_TEMPERATURE": "Temperatur"},
		Translate:  func(k string) string { return "T:" + k },
		Actor:      func(context.Context) string { return "alice" },
		Audit:      rec,
	})
}

func TestExposureProviderSchemaNamesTheSourceKeyColumnsAndGroups(t *testing.T) {
	t.Parallel()
	p := newProvider(&memExposures{}, nil)
	s, err := p.Schema(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !s.Groups {
		t.Error("the list groups by device")
	}
	want := []string{"central", "device", "channel", "kind", "key", "parameter", "type"}
	if len(s.Columns) != len(want) {
		t.Fatalf("columns = %d, want %d", len(s.Columns), len(want))
	}
	for i, c := range s.Columns {
		if c.Key != want[i] {
			t.Errorf("column %d = %q, want %q", i, c.Key, want[i])
		}
		if c.Label != "T:node.exposure.column."+want[i] {
			t.Errorf("column %q label = %q: not translated through the catalogue key", c.Key, c.Label)
		}
	}
}

func TestExposureProviderListMergesTheAllowlistAndSorts(t *testing.T) {
	t.Parallel()
	store := &memExposures{rows: map[matterendpoint.SourceKey]matterendpoint.ExposureRecord{
		keyTemp: {Key: keyTemp, Enabled: true, FriendlyName: "Outside"},
	}}
	items, err := newProvider(store, nil).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("items = %d, want 3", len(items))
	}
	if items[0].Key != keyLamp.String() || items[1].Key != keyTemp.String() || items[2].Key != keyBad.String() {
		t.Errorf("order = %q %q %q, want lamp, temp, blind", items[0].Key, items[1].Key, items[2].Key)
	}
	lamp, temp := items[0], items[1]
	if lamp.Enabled || lamp.Source != matteradapter.ExposureSourceRule || lamp.Name != "Lamp" {
		t.Errorf("lamp without a row: enabled=%v source=%q name=%q", lamp.Enabled, lamp.Source, lamp.Name)
	}
	if !temp.Enabled || temp.Source != matteradapter.ExposureSourceOverride || temp.Name != "Outside" {
		t.Errorf("temp with a row: enabled=%v source=%q name=%q", temp.Enabled, temp.Source, temp.Name)
	}
	if temp.Fields["parameter"] != "Temperatur" {
		t.Errorf("parameter label = %q, want the channel-typed translation", temp.Fields["parameter"])
	}
	if lamp.Fields["parameter"] != "Switch" {
		t.Errorf("parameter label without a translation = %q, want the title-cased key", lamp.Fields["parameter"])
	}
	if lamp.Fields["type"] != "On/Off Plug-in Unit" || temp.Fields["type"] != "Temperature Sensor" {
		t.Errorf("type labels = %q / %q, want loom's device-type names", lamp.Fields["type"], temp.Fields["type"])
	}
	if lamp.Fields["channel"] != "4" || lamp.Fields["kind"] != "custom" || lamp.Fields["central"] != "ccu" {
		t.Errorf("source-key fields = %v", lamp.Fields)
	}
}

func TestExposureProviderPreviewEqualsApplyOutcome(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := &memExposures{}
	rec := &memAudit{}
	p := newProvider(store, rec)
	name := "  Reading lamp "
	updates := []fabricui.Update{
		{Key: keyLamp.String(), Enabled: true, Name: &name},
		{Key: keyTemp.String(), Enabled: true},
	}
	preview, err := p.Preview(ctx, updates)
	if err != nil {
		t.Fatal(err)
	}
	if len(store.rows) != 0 {
		t.Fatal("a preview wrote rows")
	}
	n, err := p.Apply(ctx, updates)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("applied = %d, want 2", n)
	}
	after, err := p.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byKey := map[string]fabricui.Item{}
	for _, it := range after {
		byKey[it.Key] = it
	}
	for _, pv := range preview {
		got := byKey[pv.Key]
		if got.Enabled != pv.Enabled || got.Name != pv.Name || got.Source != pv.Source || got.Verdict.State != pv.Verdict.State {
			t.Errorf("%s: preview %+v, after apply %+v", pv.Key, pv, got)
		}
	}
	if byKey[keyLamp.String()].Name != "Reading lamp" {
		t.Errorf("name = %q, want the trimmed rename", byKey[keyLamp.String()].Name)
	}
	if store.rows[keyLamp].Actor != "alice" {
		t.Errorf("actor = %q, want the request's identity", store.rows[keyLamp].Actor)
	}
	if len(rec.entries) != 1 || rec.entries[0].Action != audit.ActionMatterExposureBulk || rec.entries[0].User != "alice" || rec.entries[0].Note != "applied=2" {
		t.Errorf("audit = %+v, want one matter_exposure_bulk entry by alice", rec.entries)
	}

	// A second apply without a name keeps the one given before.
	if _, err := p.Apply(ctx, []fabricui.Update{{Key: keyLamp.String(), Enabled: false}}); err != nil {
		t.Fatal(err)
	}
	if store.rows[keyLamp].FriendlyName != "Reading lamp" || store.rows[keyLamp].Enabled {
		t.Errorf("row after a plain disable = %+v, want the name kept and enabled false", store.rows[keyLamp])
	}
}

func TestExposureProviderRefusesUnknownAndUnmappable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := &memExposures{}
	rec := &memAudit{}
	p := newProvider(store, rec)

	_, err := p.Apply(ctx, []fabricui.Update{{Key: keyLamp.String(), Enabled: true}, {Key: "nope", Enabled: true}})
	if !errors.Is(err, fabricui.ErrUnknownItem) {
		t.Errorf("unknown key: err = %v, want ErrUnknownItem", err)
	}
	if len(store.rows) != 0 {
		t.Error("a refused batch wrote the valid half")
	}
	_, err = p.Preview(ctx, []fabricui.Update{{Key: keyBad.String(), Enabled: true}})
	if !errors.Is(err, fabricui.ErrInvalidUpdate) {
		t.Errorf("enable unmappable: err = %v, want ErrInvalidUpdate", err)
	}
	// Disabling an unmappable row is allowed: it removes a stale enable.
	if _, err := p.Apply(ctx, []fabricui.Update{{Key: keyBad.String(), Enabled: false}}); err != nil {
		t.Errorf("disable unmappable: %v", err)
	}
	if len(rec.entries) != 1 {
		t.Errorf("audit entries = %d, want 1 for the one applied batch", len(rec.entries))
	}
}

func TestExposureProviderStoreErrorIsNotA400(t *testing.T) {
	t.Parallel()
	boom := errors.New("sqlite: locked")
	p := newProvider(&memExposures{err: boom}, nil)
	_, err := p.List(context.Background())
	if !errors.Is(err, boom) || errors.Is(err, fabricui.ErrUnknownItem) || errors.Is(err, fabricui.ErrInvalidUpdate) {
		t.Errorf("err = %v, want the store's error unwrapped into neither 400 sentinel", err)
	}
}
