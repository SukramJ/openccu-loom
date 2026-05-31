// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package switchdev

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// newTestSwitch builds a minimal channel, registers a STATE *generic.Switch
// wire-DP on it (carrying w as Writer and centralName as CentralName), and
// calls New(ch). It is the canonical test-fixture factory that replaces the
// old New(addr, centralName, w) three-argument form.
func newTestSwitch(t *testing.T, addr, centralName string, w custom.Writer) *Switch {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "VCU0001"})
	ch := d.AddChannel(addr, 4, "SWITCH", hmenum.ParamsetKeyValues)
	dp := generic.NewSwitch(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: addr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterState),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
		CentralName: centralName,
		Writer:      w,
	})
	ch.Put(dp)
	return New(ch)
}

type stubWriter struct {
	lastAddr string
	lastParm hmenum.Parameter
	lastVal  any
}

func (w *stubWriter) SetValue(_ context.Context, address string, parameter hmenum.Parameter, value any, _ hmenum.CommandPriority) error {
	w.lastAddr = address
	w.lastParm = parameter
	w.lastVal = value
	return nil
}

type putWriter struct {
	stubWriter
	puts []map[string]any
}

func (p *putWriter) PutParamset(_ context.Context, _ string, _ hmenum.ParamsetKey, values map[string]any, _ hmenum.CommandPriority) error {
	cp := make(map[string]any, len(values))
	for k, v := range values {
		cp[k] = v
	}
	p.puts = append(p.puts, cp)
	return nil
}

func TestSwitchTurnOnForAtomicPutParamset(t *testing.T) {
	w := &putWriter{}
	s := newTestSwitch(t, "VCU2128127:4", "", w)
	if err := s.TurnOnFor(context.Background(), 60*time.Second, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if len(w.puts) != 1 {
		t.Fatalf("expected 1 put_paramset, got %d", len(w.puts))
	}
	got := w.puts[0]
	if got[string(hmenum.ParameterOnTime)].(float64) != 60 {
		t.Errorf("ON_TIME=%v", got[string(hmenum.ParameterOnTime)])
	}
	if got[string(hmenum.ParameterState)] != true {
		t.Errorf("STATE=%v", got[string(hmenum.ParameterState)])
	}
}

func TestSwitchSetTimerThenTurnOnAtomicPutParamset(t *testing.T) {
	w := &putWriter{}
	s := newTestSwitch(t, "VCU2128127:4", "", w)
	s.SetTimerOnTime(35400 * time.Millisecond) // 35.4s
	if err := s.TurnOn(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if len(w.puts) != 1 {
		t.Fatalf("expected 1 put_paramset, got %d", len(w.puts))
	}
	got := w.puts[0]
	if v := got[string(hmenum.ParameterOnTime)].(float64); v < 35.3 || v > 35.5 {
		t.Errorf("ON_TIME=%v", v)
	}
}

func TestSwitchRoundTrip(t *testing.T) {
	w := &stubWriter{}
	s := newTestSwitch(t, "HmIP-PS:3", "", w)
	if err := s.Set(context.Background(), true, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if w.lastAddr != "HmIP-PS:3" || w.lastParm != hmenum.ParameterState || w.lastVal != true {
		t.Fatalf("writer saw %+v", w)
	}
	on, ok := s.IsOn()
	if !on || !ok {
		t.Fatalf("after Set IsOn=%v ok=%v", on, ok)
	}
	s.OnState(false)
	on, ok = s.IsOn()
	if on || !ok {
		t.Fatalf("after OnState IsOn=%v ok=%v", on, ok)
	}
}
