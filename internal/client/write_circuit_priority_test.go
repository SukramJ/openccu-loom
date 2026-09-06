// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package client_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/reliability"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// countingWriteBackend counts the wire attempts the orchestration write
// path makes, so an open breaker's probe is observable.
type countingWriteBackend struct {
	*orchBackend
	setValues   int
	putParamset int
}

func (b *countingWriteBackend) SetValue(context.Context, string, hmenum.Parameter, any, hmenum.CommandPriority, hmenum.CommandRxMode) error {
	b.setValues++
	return nil
}

func (b *countingWriteBackend) PutParamset(context.Context, string, hmenum.ParamsetKey, map[string]any, hmenum.CommandPriority, hmenum.CommandRxMode) error {
	b.putParamset++
	return nil
}

// openBreakerClient returns a client whose breaker is tripped OPEN for the
// duration of the test, mirroring newOpenCircuitForProbe in the
// reliability package.
func openBreakerClient(t *testing.T) *client.InterfaceClient {
	t.Helper()
	cb := reliability.NewCircuit(reliability.CircuitConfig{FailureThreshold: 1, ResetTimeout: time.Hour})
	cb.RecordFailure()
	if cb.State() != hmenum.CircuitStateOpen {
		t.Fatalf("breaker state = %v, want OPEN", cb.State())
	}
	ic, err := client.New(client.Config{
		CentralName: "test",
		Interface:   hmenum.InterfaceHmIPRF,
		Caller:      client.CallerFunc(func(context.Context, string, []any) (any, error) { return nil, nil }),
		Circuit:     cb,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return ic
}

// TestSetValueForwardsCriticalPriorityThroughAnOpenBreaker pins that the
// alarm engine's stop command reaches the wire on the orchestration write
// path, not only on InterfaceClient.Call.
func TestSetValueForwardsCriticalPriorityThroughAnOpenBreaker(t *testing.T) {
	t.Parallel()
	ic := openBreakerClient(t)
	b := &countingWriteBackend{orchBackend: &orchBackend{}}

	err := ic.SetValue(context.Background(), b, "ABC0001:1", hmenum.ParameterState, false,
		hmenum.CommandPriorityCritical, hmenum.CommandRxModeUnset, true)
	if err != nil {
		t.Fatalf("critical SetValue through open breaker: err = %v, want nil", err)
	}
	if b.setValues != 1 {
		t.Fatalf("backend SetValue calls = %d, want 1 (the critical probe never reached the wire)", b.setValues)
	}
}

func TestSetValueLowPriorityIsShedByAnOpenBreaker(t *testing.T) {
	t.Parallel()
	ic := openBreakerClient(t)
	b := &countingWriteBackend{orchBackend: &orchBackend{}}

	err := ic.SetValue(context.Background(), b, "ABC0001:1", hmenum.ParameterState, false,
		hmenum.CommandPriorityLow, hmenum.CommandRxModeUnset, true)
	if !errors.Is(err, hmerr.ErrCircuitBreakerOpen) {
		t.Fatalf("low-priority SetValue: err = %v, want ErrCircuitBreakerOpen", err)
	}
	if b.setValues != 0 {
		t.Fatalf("backend SetValue calls = %d, want 0", b.setValues)
	}
}

func TestPutParamsetForwardsCriticalPriorityThroughAnOpenBreaker(t *testing.T) {
	t.Parallel()
	ic := openBreakerClient(t)
	b := &countingWriteBackend{orchBackend: &orchBackend{}}

	err := ic.PutParamset(context.Background(), b, "ABC0001:1", string(hmenum.ParamsetKeyValues),
		map[string]any{"STATE": false}, hmenum.CommandPriorityCritical, hmenum.CommandRxModeUnset, true)
	if err != nil {
		t.Fatalf("critical PutParamset through open breaker: err = %v, want nil", err)
	}
	if b.putParamset != 1 {
		t.Fatalf("backend PutParamset calls = %d, want 1 (the critical probe never reached the wire)", b.putParamset)
	}
}

func TestPutParamsetLowPriorityIsShedByAnOpenBreaker(t *testing.T) {
	t.Parallel()
	ic := openBreakerClient(t)
	b := &countingWriteBackend{orchBackend: &orchBackend{}}

	err := ic.PutParamset(context.Background(), b, "ABC0001:1", string(hmenum.ParamsetKeyValues),
		map[string]any{"STATE": false}, hmenum.CommandPriorityLow, hmenum.CommandRxModeUnset, true)
	if !errors.Is(err, hmerr.ErrCircuitBreakerOpen) {
		t.Fatalf("low-priority PutParamset: err = %v, want ErrCircuitBreakerOpen", err)
	}
	if b.putParamset != 0 {
		t.Fatalf("backend PutParamset calls = %d, want 0", b.putParamset)
	}
}
