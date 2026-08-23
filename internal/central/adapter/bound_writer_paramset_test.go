// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	switchcdp "github.com/SukramJ/openccu-loom/internal/model/custom/switch"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// recordingValueWriter counts what reaches the wire, in order.
type recordingValueWriter struct {
	calls []string
}

func (w *recordingValueWriter) SetValue(
	_ context.Context, _, _, _ string, parameter hmenum.Parameter, _ any, _ hmenum.CommandPriority,
) error {
	w.calls = append(w.calls, "setValue:"+string(parameter))
	return nil
}

func (w *recordingValueWriter) PutParamset(
	_ context.Context, _, _, _ string, _ hmenum.ParamsetKey, values map[string]any, _ hmenum.CommandPriority,
) error {
	names := make([]string, 0, len(values))
	for k := range values {
		names = append(names, k)
	}
	// Order within one paramset write is irrelevant — what matters is
	// that it is one call.
	w.calls = append(w.calls, "putParamset("+sortedParamNames(names)+")")
	return nil
}

// setValueOnlyWriter is a writer without the paramset capability, the
// shape the port has always had.
type setValueOnlyWriter struct{ calls []string }

func (w *setValueOnlyWriter) SetValue(
	_ context.Context, _, _, _ string, parameter hmenum.Parameter, _ any, _ hmenum.CommandPriority,
) error {
	w.calls = append(w.calls, "setValue:"+string(parameter))
	return nil
}

func sortedParamNames(in []string) string {
	sort.Strings(in)
	return strings.Join(in, ",")
}

func switchWithWriter(t *testing.T, w generic.Writer) *generic.Switch {
	t.Helper()
	return generic.NewSwitch(generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: "00021BE9957782:4",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterState),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
		Writer: w,
	})
}

// TestBoundWriterCarriesTheParamsetCapability is the regression guard
// for a defect that only the wire showed.
//
// A bounded switch-on has to reach the device as ONE write carrying
// both ON_TIME and STATE — the alarm concept's S1 invariant says so in
// as many words, because two writes are two radio transmissions out of
// the same duty-cycle budget a stop command later needs. generic.Switch
// implements that atomic path and selects it by asking whether its
// writer can write a paramset.
//
// The writer the device pipeline hands to every data point could not:
// it exposed SetValue alone, so the atomic branch was unreachable in
// production and every bounded switch-on split into two setValue calls.
// Verified live against an HMIP-PS before this test existed.
func TestBoundWriterCarriesTheParamsetCapability(t *testing.T) {
	t.Parallel()

	rec := &recordingValueWriter{}
	sw := switchWithWriter(t, newBoundWriter("ccu", "HmIP-RF", rec))

	if err := sw.TurnOnWithTimer(context.Background(), 3*time.Second, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("TurnOnWithTimer: %v", err)
	}

	if len(rec.calls) != 1 {
		t.Fatalf("bounded switch-on produced %d wire call(s) %v, want exactly one carrying both "+
			"parameters — two calls spend twice the duty-cycle budget and leave a window in which the "+
			"device is on without its auto-off", len(rec.calls), rec.calls)
	}
	if rec.calls[0] != "putParamset(ON_TIME,STATE)" {
		t.Errorf("wire call = %q, want a single paramset write carrying ON_TIME and STATE", rec.calls[0])
	}
}

// TestBoundWriterFallsBackWhenTheWriterCannotParamset pins the other
// half: a writer without the capability must keep the safe two-call
// order rather than failing the command.
//
// The order is what makes it safe — ON_TIME first, so a daemon that
// dies between the two leaves the device off rather than on without a
// bound.
func TestBoundWriterFallsBackWhenTheWriterCannotParamset(t *testing.T) {
	t.Parallel()

	rec := &setValueOnlyWriter{}
	sw := switchWithWriter(t, newBoundWriter("ccu", "HmIP-RF", rec))

	if err := sw.TurnOnWithTimer(context.Background(), 3*time.Second, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("TurnOnWithTimer: %v", err)
	}
	want := []string{"setValue:ON_TIME", "setValue:STATE"}
	if len(rec.calls) != len(want) {
		t.Fatalf("got %v, want the two-call fallback %v", rec.calls, want)
	}
	for i := range want {
		if rec.calls[i] != want[i] {
			t.Errorf("call %d = %q, want %q — ON_TIME has to precede STATE so an interrupted sequence "+
				"leaves the device off", i, rec.calls[i], want[i])
		}
	}
}

// TestCollectorBundlesTheBoundedSwitchOn pins the mechanism the project
// actually relies on, rather than the special case underneath it.
//
// The daemon has a CallParameterCollector for exactly this: staged
// writes to one (channel, paramset) leave as a single PutParamset, a
// lone write as a SetValue. Switch.TurnOnFor opens one — but ON_TIME
// used to be written straight past it, so the collector received only
// STATE and the pair still cost two radio transmissions. On the wire,
// against a real HMIP-PS, that showed as two setValue calls 5 ms apart.
//
// Driving TurnOnFor (the path the alarm output manager takes) rather
// than the lower-level timer call is what makes this test see the
// collector at all.
func TestCollectorBundlesTheBoundedSwitchOn(t *testing.T) {
	t.Parallel()

	rec := &recordingValueWriter{}
	dev := switchcdp.New(switchChannelFor(t, newBoundWriter("ccu", "HmIP-RF", rec)), custom.RebasedChannelGroupConfig{})
	if dev == nil {
		t.Fatal("switch custom data point was not constructed")
	}

	if err := dev.TurnOnFor(context.Background(), 3*time.Second, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("TurnOnFor: %v", err)
	}

	if len(rec.calls) != 1 {
		t.Fatalf("bounded switch-on produced %v, want a single bundled write — the collector exists "+
			"precisely so the auto-off and the switch-on share one transmission", rec.calls)
	}
	if rec.calls[0] != "putParamset(ON_TIME,STATE)" {
		t.Errorf("wire call = %q, want putParamset(ON_TIME,STATE)", rec.calls[0])
	}
}

// switchChannelFor builds a channel carrying the STATE data point a
// switch custom DP needs, wired to w.
func switchChannelFor(t *testing.T, w generic.Writer) *device.Channel {
	t.Helper()
	dev := device.New(device.Config{
		InterfaceID: "HmIP-RF", Interface: hmenum.InterfaceHmIPRF,
		Address: "00021BE9957782", Model: "HMIP-PS",
	})
	ch := dev.AddChannel("00021BE9957782:4", 4, "SWITCH_VIRTUAL_RECEIVER", hmenum.ParamsetKeyValues)
	ch.Put(generic.NewSwitch(generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterState),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
		Writer: w,
	}))
	return ch
}
