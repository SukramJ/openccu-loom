// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

const repairDev = "0001ABCD"

// repairBackend is a live-backend stand-in: per-address descriptions and
// stored paramsets, a recorded put log, and an optional put fault. A
// successful put merges the written values into the store so the read-back
// reflects the write.
type repairBackend struct {
	mu      sync.Mutex
	descs   map[string]map[string]hmproto.ParameterData
	stored  map[string]map[string]any
	puts    []repairPut
	putErr  error
	descErr error
}

type repairPut struct {
	address string
	values  map[string]any
}

func (b *repairBackend) ops() *paramsetFakeOps {
	return &paramsetFakeOps{
		getParamsetDescriptionFn: func(_ context.Context, address string, _ hmenum.ParamsetKey) (map[string]hmproto.ParameterData, error) {
			b.mu.Lock()
			defer b.mu.Unlock()
			if b.descErr != nil {
				return nil, b.descErr
			}
			d, ok := b.descs[address]
			if !ok {
				return nil, errors.New("unknown paramset")
			}
			return d, nil
		},
		getParamsetFn: func(_ context.Context, address string, _ hmenum.ParamsetKey) (map[string]any, error) {
			b.mu.Lock()
			defer b.mu.Unlock()
			out := map[string]any{}
			for k, v := range b.stored[address] {
				out[k] = v
			}
			return out, nil
		},
		putParamsetFn: func(_ context.Context, address string, _ hmenum.ParamsetKey, values map[string]any) error {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.puts = append(b.puts, repairPut{address: address, values: values})
			if b.putErr != nil {
				return b.putErr
			}
			for k, v := range values {
				b.stored[address][k] = v
			}
			return nil
		},
	}
}

func (b *repairBackend) putLog() []repairPut {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.puts)
}

func rw() hmenum.Operations { return hmenum.OperationsRead | hmenum.OperationsWrite }

// repairFixture builds central "ccu-01" holding a BidCos-RF device with
// channels :1 (MASTER in its cached description) and :2 (VALUES only).
func repairFixture(t *testing.T, b *repairBackend) (*ConfigRepairDomain, *audit.Buffer, *central.Unit) {
	t.Helper()
	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("register: %v", err)
	}
	iface := hmenum.InterfaceBidCosRF
	dev := device.New(device.Config{
		InterfaceID: string(iface), Interface: iface, Address: repairDev, Model: "HM-LC-Sw1-FM", Name: "Flur",
	})
	dev.AddChannel(repairDev+":1", 1, "SWITCH", hmenum.ParamsetKeyValues)
	dev.AddChannel(repairDev+":2", 2, "KEY", hmenum.ParamsetKeyValues)
	c.ModelRegistry.Put(dev)
	wire := hmtypes.ParseWireInterfaceID(string(iface))
	c.DescRegistry.Put(wire, hmproto.DeviceDescription{
		Address: repairDev + ":1", Paramsets: []string{"MASTER", "VALUES"},
	})
	c.DescRegistry.Put(wire, hmproto.DeviceDescription{
		Address: repairDev + ":2", Paramsets: []string{"VALUES"},
	})
	w := client.NewValueWriter()
	w.Register("ccu-01", wire, b.ops())
	rec := audit.NewBuffer(16)
	return NewConfigRepairDomain(reg, w, rec), rec, c
}

func singleChannelBackend(desc map[string]hmproto.ParameterData, stored map[string]any) *repairBackend {
	return &repairBackend{
		descs:  map[string]map[string]hmproto.ParameterData{repairDev + ":1": desc},
		stored: map[string]map[string]any{repairDev + ":1": stored},
	}
}

func repairOne(t *testing.T, d *ConfigRepairDomain, dryRun bool) interfaces.ConfigRepairOutcome {
	t.Helper()
	out, err := d.RepairDeviceConfig(context.Background(), repairDev, []string{repairDev + ":1"}, dryRun)
	if err != nil {
		t.Fatalf("RepairDeviceConfig: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("outcomes=%d, want 1", len(out))
	}
	return out[0]
}

func correctionFor(o interfaces.ConfigRepairOutcome, name string) (interfaces.ConfigRepairCorrection, bool) {
	for _, c := range o.Corrections {
		if c.Parameter == name {
			return c, true
		}
	}
	return interfaces.ConfigRepairCorrection{}, false
}

func intDesc(minV, maxV string) hmproto.ParameterData {
	return hmproto.ParameterData{
		Type: hmenum.ParameterTypeInteger, Operations: rw(),
		Min: json.RawMessage(minV), Max: json.RawMessage(maxV),
	}
}

// TestConfigRepairCleanWritesNothing pins that a store whose every value the
// description accepts is reported clean and costs no write.
func TestConfigRepairCleanWritesNothing(t *testing.T) {
	t.Parallel()
	b := singleChannelBackend(
		map[string]hmproto.ParameterData{"TX": intDesc("1", "10")},
		map[string]any{"TX": 3},
	)
	d, rec, _ := repairFixture(t, b)
	o := repairOne(t, d, false)
	if o.Status != interfaces.RepairClean {
		t.Fatalf("status=%s, want clean (%+v)", o.Status, o)
	}
	if n := len(b.putLog()); n != 0 {
		t.Fatalf("puts=%d, want 0", n)
	}
	if n := len(rec.List(10)); n != 0 {
		t.Fatalf("audit rows=%d, want 0", n)
	}
}

// TestConfigRepairClampsToViolatedBoundAndWritesFullSet pins the clamp, the
// full write set (untouched valid values included, read-only values
// excluded), the read-back report and the single audit row.
func TestConfigRepairClampsToViolatedBoundAndWritesFullSet(t *testing.T) {
	t.Parallel()
	b := singleChannelBackend(
		map[string]hmproto.ParameterData{
			"HIGH": intDesc("1", "10"),
			"LOW":  {Type: hmenum.ParameterTypeFloat, Operations: rw(), Min: json.RawMessage("0.5"), Max: json.RawMessage("2.0")},
			"OK":   intDesc("0", "5"),
			"RO":   {Type: hmenum.ParameterTypeInteger, Operations: hmenum.OperationsRead, Min: json.RawMessage("0"), Max: json.RawMessage("1")},
		},
		map[string]any{"HIGH": 99, "LOW": 0.1, "OK": 2, "RO": 7},
	)
	d, rec, _ := repairFixture(t, b)
	o := repairOne(t, d, false)
	if o.Status != interfaces.RepairRepaired {
		t.Fatalf("status=%s, want repaired (%+v)", o.Status, o)
	}
	high, ok := correctionFor(o, "HIGH")
	if !ok || high.Corrected != 10 || !strings.Contains(high.Reason, "MAX 10") {
		t.Errorf("HIGH correction=%+v, want clamped to 10 naming MAX 10", high)
	}
	low, ok := correctionFor(o, "LOW")
	if !ok || low.Corrected != 0.5 || !strings.Contains(low.Reason, "MIN 0.5") {
		t.Errorf("LOW correction=%+v, want clamped to 0.5 naming MIN 0.5", low)
	}
	if _, ok := correctionFor(o, "OK"); ok {
		t.Errorf("OK was corrected though valid")
	}
	if c, ok := correctionFor(o, "RO"); ok {
		t.Errorf("read-only RO is not part of the rewrite and must not be corrected: %+v", c)
	}
	puts := b.putLog()
	if len(puts) != 1 {
		t.Fatalf("puts=%d, want exactly one full write", len(puts))
	}
	want := map[string]any{"HIGH": 10, "LOW": 0.5, "OK": 2}
	if len(puts[0].values) != len(want) {
		t.Fatalf("write set=%v, want %v (read-only RO excluded)", puts[0].values, want)
	}
	for k, v := range want {
		if puts[0].values[k] != v {
			t.Errorf("write[%s]=%v, want %v", k, puts[0].values[k], v)
		}
	}
	if o.Result == nil || o.Result.ReadbackError != "" || len(o.Result.Divergences) != 0 {
		t.Errorf("result=%+v, want a clean read-back report", o.Result)
	}
	rows := rec.List(10)
	if len(rows) != 1 || rows[0].Action != audit.ActionParamsetWrite || rows[0].Note != configRepairAuditNote || rows[0].ChannelNo != 1 {
		t.Fatalf("audit rows=%+v, want one repair paramset_write row on channel 1", rows)
	}
}

// TestConfigRepairNeverClampsSpecialValue pins that a declared SPECIAL value
// outside MIN/MAX is a valid stored value.
func TestConfigRepairNeverClampsSpecialValue(t *testing.T) {
	t.Parallel()
	desc := intDesc("1", "10")
	desc.Special = json.RawMessage(`[{"ID":"NOT_USED","VALUE":0}]`)
	b := singleChannelBackend(map[string]hmproto.ParameterData{"TX": desc}, map[string]any{"TX": 0})
	d, _, _ := repairFixture(t, b)
	if o := repairOne(t, d, false); o.Status != interfaces.RepairClean {
		t.Fatalf("status=%s, want clean — SPECIAL must not be clamped (%+v)", o.Status, o)
	}
}

// TestConfigRepairUnusableFallsBackToDefaultOrDrops pins the DEFAULT
// fallback and the drop when no valid DEFAULT exists.
func TestConfigRepairUnusableFallsBackToDefaultOrDrops(t *testing.T) {
	t.Parallel()
	enumWithDefault := hmproto.ParameterData{
		Type: hmenum.ParameterTypeEnum, Operations: rw(), ValueList: []string{"A", "B"}, Default: json.RawMessage("1"),
	}
	enumNoDefault := hmproto.ParameterData{Type: hmenum.ParameterTypeEnum, Operations: rw(), ValueList: []string{"A", "B"}}
	b := singleChannelBackend(
		map[string]hmproto.ParameterData{"WITH": enumWithDefault, "WITHOUT": enumNoDefault},
		map[string]any{"WITH": 7, "WITHOUT": 9},
	)
	d, _, _ := repairFixture(t, b)
	o := repairOne(t, d, false)
	with, ok := correctionFor(o, "WITH")
	if !ok || with.Corrected != 1 || !strings.Contains(with.Reason, "DEFAULT") {
		t.Errorf("WITH correction=%+v, want DEFAULT 1", with)
	}
	without, ok := correctionFor(o, "WITHOUT")
	if !ok || without.Corrected != nil || !strings.Contains(without.Reason, "dropped") {
		t.Errorf("WITHOUT correction=%+v, want dropped", without)
	}
	puts := b.putLog()
	if len(puts) != 1 {
		t.Fatalf("puts=%d, want 1", len(puts))
	}
	if _, sent := puts[0].values["WITHOUT"]; sent {
		t.Errorf("dropped parameter was written: %v", puts[0].values)
	}
	if puts[0].values["WITH"] != 1 {
		t.Errorf("WITH written as %v, want 1", puts[0].values["WITH"])
	}
}

// TestConfigRepairReportsWrongTypeCoercion pins that a coercible value of the
// wrong wire type is rewritten in the descriptor's type and reported.
func TestConfigRepairReportsWrongTypeCoercion(t *testing.T) {
	t.Parallel()
	b := singleChannelBackend(map[string]hmproto.ParameterData{"TX": intDesc("1", "10")}, map[string]any{"TX": "5"})
	d, _, _ := repairFixture(t, b)
	o := repairOne(t, d, true)
	c, ok := correctionFor(o, "TX")
	if o.Status != interfaces.RepairWouldRepair || !ok || c.Corrected != 5 {
		t.Fatalf("outcome=%+v, want would_repair correcting TX to 5", o)
	}
	if n := len(b.putLog()); n != 0 {
		t.Fatalf("dry run wrote %d times", n)
	}
}

// TestConfigRepairForeignParameters pins the foreign handling: dry run
// reports foreign_parameters; the real run still writes the valid set; a
// write fault with foreign entries is foreign_parameters with the error, and
// without them it is failed.
func TestConfigRepairForeignParameters(t *testing.T) {
	t.Parallel()
	desc := map[string]hmproto.ParameterData{"TX": intDesc("1", "10")}

	b := singleChannelBackend(desc, map[string]any{"TX": 3, "GHOST": 1})
	d, _, _ := repairFixture(t, b)
	dry := repairOne(t, d, true)
	if dry.Status != interfaces.RepairForeignParameters || !slices.Equal(dry.Foreign, []string{"GHOST"}) {
		t.Fatalf("dry=%+v, want foreign_parameters [GHOST]", dry)
	}
	wet := repairOne(t, d, false)
	if wet.Status != interfaces.RepairForeignParameters || wet.Result == nil {
		t.Fatalf("wet=%+v, want foreign_parameters with a read-back", wet)
	}
	if puts := b.putLog(); len(puts) != 1 || len(puts[0].values) != 1 || puts[0].values["TX"] != 3 {
		t.Fatalf("puts=%+v, want one write of TX only", puts)
	}

	faulty := singleChannelBackend(desc, map[string]any{"TX": 3, "GHOST": 1})
	faulty.putErr = errors.New("rejected")
	d2, _, _ := repairFixture(t, faulty)
	o := repairOne(t, d2, false)
	if o.Status != interfaces.RepairForeignParameters || o.Error != "rejected" {
		t.Fatalf("fault with foreign=%+v, want foreign_parameters carrying the error", o)
	}

	plain := singleChannelBackend(desc, map[string]any{"TX": 99})
	plain.putErr = errors.New("rejected")
	d3, rec, _ := repairFixture(t, plain)
	o = repairOne(t, d3, false)
	if o.Status != interfaces.RepairFailed || o.Error != "rejected" {
		t.Fatalf("fault without foreign=%+v, want failed", o)
	}
	if n := len(rec.List(10)); n != 0 {
		t.Fatalf("a failed write was audited (%d rows)", n)
	}
}

// TestConfigRepairNothingUsableIsFailedWithoutWrite pins that a channel
// whose only corrections drop every parameter is not written (an empty
// paramset changes nothing) and reports failed instead of a false success.
func TestConfigRepairNothingUsableIsFailedWithoutWrite(t *testing.T) {
	t.Parallel()
	enumNoDefault := hmproto.ParameterData{Type: hmenum.ParameterTypeEnum, Operations: rw(), ValueList: []string{"A"}}
	b := singleChannelBackend(map[string]hmproto.ParameterData{"E": enumNoDefault}, map[string]any{"E": 5})
	d, _, _ := repairFixture(t, b)
	o := repairOne(t, d, false)
	if o.Status != interfaces.RepairFailed || o.Error == "" {
		t.Fatalf("outcome=%+v, want failed with an error", o)
	}
	if n := len(b.putLog()); n != 0 {
		t.Fatalf("puts=%d, want 0", n)
	}
}

// TestConfigRepairEnumeratesMasterBearingChannels pins the default channel
// set: the device level when the model carries the root channel, and every
// channel whose cached description lists MASTER.
func TestConfigRepairEnumeratesMasterBearingChannels(t *testing.T) {
	t.Parallel()
	b := &repairBackend{
		descs: map[string]map[string]hmproto.ParameterData{
			repairDev:        {"DEV": intDesc("0", "1")},
			repairDev + ":1": {"TX": intDesc("1", "10")},
		},
		stored: map[string]map[string]any{repairDev: {"DEV": 1}, repairDev + ":1": {"TX": 2}},
	}
	d, _, c := repairFixture(t, b)
	dev, _ := c.ModelRegistry.Get(repairDev)
	dev.EnsureRootChannel()
	out, err := d.RepairDeviceConfig(context.Background(), repairDev, nil, true)
	if err != nil {
		t.Fatalf("RepairDeviceConfig: %v", err)
	}
	got := make([]string, 0, len(out))
	for _, o := range out {
		got = append(got, o.Channel)
		if o.Status != interfaces.RepairClean {
			t.Errorf("%s status=%s, want clean", o.Channel, o.Status)
		}
	}
	if want := []string{repairDev, repairDev + ":1"}; !slices.Equal(got, want) {
		t.Fatalf("channels=%v, want %v (:2 carries no MASTER)", got, want)
	}
}

// TestConfigRepairChannelOutcomes pins the per-channel failures: a channel
// of another device, a read fault, and an unknown device.
func TestConfigRepairChannelOutcomes(t *testing.T) {
	t.Parallel()
	b := singleChannelBackend(map[string]hmproto.ParameterData{"TX": intDesc("1", "10")}, map[string]any{"TX": 2})
	d, _, _ := repairFixture(t, b)
	out, err := d.RepairDeviceConfig(context.Background(), repairDev, []string{"OTHER:1", repairDev + ":1"}, true)
	if err != nil {
		t.Fatalf("RepairDeviceConfig: %v", err)
	}
	if len(out) != 2 || out[0].Status != interfaces.RepairFailed || !strings.Contains(out[0].Error, "does not belong") {
		t.Fatalf("foreign-device channel=%+v, want failed", out)
	}
	if out[1].Status != interfaces.RepairClean {
		t.Fatalf("a failing channel stopped the rest: %+v", out[1])
	}

	b.descErr = errors.New("unreachable")
	if o := repairOne(t, d, true); o.Status != interfaces.RepairFailed || !strings.Contains(o.Error, "unreachable") {
		t.Fatalf("read fault=%+v, want failed", o)
	}

	if _, err := d.RepairDeviceConfig(context.Background(), "UNKNOWN", nil, true); !errors.Is(err, hmerr.ErrDescriptionNotFound) {
		t.Fatalf("unknown device err=%v, want ErrDescriptionNotFound", err)
	}
}
