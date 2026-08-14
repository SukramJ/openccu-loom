// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package registry

import (
	"reflect"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// fakeParamsetSink is a [ParamsetSink] recorder used to assert that
// [ParamsetRegistry] mutations mirror into the persistence sink with the
// normalised (and, for Add, patched) paramset.
type fakeParamsetSink struct {
	mu      sync.Mutex
	puts    []fakeParamsetPut
	deletes []fakeParamsetChannelDelete
}

type fakeParamsetPut struct {
	iface          hmtypes.WireInterfaceID
	channelAddress string
	psKey          hmenum.ParamsetKey
	ps             hmproto.Paramset
}

type fakeParamsetChannelDelete struct {
	iface          hmtypes.WireInterfaceID
	channelAddress string
}

func (f *fakeParamsetSink) PutParamset(iface hmtypes.WireInterfaceID, channelAddress string, psKey hmenum.ParamsetKey, ps hmproto.Paramset) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.puts = append(f.puts, fakeParamsetPut{iface: iface, channelAddress: channelAddress, psKey: psKey, ps: ps})
}

func (f *fakeParamsetSink) DeleteChannelParamsets(iface hmtypes.WireInterfaceID, channelAddress string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deletes = append(f.deletes, fakeParamsetChannelDelete{iface: iface, channelAddress: channelAddress})
}

func (f *fakeParamsetSink) putCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.puts)
}

func (f *fakeParamsetSink) deleteCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.deletes)
}

func TestParamsetRegistryAddFiresSinkWithNormalisedParamset(t *testing.T) {
	r := NewParamsetRegistry()
	sink := &fakeParamsetSink{}
	r.SetSink(sink)

	raw := hmproto.Paramset{
		"LEVEL": {Type: hmenum.ParameterTypeFloat, Unit: "  % "},
	}
	r.Add(wireHmIPRF, "VCU1:1", hmenum.ParamsetKeyValues, raw, "HmIP-PS")

	if got := sink.putCount(); got != 1 {
		t.Fatalf("PutParamset called %d times, want 1", got)
	}
	sink.mu.Lock()
	got := sink.puts[0]
	sink.mu.Unlock()
	if got.iface != wireHmIPRF || got.channelAddress != "VCU1:1" || got.psKey != hmenum.ParamsetKeyValues {
		t.Fatalf("sink call=%+v want {HmIP-RF VCU1:1 VALUES ...}", got)
	}
	want := hmproto.NormalizeParamset(raw)
	if !reflect.DeepEqual(got.ps, want) {
		t.Errorf("sink ps=%+v want normalised %+v", got.ps, want)
	}
}

func TestParamsetRegistryPutFiresSinkWithNormalisedParamset(t *testing.T) {
	r := NewParamsetRegistry()
	sink := &fakeParamsetSink{}
	r.SetSink(sink)

	raw := hmproto.Paramset{
		"STATE": {Type: hmenum.ParameterTypeBool, Unit: " bool "},
	}
	r.Put(wireHmIPRF, "VCU2:1", hmenum.ParamsetKeyMaster, raw)

	if got := sink.putCount(); got != 1 {
		t.Fatalf("PutParamset called %d times, want 1", got)
	}
	sink.mu.Lock()
	got := sink.puts[0]
	sink.mu.Unlock()
	want := hmproto.NormalizeParamset(raw)
	if !reflect.DeepEqual(got.ps, want) {
		t.Errorf("sink ps=%+v want normalised %+v", got.ps, want)
	}
}

func TestParamsetRegistryDeleteChannelFiresSinkExactlyOnce(t *testing.T) {
	r := NewParamsetRegistry()
	r.Put(wireHmIPRF, "CH:1", hmenum.ParamsetKeyValues, hmproto.Paramset{"A": {}})
	r.Put(wireHmIPRF, "CH:1", hmenum.ParamsetKeyMaster, hmproto.Paramset{"B": {}})
	sink := &fakeParamsetSink{}
	r.SetSink(sink)

	r.DeleteChannel(wireHmIPRF, "CH:1")

	if got := sink.deleteCount(); got != 1 {
		t.Fatalf("DeleteChannelParamsets called %d times, want exactly 1 despite 2 stored paramset keys", got)
	}
	sink.mu.Lock()
	got := sink.deletes[0]
	sink.mu.Unlock()
	if got.iface != wireHmIPRF || got.channelAddress != "CH:1" {
		t.Errorf("delete call=%+v want {HmIP-RF CH:1}", got)
	}
}

func TestParamsetRegistryDeleteSingleFiresNoSinkCall(t *testing.T) {
	r := NewParamsetRegistry()
	r.Put(wireHmIPRF, "CH:2", hmenum.ParamsetKeyValues, hmproto.Paramset{"A": {}})
	sink := &fakeParamsetSink{}
	r.SetSink(sink)

	if !r.Delete(wireHmIPRF, "CH:2", hmenum.ParamsetKeyValues) {
		t.Fatal("Delete must report true for an existing entry")
	}
	if got := sink.putCount(); got != 0 {
		t.Fatalf("PutParamset called %d times, want 0", got)
	}
	if got := sink.deleteCount(); got != 0 {
		t.Fatalf("single-key Delete must not invoke DeleteChannelParamsets, called %d times", got)
	}
}

func TestParamsetRegistrySetSinkNilDetaches(t *testing.T) {
	r := NewParamsetRegistry()
	sink := &fakeParamsetSink{}
	r.SetSink(sink)

	r.Put(wireHmIPRF, "CH:3", hmenum.ParamsetKeyValues, hmproto.Paramset{"A": {}})
	if got := sink.putCount(); got != 1 {
		t.Fatalf("PutParamset called %d times before detach, want 1", got)
	}

	r.SetSink(nil)
	r.Put(wireHmIPRF, "CH:4", hmenum.ParamsetKeyValues, hmproto.Paramset{"B": {}})
	r.DeleteChannel(wireHmIPRF, "CH:3")

	if got := sink.putCount(); got != 1 {
		t.Fatalf("PutParamset called %d times after SetSink(nil), want unchanged 1", got)
	}
	if got := sink.deleteCount(); got != 0 {
		t.Fatalf("DeleteChannelParamsets called %d times after SetSink(nil), want 0", got)
	}
}
