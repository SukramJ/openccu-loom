// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"context"
	"sort"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// relevantInitCandidateParameters are the channel-0 parameters this guard
// offers the sweep. Every member of [hmenum.RelevantInitParameters] must be
// among them, plus at least one channel-0 parameter that is deliberately not
// a member — LOW_BAT is device-level (it is in
// [hmenum.DeviceChannel0Parameters]) but is not part of the bootstrap set, so
// a consumer that walked the whole device-level set instead of the shared
// init set would show up as an extra load.
var relevantInitCandidateParameters = append(
	append([]hmenum.Parameter{}, hmenum.RelevantInitParameters...),
	hmenum.ParameterLowBat,
)

// relevantInitProbeLoader answers a VALUES paramset fetch with an empty
// response and counts the fetches. Empty on purpose: a populated response
// would bulk-fill the channel's sibling data points (see
// Device.runLoadValuesParamset), which marks them observed and hides from the
// guard whether the consumer asked for them at all.
type relevantInitProbeLoader struct {
	calls atomic.Int32
}

func (l *relevantInitProbeLoader) GetValue(context.Context, string, hmenum.Parameter) (any, error) {
	l.calls.Add(1)
	return nil, nil
}

func (l *relevantInitProbeLoader) GetParamset(context.Context, string, hmenum.ParamsetKey) (map[string]any, error) {
	l.calls.Add(1)
	return nil, nil
}

// relevantInitProbeDevice builds a device whose channel 0 carries exactly one
// data point, for parameter p. One parameter per device is what makes the
// observation unambiguous: the value loader only learns the channel address,
// never the parameter, so a device that carried several candidates could not
// report which of them the consumer actually asked for.
//
// The data point is marked NoCreate so it is invisible. That confines it to
// the sweep's channel-0 pass: the broad third pass gates on the visibility
// flag and would otherwise load every readable data point, member or not, and
// mask the very divergence this guard exists to catch.
func relevantInitProbeDevice(t *testing.T, unit *central.Unit, wireID, address string, p hmenum.Parameter) *relevantInitProbeLoader {
	t.Helper()
	d := device.New(device.Config{
		InterfaceID: wireID,
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     address,
	})
	ch0 := d.AddChannel(address+":0", 0, "MAINTENANCE", hmenum.ParamsetKeyValues)
	ch0.Put(generic.NewDataPoint[bool](generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    wireID,
			ChannelAddress: address + ":0",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(p),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
		Usage: hmenum.DataPointUsageNoCreate,
	}))
	loader := &relevantInitProbeLoader{}
	d.SetValueLoader(loader)
	unit.ModelRegistry.Put(d)
	return loader
}

// TestUnobservedSweepLoadsTheSharedRelevantInitSet pins the reconciler's
// unobserved sweep to [hmenum.RelevantInitParameters], the single definition
// of the channel-0 bootstrap set that the CCU/CUxD/hotplug seed also walks.
//
// The set used to be declared twice — once in the adapter and once, dead, in
// the device model — so a maintainer adding a member could edit the copy no
// production path reads. This guard measures the effect on the wire (which
// channel-0 parameters does the sweep actually fetch?) against the shared
// definition, so re-introducing a private list in the sweep turns it red
// while widening the shared set keeps it green.
func TestUnobservedSweepLoadsTheSharedRelevantInitSet(t *testing.T) {
	t.Parallel()
	if len(hmenum.RelevantInitParameters) == 0 {
		t.Fatal("hmenum.RelevantInitParameters is empty: the guard would assert nothing")
	}
	reg := central.NewRegistry()
	unit, err := central.New(central.Config{Name: "relevant-init-shared-set"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	if err := reg.Register(unit); err != nil {
		t.Fatalf("register: %v", err)
	}
	wireID := adapter.WireInterfaceID(unit.Name(), hmenum.InterfaceHmIPRF)

	loaders := make(map[hmenum.Parameter]*relevantInitProbeLoader, len(relevantInitCandidateParameters))
	for i, p := range relevantInitCandidateParameters {
		address := string(rune('A'+i)) + "000RELINIT"
		loaders[p] = relevantInitProbeDevice(t, unit, wireID, address, p)
	}

	adapter.NewUnobservedSweep(reg, nil).SweepUnobserved(context.Background())

	var fetched []string
	for p, l := range loaders {
		got := l.calls.Load()
		switch got {
		case 0:
			continue
		case 1:
			fetched = append(fetched, string(p))
		default:
			t.Errorf("parameter %s was fetched %d times, want at most 1", p, got)
		}
	}
	want := make([]string, 0, len(hmenum.RelevantInitParameters))
	for _, p := range hmenum.RelevantInitParameters {
		want = append(want, string(p))
	}
	sort.Strings(fetched)
	sort.Strings(want)
	if len(fetched) != len(want) {
		t.Fatalf("sweep fetched channel-0 parameters %v, want %v (hmenum.RelevantInitParameters): the sweep no longer walks the shared set", fetched, want)
	}
	for i := range want {
		if fetched[i] != want[i] {
			t.Fatalf("sweep fetched channel-0 parameters %v, want %v (hmenum.RelevantInitParameters): the sweep no longer walks the shared set", fetched, want)
		}
	}
}
