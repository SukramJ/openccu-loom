// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package alarm

import (
	"context"
	"log/slog"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// --- Service.active -------------------------------------------------------

// countingHandler is a minimal slog.Handler that only counts records, so
// a test can assert a warning fired exactly once without depending on
// message formatting.
type countingHandler struct {
	mu    sync.Mutex
	count int
}

func (h *countingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *countingHandler) Handle(context.Context, slog.Record) error {
	h.mu.Lock()
	h.count++
	h.mu.Unlock()
	return nil
}

func (h *countingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }

func (h *countingHandler) WithGroup(string) slog.Handler { return h }

func (h *countingHandler) Count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.count
}

// TestServiceActiveFallsBackAndWarnsOnceForUnresolvableValueList builds a
// Service as a bare struct literal (the established pattern in this
// package's tests — see candidates_test.go) around a binding whose
// channel the registry does not carry. The rule is configured, so
// resolution needs the parameter's value list; since the channel never
// resolves, active falls back to the historical rule and must warn
// exactly once, not on every event of the same binding.
func TestServiceActiveFallsBackAndWarnsOnceForUnresolvableValueList(t *testing.T) {
	t.Parallel()

	reg := central.NewRegistry()
	u, err := central.New(central.Config{Name: "my-ccu"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	if err := reg.Register(u); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	// "my-ccu" is registered but carries no device/channel matching the
	// binding below, so the registry genuinely has no such channel.

	handler := &countingHandler{}
	s := &Service{
		reg:   reg,
		enums: newEnumResolver(reg),
		log:   slog.New(handler),
	}
	binding := sensorBinding{
		id:             "sensor-1",
		activeValues:   []string{"PRIMARY_ALARM", "SECONDARY_ALARM"},
		centralName:    "my-ccu",
		interfaceID:    "HmIP-RF",
		channelAddress: "SWSD0001:1",
		parameter:      "SMOKE_DETECTOR_ALARM_STATUS",
	}
	value := hmtypes.IntValue(1)

	active, known := s.active(binding, value)
	if !known {
		t.Fatal("known = false, want true (a fallback verdict is still a known one)")
	}
	if !active {
		t.Error("active = false, want true (the default rule reads 1 as active)")
	}
	if got := handler.Count(); got != 1 {
		t.Fatalf("log count after first event = %d, want 1", got)
	}

	// A second event on the same binding must not log again — the
	// chattering-sensor guard in enumResolver.shouldWarn.
	s.active(binding, value)
	if got := handler.Count(); got != 1 {
		t.Errorf("log count after second event = %d, want still 1 (no repeat warning)", got)
	}
}
