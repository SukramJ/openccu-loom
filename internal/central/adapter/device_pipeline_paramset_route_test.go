// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// routedParamsetWriter is a ValueWriter with the paramset capability
// that records the routing tuple of every call, so a test can tell a
// write that went through the write-policy layer from one that went
// straight to the backend.
type routedParamsetWriter struct {
	mu    sync.Mutex
	puts  []routedParamsetCall
	sets  int
	fails error
}

// routedParamsetCall is one recorded paramset write.
type routedParamsetCall struct {
	centralName, iface, channelAddress string
	paramsetKey                        hmenum.ParamsetKey
	values                             map[string]any
}

func (w *routedParamsetWriter) SetValue(
	_ context.Context, _, _, _ string, _ hmenum.Parameter, _ any, _ hmenum.CommandPriority,
) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.sets++
	return w.fails
}

func (w *routedParamsetWriter) PutParamset(
	_ context.Context, centralName, iface, channelAddress string,
	paramsetKey hmenum.ParamsetKey, values map[string]any, _ hmenum.CommandPriority,
) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.puts = append(w.puts, routedParamsetCall{
		centralName: centralName, iface: iface, channelAddress: channelAddress,
		paramsetKey: paramsetKey, values: values,
	})
	return w.fails
}

func (w *routedParamsetWriter) putCalls() []routedParamsetCall {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]routedParamsetCall, len(w.puts))
	copy(out, w.puts)
	return out
}

// hydratedChannel runs the production hydration path with vw as the
// value writer and returns the channel whose data points captured
// their write path from it.
func hydratedChannel(t *testing.T, vw ValueWriter, backend *paramsetFakeOps) *device.Channel {
	t.Helper()
	c, _ := central.New(central.Config{Name: "ccu-01"})
	p := NewDevicePipeline(c).WithVisibility(newProductionVisibilityGate())
	if err := p.IngestFromBackend(
		context.Background(), "HmIP-RF", hmenum.InterfaceHmIPRF,
		backend, vw, nil, slog.Default(),
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
	return ch
}

// TestHydratedParamsetWriteRoutesThroughTheValueWriter pins the second
// half of the write path a data point captures.
//
// Every data point of a channel writes through the channel's own
// writer, and a data point whose semantics need one atomic message (a
// bounded switch-on) reaches it as a paramset write. That write has to
// travel the same route a single-value write does — through the
// configured value writer, with its (central, interface) resolution and
// its in-flight staging — and not shortcut to the raw backend the
// pipeline happened to hydrate from.
func TestHydratedParamsetWriteRoutesThroughTheValueWriter(t *testing.T) {
	t.Parallel()

	var backendPuts int
	b := newHydratingBackend()
	b.putParamsetFn = func(_ context.Context, _ string, _ hmenum.ParamsetKey, _ map[string]any) error {
		backendPuts++
		return nil
	}
	vw := &routedParamsetWriter{}
	ch := hydratedChannel(t, vw, b)

	w := ch.Writer()
	if w == nil {
		t.Fatal("hydration installed no channel writer")
	}
	values := map[string]any{"ON_TIME": 3.0, "STATE": true}
	if err := w.PutParamset(
		context.Background(), ch.Address, hmenum.ParamsetKeyValues, values, hmenum.CommandPriorityHigh,
	); err != nil {
		t.Fatalf("PutParamset: %v", err)
	}

	calls := vw.putCalls()
	if len(calls) != 1 {
		t.Fatalf("value writer saw %d paramset write(s), want 1 — the write-policy layer is off the "+
			"data-point path when the adapter goes to the backend directly", len(calls))
	}
	got := calls[0]
	if got.centralName != "ccu-01" || got.iface != "HmIP-RF" || got.channelAddress != ch.Address {
		t.Errorf("routing tuple = (%q,%q,%q), want (ccu-01,HmIP-RF,%s)",
			got.centralName, got.iface, got.channelAddress, ch.Address)
	}
	if got.paramsetKey != hmenum.ParamsetKeyValues || len(got.values) != len(values) {
		t.Errorf("paramset write = key %q values %v, want VALUES %v", got.paramsetKey, got.values, values)
	}
	if backendPuts != 0 {
		t.Errorf("backend received %d direct paramset write(s), want 0", backendPuts)
	}
}

// TestHydratedParamsetWriteHonorsTheChannelLock keeps the lock and the
// routing honest about each other: the operator lock gates a VALUES
// paramset write, leaves a MASTER one alone (channel configuration
// stays editable), and the unlocked write still reaches the wire.
func TestHydratedParamsetWriteHonorsTheChannelLock(t *testing.T) {
	t.Parallel()

	vw := &routedParamsetWriter{}
	ch := hydratedChannel(t, vw, newHydratingBackend())
	w := ch.Writer()

	ch.SetOperatorFlags(false, true)
	err := w.PutParamset(
		context.Background(), ch.Address, hmenum.ParamsetKeyValues,
		map[string]any{"STATE": true}, hmenum.CommandPriorityHigh,
	)
	if !errors.Is(err, device.ErrChannelOperationLocked) {
		t.Fatalf("VALUES paramset write on a locked channel: got %v, want ErrChannelOperationLocked", err)
	}
	if got := len(vw.putCalls()); got != 0 {
		t.Fatalf("locked channel dispatched %d paramset write(s)", got)
	}

	if err := w.PutParamset(
		context.Background(), ch.Address, hmenum.ParamsetKeyMaster,
		map[string]any{"AES_ACTIVE": false}, hmenum.CommandPriorityHigh,
	); err != nil {
		t.Fatalf("MASTER paramset write on a locked channel: %v, want it to pass", err)
	}

	ch.SetOperatorFlags(false, false)
	if err := w.PutParamset(
		context.Background(), ch.Address, hmenum.ParamsetKeyValues,
		map[string]any{"STATE": true}, hmenum.CommandPriorityHigh,
	); err != nil {
		t.Fatalf("VALUES paramset write after unlocking: %v", err)
	}
	if got := len(vw.putCalls()); got != 2 {
		t.Errorf("value writer saw %d paramset write(s), want 2 (MASTER while locked, VALUES after "+
			"unlocking) — the lock has to gate the write, not drop it", got)
	}
}
