// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestHydratedDataPointWriterEnforcesChannelLock pins the write path a data
// point captures at hydration time.
//
// A data point keeps the writer it was constructed with, and every custom data
// point composed on top of it (light, switch, valve) captures that same
// writer instead of going through Channel.Set. Built with the raw bound
// writer, those commands reached the CCU while the channel advertised an
// operator lock — the lock only ever guarded Channel.Set / SetMany. The
// pipeline therefore has to hand out the channel's own lock-enforcing writer.
func TestHydratedDataPointWriterEnforcesChannelLock(t *testing.T) {
	t.Parallel()

	c, _ := central.New(central.Config{Name: "ccu-01"})
	p := NewDevicePipeline(c).WithVisibility(newProductionVisibilityGate())

	b := newHydratingBackend()
	w := client.NewValueWriter()
	w.Register("ccu-01", "HmIP-RF", b)

	vw := &fakeWriter{}
	if err := p.IngestFromBackend(
		context.Background(), "HmIP-RF", hmenum.InterfaceHmIPRF,
		b, vw, nil, slog.Default(),
	); err != nil {
		t.Fatalf("IngestFromBackend: %v", err)
	}

	dev, ok := c.ModelRegistry.Get("0001ABCD")
	if !ok {
		t.Fatal("device not in registry after IngestFromBackend")
	}
	ch := dev.Channel("0001ABCD:1")
	if ch == nil {
		t.Fatal("channel 0001ABCD:1 not found")
	}
	dp, isFloat := ch.Parameter(hmenum.ParameterLevel).(*generic.Float)
	if !isFloat {
		t.Fatalf("LEVEL data point has type %T, want *generic.Float", ch.Parameter(hmenum.ParameterLevel))
	}

	// Unlocked: the captured writer dispatches.
	if err := dp.Set(context.Background(), 0.5, hmenum.CommandPriorityLow); err != nil {
		t.Fatalf("write on an unlocked channel: %v", err)
	}
	before := vw.calls.Load()
	if before == 0 {
		t.Fatal("write on an unlocked channel never reached the wire")
	}

	ch.SetOperatorFlags(false, true)
	err := dp.Set(context.Background(), 0.75, hmenum.CommandPriorityLow)
	if !errors.Is(err, device.ErrChannelOperationLocked) {
		t.Fatalf("write on a locked channel: got %v, want ErrChannelOperationLocked", err)
	}
	if after := vw.calls.Load(); after != before {
		t.Errorf("locked channel still dispatched %d wire writes", after-before)
	}
}
