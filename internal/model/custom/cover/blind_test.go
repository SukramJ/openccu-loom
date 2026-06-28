// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package cover

import (
	"context"
	"maps"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/combined"
	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// putWriter records every SetValue + PutParamset call so the blind tests
// can assert the wire shape (LEVEL_COMBINED for HM blinds,
// COMBINED_PARAMETER for HmIP blinds — both delivered as a single SetValue).
type putWriter struct {
	mu    sync.Mutex
	calls []setCall
	puts  []map[string]any
}

type setCall struct {
	param hmenum.Parameter
	value any
}

func (p *putWriter) SetValue(_ context.Context, _ string, parameter hmenum.Parameter, value any, _ hmenum.CommandPriority) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, setCall{parameter, value})
	return nil
}

func (p *putWriter) PutParamset(_ context.Context, _ string, _ hmenum.ParamsetKey, values map[string]any, _ hmenum.CommandPriority) error {
	cp := make(map[string]any, len(values))
	maps.Copy(cp, values)
	p.mu.Lock()
	p.puts = append(p.puts, cp)
	p.mu.Unlock()
	return nil
}

// combinedCalls returns the LEVEL_COMBINED (HM) or COMBINED_PARAMETER (IP)
// SetValue calls in arrival order.
func (p *putWriter) combinedCalls() []setCall {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]setCall, 0, len(p.calls))
	for _, c := range p.calls {
		if c.param == hmenum.ParameterLevelCombined || c.param == hmenum.ParameterCombinedParameter {
			out = append(out, c)
		}
	}
	return out
}

func newBlindRig(t *testing.T, address string, w Writer, caps custom.CoverCapabilities, kind BlindKind) *Blind {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	ch := d.AddChannel(address, 1, "BLIND", hmenum.ParamsetKeyValues)
	mk := func(p hmenum.Parameter) *generic.Float {
		return generic.NewFloat(generic.Spec{
			Key: hmtypes.DataPointKey{
				ChannelAddress: address,
				ParamsetKey:    hmenum.ParamsetKeyValues,
				Parameter:      string(p),
			},
			Descriptor: hmproto.ParameterData{
				Type:       hmenum.ParameterTypeFloat,
				Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
			},
			Writer: w,
		})
	}
	level := mk(hmenum.ParameterLevel)
	level2 := mk(hmenum.ParameterLevel2)
	ch.Put(level)
	ch.Put(level2)
	return NewBlind(BlindConfig{Channel: ch, Writer: w, Capabilities: caps, Kind: kind})
}

func TestBlindHMSetTiltSendsLevelCombined(t *testing.T) {
	w := &putWriter{}
	b := newBlindRig(t, "VCU3560967:1", w, custom.CoverCapabilities{SupportsTilt: true}, BlindKindHM)
	if err := b.SetTilt(context.Background(), 0.45, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	cc := w.combinedCalls()
	if len(cc) != 1 {
		t.Fatalf("expected 1 LEVEL_COMBINED SetValue, got %d", len(cc))
	}
	if cc[0].param != hmenum.ParameterLevelCombined {
		t.Errorf("param=%v, want LEVEL_COMBINED", cc[0].param)
	}
	// level=0 → 0x00; tilt=0.45 → int(0.45*100*2)=90=0x5a → "0x00,0x5a"
	if got, ok := cc[0].value.(string); !ok || got != "0x00,0x5a" {
		t.Errorf("LEVEL_COMBINED=%v, want 0x00,0x5a", cc[0].value)
	}
}

func TestBlindCommandLockSerialisesConcurrentMoves(t *testing.T) {
	w := &slowWriter{delay: 30 * time.Millisecond}
	b := newBlindRig(t, "VCU3560967:1", w, custom.CoverCapabilities{SupportsTilt: true, SupportsStop: true}, BlindKindHM)

	var wg sync.WaitGroup
	wg.Add(2)
	start := time.Now()
	go func() {
		defer wg.Done()
		_ = b.SetTilt(context.Background(), 0.3, hmenum.CommandPriorityHigh)
	}()
	time.Sleep(2 * time.Millisecond)
	go func() {
		defer wg.Done()
		_ = b.SetTilt(context.Background(), 0.7, hmenum.CommandPriorityHigh)
	}()
	wg.Wait()
	elapsed := time.Since(start)
	if elapsed < 60*time.Millisecond {
		t.Errorf("two serialised LEVEL_COMBINED writes should take ≥ 60ms, got %v", elapsed)
	}
	if got := w.combinedCount(); got < 2 {
		t.Errorf("expected ≥ 2 LEVEL_COMBINED SetValue calls, got %d", got)
	}
}

func TestBlindStopFiresBeforeNewLevelWhenMoving(t *testing.T) {
	w := &putWriter{}
	b := newBlindRig(t, "VCU3560967:1", w, custom.CoverCapabilities{SupportsTilt: true, SupportsStop: true}, BlindKindHM)
	if err := b.SetTilt(context.Background(), 0.4, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	combined0 := len(w.combinedCalls())
	stops0 := w.stopCount()
	if err := b.SetTilt(context.Background(), 0.6, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if delta := w.stopCount() - stops0; delta != 1 {
		t.Errorf("expected exactly one STOP call between the two motions, got %d", delta)
	}
	if len(w.combinedCalls()) <= combined0 {
		t.Errorf("expected new LEVEL_COMBINED SetValue for second motion")
	}
}

// slowWriter is a writer whose SetValue + PutParamset both sleep so the
// concurrent-blind test can observe command-lock serialisation.
type slowWriter struct {
	mu       sync.Mutex
	combined int
	delay    time.Duration
}

func (p *slowWriter) SetValue(_ context.Context, _ string, parameter hmenum.Parameter, _ any, _ hmenum.CommandPriority) error {
	time.Sleep(p.delay)
	if parameter == hmenum.ParameterLevelCombined || parameter == hmenum.ParameterCombinedParameter {
		p.mu.Lock()
		p.combined++
		p.mu.Unlock()
	}
	return nil
}

func (p *slowWriter) PutParamset(_ context.Context, _ string, _ hmenum.ParamsetKey, _ map[string]any, _ hmenum.CommandPriority) error {
	time.Sleep(p.delay)
	return nil
}

func (p *slowWriter) combinedCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.combined
}

// stopCount reports how many STOP-parameter writes have been recorded
// on the underlying putWriter.
func (p *putWriter) stopCount() int {
	n := 0
	for _, c := range p.calls {
		if c.param == hmenum.ParameterStop {
			n++
		}
	}
	return n
}

// BlindKindIP SetCombined must use "L2=<tilt_pct>,L=<level_pct>" format with
// integer 0..100 values (tilt first).
func TestBlindIPCombinedFormat(t *testing.T) {
	w := &putWriter{}
	b := newBlindRig(t, "VCU9000001:1", w, custom.CoverCapabilities{}, BlindKindIP)
	if err := b.SetCombined(context.Background(), 0.5, 0.75, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	// The combined write goes through SetValue (not PutParamset) for COMBINED_PARAMETER.
	var combinedVal string
	for _, c := range w.calls {
		if c.param == hmenum.ParameterCombinedParameter {
			combinedVal = c.value.(string)
		}
	}
	// tilt=0.75 → 75, level=0.5 → 50 — tilt (L2) must come first.
	want := "L2=75,L=50"
	if combinedVal != want {
		t.Errorf("COMBINED_PARAMETER=%q, want %q", combinedVal, want)
	}
}

func TestBlindIPCombinedFormatInverted(t *testing.T) {
	w := &putWriter{}
	caps := custom.CoverCapabilities{InvertedControl: true}
	b := newBlindRig(t, "VCU9000001:1", w, caps, BlindKindIP)
	if err := b.SetCombined(context.Background(), 0.5, 0.75, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	var combinedVal string
	for _, c := range w.calls {
		if c.param == hmenum.ParameterCombinedParameter {
			combinedVal = c.value.(string)
		}
	}
	// Inverted: wire_level = 1-0.5 = 0.5, wire_tilt = 1-0.75 = 0.25
	// → "L2=25,L=50"
	want := "L2=25,L=50"
	if combinedVal != want {
		t.Errorf("COMBINED_PARAMETER inverted=%q, want %q", combinedVal, want)
	}
}

// Blind.OperationMode records CHANNEL_OPERATION_MODE updates.
func TestBlindOperationMode(t *testing.T) {
	w := &putWriter{}
	b := newBlindRig(t, "VCU9000002:1", w, custom.CoverCapabilities{}, BlindKindIP)
	_, ok := b.OperationMode()
	if ok {
		t.Fatal("fresh blind must not report OperationMode")
	}
	b.OnOperationMode("SHUTTER")
	mode, ok := b.OperationMode()
	if !ok {
		t.Fatal("OperationMode not observed after OnOperationMode")
	}
	if mode != "SHUTTER" {
		t.Errorf("OperationMode=%q, want SHUTTER", mode)
	}
}

func TestBlindHMSetPositionAfterTiltSendsLevelCombined(t *testing.T) {
	w := &putWriter{}
	b := newBlindRig(t, "VCU3560967:1", w, custom.CoverCapabilities{SupportsTilt: true}, BlindKindHM)
	if err := b.SetTilt(context.Background(), 0.5, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	w.mu.Lock()
	w.calls = nil
	w.mu.Unlock()
	if err := b.SetPosition(context.Background(), 1.0, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	cc := w.combinedCalls()
	if len(cc) != 1 {
		t.Fatalf("expected 1 LEVEL_COMBINED SetValue, got %d", len(cc))
	}
	if cc[0].param != hmenum.ParameterLevelCombined {
		t.Errorf("param=%v, want LEVEL_COMBINED", cc[0].param)
	}
	// level=1.0 → int(1.0*100*2)=200=0xc8; tilt staged=0.5 → int(0.5*100*2)=100=0x64 → "0xc8,0x64"
	if got, ok := cc[0].value.(string); !ok || got != "0xc8,0x64" {
		t.Errorf("LEVEL_COMBINED=%v, want 0xc8,0x64", cc[0].value)
	}
}

// TestBlindHMAttachesLevelCombinedDP verifies that NewBlind (HM kind, tilt)
// attaches a LevelCombined combined DP that appears in CombinedDataPoints().
func TestBlindHMAttachesLevelCombinedDP(t *testing.T) {
	t.Parallel()
	w := &putWriter{}
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "VCU3560967"})
	ch := d.AddChannel("VCU3560967:1", 1, "BLIND", hmenum.ParamsetKeyValues)
	mk := func(p hmenum.Parameter) *generic.Float {
		return generic.NewFloat(generic.Spec{
			Key: hmtypes.DataPointKey{
				ChannelAddress: "VCU3560967:1",
				ParamsetKey:    hmenum.ParamsetKeyValues,
				Parameter:      string(p),
			},
			Descriptor: hmproto.ParameterData{
				Type:       hmenum.ParameterTypeFloat,
				Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
			},
			Writer: w,
		})
	}
	ch.Put(mk(hmenum.ParameterLevel))
	ch.Put(mk(hmenum.ParameterLevel2))
	_ = NewBlind(BlindConfig{Channel: ch, Writer: w, Capabilities: custom.CoverCapabilities{SupportsTilt: true}, Kind: BlindKindHM})

	cdps := ch.CombinedDataPoints()
	var found bool
	for _, cdp := range cdps {
		if _, ok := cdp.(*combined.LevelCombined); ok {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected LevelCombined in CombinedDataPoints(), got %d DPs", len(cdps))
	}
}
