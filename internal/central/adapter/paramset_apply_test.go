// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// The production description store is the index the domain consumes.
var _ ParamsetDescriptionIndex = (*sqlite.ParamsetStore)(nil)

// fakeDescriptionIndex is an in-memory ParamsetDescriptionIndex. List answers
// in insertion order.
type fakeDescriptionIndex struct {
	mu      sync.Mutex
	records []sqlite.ParamsetRecord
}

func (f *fakeDescriptionIndex) put(centralName, iface, addr string, ps hmproto.Paramset) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.records = append(f.records, sqlite.ParamsetRecord{
		CentralName: centralName, InterfaceID: iface, ChannelAddress: addr,
		ParamsetKey: hmenum.ParamsetKeyMaster, Paramset: ps,
	})
}

func (f *fakeDescriptionIndex) Get(
	_ context.Context, centralName, ifaceID, channelAddress string, psKey hmenum.ParamsetKey,
) (sqlite.ParamsetRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.records {
		if r.CentralName == centralName && r.InterfaceID == ifaceID && r.ChannelAddress == channelAddress && r.ParamsetKey == psKey {
			return r, nil
		}
	}
	return sqlite.ParamsetRecord{}, sqlite.ErrParamsetNotFound
}

func (f *fakeDescriptionIndex) ListByCentral(_ context.Context, centralName string) ([]sqlite.ParamsetRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []sqlite.ParamsetRecord
	for _, r := range f.records {
		if r.CentralName == centralName {
			out = append(out, r)
		}
	}
	return out, nil
}

func levelDescription(maxRaw string) hmproto.Paramset {
	return hmproto.Paramset{
		string(hmenum.ParameterLevel): {
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
			Min:        json.RawMessage("0.0"),
			Max:        json.RawMessage(maxRaw),
		},
	}
}

// applyFixture holds a central "ccu-01" whose model knows the source device
// SRC and the target devices T1..T4 (no channel objects, so writes take the
// backend path), plus the description index and the backend fake.
type applyFixture struct {
	domain *ParamsetApplyDomain
	index  *fakeDescriptionIndex
	ops    *paramsetFakeOps
	reg    *central.Registry
	unit   *central.Unit
	puts   *[]string
}

func buildApplyFixture(t *testing.T) applyFixture {
	t.Helper()
	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	for _, addr := range []string{"SRC", "T1", "T2", "T3", "T4"} {
		c.ModelRegistry.Put(device.New(device.Config{
			InterfaceID: "HmIP-RF", Interface: hmenum.InterfaceHmIPRF,
			Address: addr, Model: "HmIP-BROLL", Name: "Device " + addr,
		}))
	}
	index := &fakeDescriptionIndex{}
	var (
		mu   sync.Mutex
		puts []string
	)
	ops := &paramsetFakeOps{
		getParamsetDescriptionFn: func(context.Context, string, hmenum.ParamsetKey) (map[string]hmproto.ParameterData, error) {
			return levelDescription("1.0"), nil
		},
		getParamsetFn: func(context.Context, string, hmenum.ParamsetKey) (map[string]any, error) {
			return map[string]any{string(hmenum.ParameterLevel): 0.5}, nil
		},
	}
	ops.putParamsetFn = func(_ context.Context, address string, _ hmenum.ParamsetKey, _ map[string]any) error {
		mu.Lock()
		defer mu.Unlock()
		puts = append(puts, address)
		return nil
	}
	w := client.NewValueWriter()
	w.Register("ccu-01", "HmIP-RF", ops)
	domain := NewParamsetApplyDomain(reg, index, NewParamsetsDomain(reg, w))
	return applyFixture{domain: domain, index: index, ops: ops, reg: reg, unit: c, puts: &puts}
}

func TestParamsetApplyTargetsEligibility(t *testing.T) {
	t.Parallel()
	f := buildApplyFixture(t)
	named := f.unit.ModelRegistry
	dev, _ := named.Get("T1")
	ch := dev.AddChannel("T1:1", 1, "BLIND", hmenum.ParamsetKeyMaster)

	same := levelDescription("1.0")
	f.index.put("ccu-01", "HmIP-RF", "SRC:1", same)
	f.index.put("ccu-01", "HmIP-RF", "T1:1", levelDescription("1.0")) // identical, modelled
	f.index.put("ccu-01", "HmIP-RF", "T2:1", levelDescription("2.0")) // description differs
	f.index.put("ccu-01", "BidCos-RF", "T3:1", same)                  // other interface
	f.index.put("ccu-01", "HmIP-RF", "GONE:1", same)                  // not modelled
	f.index.put("ccu-02", "HmIP-RF", "T4:1", same)                    // other central

	got, err := f.domain.ApplyTargets(context.Background(), "SRC:1")
	if err != nil {
		t.Fatalf("ApplyTargets: %v", err)
	}
	want := []interfaces.ParamsetApplyTarget{
		{
			Address: "T1:1", Name: ch.Name(), DeviceAddress: "T1", DeviceName: "Device T1",
			DeviceModel: "HmIP-BROLL", InterfaceID: "HmIP-RF",
		},
		{Address: "GONE:1", InterfaceID: "HmIP-RF"},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("ApplyTargets =\n %+v\nwant\n %+v", got, want)
	}
}

func TestParamsetApplyTargetsSourceWithoutDescription(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		source string
	}{
		{name: "modelled source without stored description", source: "SRC:1"},
		{name: "source not modelled on any central", source: "NOPE:1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := buildApplyFixture(t)
			f.index.put("ccu-01", "HmIP-RF", "T1:1", levelDescription("1.0"))
			_, err := f.domain.ApplyTargets(context.Background(), tc.source)
			if !errors.Is(err, hmerr.ErrDescriptionNotFound) {
				t.Fatalf("err = %v, want ErrDescriptionNotFound", err)
			}
		})
	}
}

func TestParamsetApplyToChannelsOutcomes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		values     map[string]any
		targets    []string
		dryRun     bool
		failPut    string
		wantStatus []string
		wantPuts   []string
	}{
		{
			name:       "applied with report",
			values:     map[string]any{string(hmenum.ParameterLevel): 0.5},
			targets:    []string{"T1:1"},
			wantStatus: []string{interfaces.ApplyApplied},
			wantPuts:   []string{"T1:1"},
		},
		{
			name:       "dry run would apply and writes nothing",
			values:     map[string]any{string(hmenum.ParameterLevel): 0.5},
			targets:    []string{"T1:1", "T3:1"},
			dryRun:     true,
			wantStatus: []string{interfaces.ApplyWouldApply, interfaces.ApplyWouldApply},
		},
		{
			name:       "refused on description drift",
			values:     map[string]any{string(hmenum.ParameterLevel): 0.5},
			targets:    []string{"T2:1"},
			wantStatus: []string{interfaces.ApplyRefused},
		},
		{
			name:       "refused without stored description",
			values:     map[string]any{string(hmenum.ParameterLevel): 0.5},
			targets:    []string{"T4:1"},
			wantStatus: []string{interfaces.ApplyRefused},
		},
		{
			name:       "refused on validation",
			values:     map[string]any{string(hmenum.ParameterLevel): 5.0},
			targets:    []string{"T1:1"},
			wantStatus: []string{interfaces.ApplyRefused},
		},
		{
			// The dry run never reaches the write path, so only the
			// per-target validation against the stored description can
			// refuse here.
			name:       "dry run refuses a value the target description rejects",
			values:     map[string]any{string(hmenum.ParameterLevel): 1.5},
			targets:    []string{"T1:1"},
			dryRun:     true,
			wantStatus: []string{interfaces.ApplyRefused},
		},
		{
			name:       "refused when target is the source",
			values:     map[string]any{string(hmenum.ParameterLevel): 0.5},
			targets:    []string{"SRC:1"},
			wantStatus: []string{interfaces.ApplyRefused},
		},
		{
			name:       "failed upstream",
			values:     map[string]any{string(hmenum.ParameterLevel): 0.5},
			targets:    []string{"T1:1"},
			failPut:    "T1:1",
			wantStatus: []string{interfaces.ApplyFailed},
			wantPuts:   []string{"T1:1"},
		},
		{
			name:       "order preserved and later targets proceed after failures",
			values:     map[string]any{string(hmenum.ParameterLevel): 0.5},
			targets:    []string{"T3:1", "T2:1", "T1:1"},
			failPut:    "T3:1",
			wantStatus: []string{interfaces.ApplyFailed, interfaces.ApplyRefused, interfaces.ApplyApplied},
			wantPuts:   []string{"T3:1", "T1:1"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := buildApplyFixture(t)
			f.index.put("ccu-01", "HmIP-RF", "SRC:1", levelDescription("1.0"))
			f.index.put("ccu-01", "HmIP-RF", "T1:1", levelDescription("1.0"))
			f.index.put("ccu-01", "HmIP-RF", "T2:1", levelDescription("2.0"))
			f.index.put("ccu-01", "HmIP-RF", "T3:1", levelDescription("1.0"))
			if tc.failPut != "" {
				record := f.ops.putParamsetFn
				f.ops.putParamsetFn = func(ctx context.Context, address string, key hmenum.ParamsetKey, values map[string]any) error {
					if err := record(ctx, address, key, values); err != nil {
						return err
					}
					if address == tc.failPut {
						return errors.New("ccu unreachable")
					}
					return nil
				}
			}

			got, err := f.domain.ApplyToChannels(context.Background(), "SRC:1", tc.values, tc.targets, tc.dryRun)
			if err != nil {
				t.Fatalf("ApplyToChannels: %v", err)
			}
			if len(got) != len(tc.targets) {
				t.Fatalf("outcomes = %+v, want one per target", got)
			}
			for i, o := range got {
				if o.Address != tc.targets[i] {
					t.Errorf("outcome %d address = %q, want %q (request order)", i, o.Address, tc.targets[i])
				}
				if o.Status != tc.wantStatus[i] {
					t.Errorf("outcome %d (%s) status = %q (reason %q), want %q", i, o.Address, o.Status, o.Reason, tc.wantStatus[i])
				}
				switch o.Status {
				case interfaces.ApplyApplied:
					if o.Result == nil || o.Result.Divergences == nil {
						t.Errorf("outcome %d: applied without a write report: %+v", i, o.Result)
					}
				case interfaces.ApplyRefused, interfaces.ApplyFailed:
					if o.Reason == "" {
						t.Errorf("outcome %d: %s without a reason", i, o.Status)
					}
				}
			}
			if !slices.Equal(*f.puts, tc.wantPuts) {
				t.Fatalf("backend writes = %v, want %v", *f.puts, tc.wantPuts)
			}
		})
	}
}

func TestParamsetApplyToChannelsSourceWithoutDescriptionRefusesEveryTarget(t *testing.T) {
	t.Parallel()
	f := buildApplyFixture(t)
	f.index.put("ccu-01", "HmIP-RF", "T1:1", levelDescription("1.0"))
	got, err := f.domain.ApplyToChannels(context.Background(), "SRC:1",
		map[string]any{string(hmenum.ParameterLevel): 0.5}, []string{"T1:1", "T2:1"}, false)
	if err != nil {
		t.Fatalf("ApplyToChannels: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("outcomes = %+v, want one per target", got)
	}
	for _, o := range got {
		if o.Status != interfaces.ApplyRefused || o.Reason == "" {
			t.Errorf("outcome %+v, want refused with a reason", o)
		}
	}
	if len(*f.puts) != 0 {
		t.Fatalf("backend writes = %v, want none", *f.puts)
	}
}
