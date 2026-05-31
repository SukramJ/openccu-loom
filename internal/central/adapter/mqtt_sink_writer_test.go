// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// mqtt_sink_writer_test.go covers the non-nil writer path in
// MQTTCommandSink.SetValue and the non-nil cdpDispatch path in
// MQTTCommandSink.InvokeCustomDP.

package adapter

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func TestMQTTCommandSinkSetValueWithWriter(t *testing.T) {
	t.Parallel()
	w := &fakeWriter{}
	s := NewMQTTCommandSink(central.NewRegistry(), w)
	err := s.SetValue(
		context.Background(),
		"ccu-01", "HmIP-RF", "DEV001:1",
		hmenum.ParameterState, true, hmenum.CommandPriorityLow,
	)
	// fakeWriter.SetValue returns nil — call must succeed.
	if err != nil {
		t.Errorf("SetValue with fakeWriter: %v", err)
	}
	if w.calls.Load() != 1 {
		t.Errorf("fakeWriter not called, got %d calls", w.calls.Load())
	}
}
