// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package cover

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

type stubWriter struct{ last any }

func (w *stubWriter) SetValue(_ context.Context, _ string, _ hmenum.Parameter, value any, _ hmenum.CommandPriority) error {
	w.last = value
	return nil
}

// newRig builds a channel with a LEVEL data point and constructs a
// Cover against it, matching the production assembly path.
func newRig(t *testing.T, address string, w Writer, caps custom.CoverCapabilities) (*Cover, *device.Channel, *generic.Float) {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	ch := d.AddChannel(address, 1, "BLIND", hmenum.ParamsetKeyValues)
	level := generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterLevel),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
		Writer: w,
	})
	ch.Put(level)
	c := New(Config{Channel: ch, Writer: w, Capabilities: caps})
	// Deferred GoTo*Percentage writes fire only via flushGoToWrites so
	// tests stay deterministic.
	neuterGoToTimers(&c.matterGoTo)
	return c, ch, level
}

func TestCoverSetPositionPlain(t *testing.T) {
	w := &stubWriter{}
	c, _, _ := newRig(t, "HmIP-BROLL:3", w, custom.CoverCapabilities{})
	if err := c.SetPosition(context.Background(), 0.25, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if f := w.last.(float64); f != 0.25 {
		t.Fatalf("wire=%v", f)
	}
	p, _ := c.Position()
	if p.Level() != 0.25 {
		t.Fatalf("position=%v", p.Level())
	}
}

func TestCoverSetPositionInverted(t *testing.T) {
	w := &stubWriter{}
	c, _, _ := newRig(t, "HmIP-BROLL:3", w, custom.CoverCapabilities{InvertedControl: true})
	if err := c.SetPosition(context.Background(), 0.25, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	// Wire value is inverted (0.75) but domain-level Position stays at 0.25.
	if f := w.last.(float64); f != 0.75 {
		t.Fatalf("wire=%v", f)
	}
	p, _ := c.Position()
	if p.Level() != 0.25 {
		t.Fatalf("domain position=%v", p.Level())
	}
}

func TestCoverOnLevelInverted(t *testing.T) {
	c, _, _ := newRig(t, "x", &stubWriter{}, custom.CoverCapabilities{InvertedControl: true})
	c.OnLevel(0.8)
	p, _ := c.Position()
	if p.Level() != 0.19999999999999996 && p.Level() != 0.2 {
		t.Fatalf("inverted OnLevel: %v", p.Level())
	}
}

func TestCoverStopNoOpWithoutCapability(t *testing.T) {
	w := &stubWriter{}
	c, _, _ := newRig(t, "x", w, custom.CoverCapabilities{})
	if err := c.Stop(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if w.last != nil {
		t.Fatal("Stop without capability should be a no-op")
	}
}

func TestCoverDirectionPlain(t *testing.T) {
	c, _, _ := newRig(t, "x", &stubWriter{}, custom.CoverCapabilities{})
	if c.IsOpening() || c.IsClosing() {
		t.Fatal("fresh cover must not report motion")
	}
	c.OnDirection(DirectionUp)
	if !c.IsOpening() || c.IsClosing() {
		t.Fatal("DirectionUp → opening")
	}
	c.OnDirection(DirectionDown)
	if c.IsOpening() || !c.IsClosing() {
		t.Fatal("DirectionDown → closing")
	}
	c.OnDirection(DirectionNone)
	if c.IsOpening() || c.IsClosing() {
		t.Fatal("DirectionNone → idle")
	}
}

func TestCoverDirectionInverted(t *testing.T) {
	c, _, _ := newRig(t, "x", &stubWriter{}, custom.CoverCapabilities{InvertedControl: true})
	c.OnDirection(DirectionUp)
	if c.IsOpening() {
		t.Fatal("inverted cover: DirectionUp must not be opening")
	}
	if !c.IsClosing() {
		t.Fatal("inverted cover: DirectionUp → closing")
	}
	c.OnDirection(DirectionDown)
	if !c.IsOpening() {
		t.Fatal("inverted cover: DirectionDown → opening")
	}
}

func TestCoverIsClosed(t *testing.T) {
	c, _, level := newRig(t, "x", &stubWriter{}, custom.CoverCapabilities{})
	if c.IsClosed() {
		t.Fatal("unobserved cover must not be closed")
	}
	level.OnEvent(0)
	if !c.IsClosed() {
		t.Fatal("level 0 → closed")
	}
	level.OnEvent(0.5)
	if c.IsClosed() {
		t.Fatal("level 0.5 → not closed")
	}
}

func TestCoverSharesLevelInstanceWithChannel(t *testing.T) {
	// Verifies the core invariant: the Cover's embedded *generic.Float
	// IS the channel's LEVEL pointer — no duplicate instance.
	c, ch, level := newRig(t, "x", &stubWriter{}, custom.CoverCapabilities{})
	chDP := ch.Parameter(hmenum.ParameterLevel)
	if chDP == nil {
		t.Fatal("channel must expose LEVEL")
	}
	if any(c.Float) != any(chDP) || any(c.Float) != any(level) {
		t.Fatalf("Cover.Float must be the same instance as channel parameter")
	}
}

// priorityWriter records the priority of every SetValue call so tests can
// assert the Stop-always-CRITICAL invariant.
type priorityWriter struct {
	stubWriter
	priorities []hmenum.CommandPriority
}

func (p *priorityWriter) SetValue(ctx context.Context, addr string, param hmenum.Parameter, value any, priority hmenum.CommandPriority) error {
	p.priorities = append(p.priorities, priority)
	return p.stubWriter.SetValue(ctx, addr, param, value, priority)
}

// Cover.Stop always uses CommandPriorityCritical regardless of what
// The caller passes — mirrors
// @bind_collector(priority=CommandPriority.CRITICAL) on CustomDpCover.stop.
func TestCoverStopAlwaysUsesCriticalPriority(t *testing.T) {
	w := &priorityWriter{}
	c, _, _ := newRig(t, "x", w, custom.CoverCapabilities{SupportsStop: true})
	// Pass a non-critical priority — it must be overridden internally.
	if err := c.Stop(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if len(w.priorities) != 1 {
		t.Fatalf("expected 1 SetValue call, got %d", len(w.priorities))
	}
	if got := w.priorities[0]; got != hmenum.CommandPriorityCritical {
		t.Errorf("Stop priority=%v, want CommandPriorityCritical", got)
	}
}
