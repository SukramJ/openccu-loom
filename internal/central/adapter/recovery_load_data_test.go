// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// recordingSeeder records every request and the order relative to the hub
// step through a shared call log.
type recordingSeeder struct {
	calls  *[]string
	values map[string]map[string]any
	err    error
	ifaces []hmenum.Interface
	depths []SeedDepth
}

func (s *recordingSeeder) SeedValues(_ context.Context, iface hmenum.Interface, depth SeedDepth) (map[string]map[string]any, error) {
	*s.calls = append(*s.calls, "seed")
	s.ifaces = append(s.ifaces, iface)
	s.depths = append(s.depths, depth)
	return s.values, s.err
}

func recordingHubStep(calls *[]string, err error) func(context.Context) error {
	return func(context.Context) error {
		*calls = append(*calls, "hub")
		return err
	}
}

func TestRecoveryLoadDataReseedsItsInterfaceInFullAfterTheHubRefresh(t *testing.T) {
	t.Parallel()
	f := buildBoost7Fixture(t)
	p := NewDevicePipeline(f.unit)
	ch := f.dev.Channel("DEV002:0")
	if ch == nil {
		t.Fatal("fixture channel DEV002:0 missing")
	}
	state := generic.NewDataPoint[bool](generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID: "HmIP-RF", ChannelAddress: "DEV002:0",
			ParamsetKey: hmenum.ParamsetKeyValues, Parameter: "STATE",
		},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeBool, Operations: hmenum.OperationsRead | hmenum.OperationsEvent},
	})
	ch.Put(state)

	var calls []string
	seeder := &recordingSeeder{calls: &calls, values: map[string]map[string]any{"DEV002:0": {"STATE": true}}}
	step := newRecoveryLoadData(recordingHubStep(&calls, nil), p, hmenum.InterfaceHmIPRF, seeder, slog.New(slog.DiscardHandler))

	if err := step(context.Background()); err != nil {
		t.Fatalf("step: %v", err)
	}
	if len(calls) != 2 || calls[0] != "hub" || calls[1] != "seed" {
		t.Fatalf("call order %v, want [hub seed]", calls)
	}
	if len(seeder.depths) != 1 || seeder.depths[0] != SeedFull {
		t.Errorf("seeder saw depths %v, want [SeedFull]", seeder.depths)
	}
	if len(seeder.ifaces) != 1 || seeder.ifaces[0] != hmenum.InterfaceHmIPRF {
		t.Errorf("seeder saw interfaces %v, want [%s]", seeder.ifaces, hmenum.InterfaceHmIPRF)
	}
	if v, observed := state.Value(); !observed || !v {
		t.Errorf("STATE = %v (observed %v), want true from the post-recovery reseed", v, observed)
	}
}

func TestRecoveryLoadDataWithoutSeederStillRefreshesTheHub(t *testing.T) {
	t.Parallel()
	f := buildBoost7Fixture(t)
	var calls []string
	step := newRecoveryLoadData(recordingHubStep(&calls, nil), NewDevicePipeline(f.unit), hmenum.InterfaceHmIPRF, nil, nil)
	if err := step(context.Background()); err != nil {
		t.Fatalf("step: %v", err)
	}
	if len(calls) != 1 || calls[0] != "hub" {
		t.Fatalf("calls %v, want [hub]", calls)
	}
}

// A failed reseed is best-effort, like the hub refresh it follows: it must
// not fail the DATA_LOADING stage.
func TestRecoveryLoadDataReseedFailureDoesNotFailTheStage(t *testing.T) {
	t.Parallel()
	f := buildBoost7Fixture(t)
	var calls []string
	seeder := &recordingSeeder{calls: &calls, err: errors.New("rega down")}
	step := newRecoveryLoadData(recordingHubStep(&calls, nil), NewDevicePipeline(f.unit), hmenum.InterfaceHmIPRF, seeder, slog.New(slog.DiscardHandler))
	if err := step(context.Background()); err != nil {
		t.Fatalf("step returned %v, want nil for a best-effort reseed failure", err)
	}
	if len(calls) != 2 {
		t.Fatalf("calls %v, want [hub seed]", calls)
	}
}

// The hub step's own result passes through unchanged, and the reseed still
// runs: the devices need re-measuring whatever the hub metadata did.
func TestRecoveryLoadDataPassesTheHubResultThroughAndStillReseeds(t *testing.T) {
	t.Parallel()
	f := buildBoost7Fixture(t)
	var calls []string
	hubErr := errors.New("hub failed")
	seeder := &recordingSeeder{calls: &calls}
	step := newRecoveryLoadData(recordingHubStep(&calls, hubErr), NewDevicePipeline(f.unit), hmenum.InterfaceHmIPRF, seeder, slog.New(slog.DiscardHandler))
	if err := step(context.Background()); !errors.Is(err, hubErr) {
		t.Fatalf("step returned %v, want the hub step's error", err)
	}
	if len(seeder.depths) != 1 {
		t.Fatalf("seeder called %d times, want 1", len(seeder.depths))
	}
}
