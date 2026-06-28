// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package light_test

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/light"
	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// fakeColorTemperatureWriter is a test double for ColorTemperatureWriter that
// records the last call arguments and an optional injected error.
type fakeColorTemperatureWriter struct {
	calls        int
	lastMireds   uint16
	lastPriority hmenum.CommandPriority
	injectErr    error
}

func (f *fakeColorTemperatureWriter) SetColorTemperatureMireds(_ context.Context, mireds uint16, priority hmenum.CommandPriority) error {
	f.calls++
	f.lastMireds = mireds
	f.lastPriority = priority
	return f.injectErr
}

// currentMireds reads the CurrentColorTemperatureMireds attribute from the
// server and fails the test if the attribute is absent or has the wrong type.
func currentMireds(t *testing.T, srv *light.ColorControlServer) uint16 {
	t.Helper()
	v, ok := srv.MatterRead(wire.ColorCtrlAttrColorTemperatureMireds)
	if !ok {
		t.Fatal("MatterRead(ColorTemperatureMireds) returned ok=false")
	}
	m, ok := v.(uint16)
	if !ok {
		t.Fatalf("ColorTemperatureMireds expected uint16, got %T", v)
	}
	return m
}

// TestColorControl_WriteThrough_WriterCalledOnce verifies that a successful
// MoveToColorTemperature with a wired writer calls the writer exactly once with
// the exact (in-range) mired value and the supplied priority, returns (nil, nil),
// and updates the reported state.
func TestColorControl_WriteThrough_WriterCalledOnce(t *testing.T) {
	t.Parallel()
	cfg := light.DefaultColorControlServerConfig() // min=153 max=500, initial=370
	srv := light.NewColorControlServer(cfg)
	w := &fakeColorTemperatureWriter{}
	srv.SetWriter(w)

	const target uint16 = 300
	ret, err := srv.MatterInvoke(context.Background(), wire.ColorCtrlCmdMoveToColorTemperature,
		wire.MoveToColorTemperatureRequest{ColorTemperatureMireds: target}, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("MatterInvoke returned error: %v", err)
	}
	if ret != nil {
		t.Errorf("MatterInvoke returned non-nil response: %v", ret)
	}
	if w.calls != 1 {
		t.Errorf("writer called %d times, want 1", w.calls)
	}
	if w.lastMireds != target {
		t.Errorf("writer received mireds=%d, want %d", w.lastMireds, target)
	}
	if w.lastPriority != hmenum.CommandPriorityHigh {
		t.Errorf("writer received priority=%v, want CommandPriorityHigh", w.lastPriority)
	}
	if got := currentMireds(t, srv); got != target {
		t.Errorf("reported state = %d after write-through, want %d", got, target)
	}
}

// TestColorControl_WriteThrough_WriterError verifies that when the writer
// returns an error the command returns a wrapped error and the in-process
// CurrentColorTemperatureMireds is left unchanged (old value).
func TestColorControl_WriteThrough_WriterError(t *testing.T) {
	t.Parallel()
	cfg := light.DefaultColorControlServerConfig() // initial=370
	srv := light.NewColorControlServer(cfg)
	sentinel := errors.New("device rejected write")
	w := &fakeColorTemperatureWriter{injectErr: sentinel}
	srv.SetWriter(w)

	const initialMireds uint16 = 370 // matches DefaultColorControlServerConfig
	before := currentMireds(t, srv)
	if before != initialMireds {
		t.Fatalf("pre-condition failed: initial mireds = %d, want %d", before, initialMireds)
	}

	_, err := srv.MatterInvoke(context.Background(), wire.ColorCtrlCmdMoveToColorTemperature,
		wire.MoveToColorTemperatureRequest{ColorTemperatureMireds: 300}, hmenum.CommandPriorityHigh)

	if err == nil {
		t.Fatal("MatterInvoke expected error when writer fails, got nil")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("error = %v, want wrapped sentinel %v", err, sentinel)
	}
	// State must be unchanged.
	if after := currentMireds(t, srv); after != before {
		t.Errorf("reported state changed from %d to %d after failed write-through; must stay unchanged", before, after)
	}
}

// TestColorControl_WriteThrough_Clamping verifies that values outside the
// configured [MinMireds, MaxMireds] range are cropped before being forwarded
// to the writer and stored as state.
func TestColorControl_WriteThrough_Clamping(t *testing.T) {
	t.Parallel()
	cfg := light.DefaultColorControlServerConfig() // min=153 max=500

	cases := []struct {
		name       string
		target     uint16
		wantMireds uint16
	}{
		{"above max → clamped to MaxMireds", 999, 500},
		{"below min → clamped to MinMireds", 50, 153},
		{"at max boundary → exact", 500, 500},
		{"at min boundary → exact", 153, 153},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv := light.NewColorControlServer(cfg)
			w := &fakeColorTemperatureWriter{}
			srv.SetWriter(w)

			_, err := srv.MatterInvoke(context.Background(), wire.ColorCtrlCmdMoveToColorTemperature,
				wire.MoveToColorTemperatureRequest{ColorTemperatureMireds: tc.target}, hmenum.CommandPriorityHigh)
			if err != nil {
				t.Fatalf("MatterInvoke returned error: %v", err)
			}
			// Writer must receive clamped value.
			if w.calls != 1 {
				t.Fatalf("writer called %d times, want 1", w.calls)
			}
			if w.lastMireds != tc.wantMireds {
				t.Errorf("writer received mireds=%d, want %d (input=%d)", w.lastMireds, tc.wantMireds, tc.target)
			}
			// Reported state must also be clamped.
			if got := currentMireds(t, srv); got != tc.wantMireds {
				t.Errorf("reported state = %d, want %d (input=%d)", got, tc.wantMireds, tc.target)
			}
		})
	}
}

// TestColorControl_WriteThrough_NoWriter verifies that a server without a
// wired writer still updates its in-process state, does not panic, and returns
// (nil, nil) on a MoveToColorTemperature command.
func TestColorControl_WriteThrough_NoWriter(t *testing.T) {
	t.Parallel()
	cfg := light.DefaultColorControlServerConfig()
	srv := light.NewColorControlServer(cfg)
	// No SetWriter call — writer is nil.

	const target uint16 = 250
	ret, err := srv.MatterInvoke(context.Background(), wire.ColorCtrlCmdMoveToColorTemperature,
		wire.MoveToColorTemperatureRequest{ColorTemperatureMireds: target}, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("MatterInvoke without writer returned error: %v", err)
	}
	if ret != nil {
		t.Errorf("MatterInvoke without writer returned non-nil response: %v", ret)
	}
	if got := currentMireds(t, srv); got != target {
		t.Errorf("reported state = %d after no-writer command, want %d", got, target)
	}
}

// TestColorControl_WriteThrough_SetWriterNil verifies that passing nil to
// SetWriter detaches any previously wired writer and subsequent commands
// proceed without error (no panic, state updated).
func TestColorControl_WriteThrough_SetWriterNil(t *testing.T) {
	t.Parallel()
	cfg := light.DefaultColorControlServerConfig()
	srv := light.NewColorControlServer(cfg)
	w := &fakeColorTemperatureWriter{}
	srv.SetWriter(w)
	srv.SetWriter(nil) // detach

	const target uint16 = 200
	_, err := srv.MatterInvoke(context.Background(), wire.ColorCtrlCmdMoveToColorTemperature,
		wire.MoveToColorTemperatureRequest{ColorTemperatureMireds: target}, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("MatterInvoke after SetWriter(nil) returned error: %v", err)
	}
	if w.calls != 0 {
		t.Errorf("detached writer was called %d times, want 0", w.calls)
	}
	if got := currentMireds(t, srv); got != target {
		t.Errorf("reported state = %d, want %d", got, target)
	}
}

// TestColorControl_WriteThrough_PriorityPropagation verifies that the exact
// priority value passed to MatterInvoke is forwarded unchanged to the writer.
func TestColorControl_WriteThrough_PriorityPropagation(t *testing.T) {
	t.Parallel()
	priorities := []hmenum.CommandPriority{
		hmenum.CommandPriorityCritical,
		hmenum.CommandPriorityHigh,
		hmenum.CommandPriorityLow,
	}
	cfg := light.DefaultColorControlServerConfig()
	for _, prio := range priorities {
		t.Run("priority", func(t *testing.T) {
			t.Parallel()
			srv := light.NewColorControlServer(cfg)
			w := &fakeColorTemperatureWriter{}
			srv.SetWriter(w)
			_, err := srv.MatterInvoke(context.Background(), wire.ColorCtrlCmdMoveToColorTemperature,
				wire.MoveToColorTemperatureRequest{ColorTemperatureMireds: 300}, prio)
			if err != nil {
				t.Fatalf("MatterInvoke: %v", err)
			}
			if w.lastPriority != prio {
				t.Errorf("writer received priority=%v, want %v", w.lastPriority, prio)
			}
		})
	}
}
