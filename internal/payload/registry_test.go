// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package payload

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// mustNotPanic calls f and returns the recovered value (or nil if f did not
// panic). Use with t.Helper() callers to keep attribution clean.
func recoverPanic(f func()) (v any) {
	defer func() { v = recover() }()
	f()
	return nil
}

// TestZeroValueServiceRegistry verifies the zero value is safe to use without
// any initialisation.
func TestZeroValueServiceRegistry(t *testing.T) {
	var r ServiceRegistry

	if names := r.ServiceMethodNames(); names != nil {
		t.Fatalf("zero-value ServiceMethodNames: want nil, got %v", names)
	}

	err := r.Invoke(context.Background(), "anything", nil, hmenum.CommandPriorityCritical)
	if !errors.Is(err, ErrUnknownServiceMethod) {
		t.Fatalf("zero-value Invoke: want ErrUnknownServiceMethod, got %v", err)
	}
}

// TestRegisterServicePreservesOrder verifies ServiceMethodNames returns
// names in the order they were registered.
func TestRegisterServicePreservesOrder(t *testing.T) {
	var r ServiceRegistry
	noop := ServiceHandler(func(_ context.Context, _ map[string]any, _ hmenum.CommandPriority) error {
		return nil
	})

	r.RegisterService("alpha", noop)
	r.RegisterService("beta", noop)
	r.RegisterService("gamma", noop)

	got := r.ServiceMethodNames()
	want := []string{"alpha", "beta", "gamma"}
	if len(got) != len(want) {
		t.Fatalf("ServiceMethodNames len=%d, want %d", len(got), len(want))
	}
	for i, n := range want {
		if got[i] != n {
			t.Errorf("position %d: got %q, want %q", i, got[i], n)
		}
	}
}

// TestServiceMethodNamesFreshCopy verifies that mutating the returned slice
// does not affect the registry's internal state.
func TestServiceMethodNamesFreshCopy(t *testing.T) {
	var r ServiceRegistry
	noop := ServiceHandler(func(_ context.Context, _ map[string]any, _ hmenum.CommandPriority) error {
		return nil
	})
	r.RegisterService("one", noop)
	r.RegisterService("two", noop)

	names := r.ServiceMethodNames()
	names[0] = "mutated"

	again := r.ServiceMethodNames()
	if again[0] != "one" {
		t.Fatalf("registry mutated by caller: got %q, want %q", again[0], "one")
	}
}

// TestInvokeDispatchesCorrectHandler verifies that Invoke calls the right
// handler and forwards ctx, params, and priority unchanged.
func TestInvokeDispatchesCorrectHandler(t *testing.T) {
	var r ServiceRegistry

	wantCtx := context.WithValue(context.Background(), struct{ k string }{"key"}, "val")
	wantParams := map[string]any{"level": 42}
	wantPriority := hmenum.CommandPriorityHigh

	var gotCtx context.Context
	var gotParams map[string]any
	var gotPriority hmenum.CommandPriority

	r.RegisterService("set_level", func(ctx context.Context, params map[string]any, priority hmenum.CommandPriority) error {
		gotCtx = ctx
		gotParams = params
		gotPriority = priority
		return nil
	})

	if err := r.Invoke(wantCtx, "set_level", wantParams, wantPriority); err != nil {
		t.Fatalf("Invoke returned unexpected error: %v", err)
	}
	if gotCtx != wantCtx {
		t.Error("ctx not forwarded to handler")
	}
	if gotParams == nil || gotParams["level"] != 42 {
		t.Errorf("params not forwarded: %v", gotParams)
	}
	if gotPriority != wantPriority {
		t.Errorf("priority not forwarded: got %v, want %v", gotPriority, wantPriority)
	}
}

// TestInvokeUnknownMethod verifies that Invoke on an unregistered name
// returns an error that wraps ErrUnknownServiceMethod and contains the
// method name in its message.
func TestInvokeUnknownMethod(t *testing.T) {
	var r ServiceRegistry

	err := r.Invoke(context.Background(), "no_such_method", nil, hmenum.CommandPriorityCritical)
	if !errors.Is(err, ErrUnknownServiceMethod) {
		t.Fatalf("want ErrUnknownServiceMethod, got %v", err)
	}
	if !strings.Contains(err.Error(), "no_such_method") {
		t.Errorf("error message does not contain method name: %q", err.Error())
	}
}

// TestInvokeHandlerErrorPassthrough verifies that an error returned by a
// handler flows back to the caller without extra wrapping.
func TestInvokeHandlerErrorPassthrough(t *testing.T) {
	var r ServiceRegistry

	sentinel := errors.New("handler exploded")
	r.RegisterService("boom", func(_ context.Context, _ map[string]any, _ hmenum.CommandPriority) error {
		return sentinel
	})

	err := r.Invoke(context.Background(), "boom", nil, hmenum.CommandPriorityCritical)
	if !errors.Is(err, sentinel) {
		t.Fatalf("handler error not passed through: got %v", err)
	}
}

// TestRegisterServicePanicsOnEmptyName verifies that registering with an empty
// name panics and that the panic message contains "empty".
func TestRegisterServicePanicsOnEmptyName(t *testing.T) {
	var r ServiceRegistry
	noop := ServiceHandler(func(_ context.Context, _ map[string]any, _ hmenum.CommandPriority) error {
		return nil
	})

	v := recoverPanic(func() { r.RegisterService("", noop) })
	if v == nil {
		t.Fatal("expected panic for empty name, got none")
	}
	msg, ok := v.(string)
	if !ok {
		t.Fatalf("panic value is not a string: %T %v", v, v)
	}
	if !strings.Contains(strings.ToLower(msg), "empty") {
		t.Errorf("panic message %q does not contain 'empty'", msg)
	}
}

// TestRegisterServicePanicsOnNilHandler verifies that passing a nil handler
// panics and that the message contains "nil handler".
func TestRegisterServicePanicsOnNilHandler(t *testing.T) {
	var r ServiceRegistry

	v := recoverPanic(func() { r.RegisterService("valid_name", nil) })
	if v == nil {
		t.Fatal("expected panic for nil handler, got none")
	}
	msg, ok := v.(string)
	if !ok {
		t.Fatalf("panic value is not a string: %T %v", v, v)
	}
	if !strings.Contains(strings.ToLower(msg), "nil handler") {
		t.Errorf("panic message %q does not contain 'nil handler'", msg)
	}
}

// TestRegisterServicePanicsOnDuplicate verifies that registering the same
// name twice panics and that the message contains "duplicate".
func TestRegisterServicePanicsOnDuplicate(t *testing.T) {
	var r ServiceRegistry
	noop := ServiceHandler(func(_ context.Context, _ map[string]any, _ hmenum.CommandPriority) error {
		return nil
	})
	r.RegisterService("dup", noop)

	v := recoverPanic(func() { r.RegisterService("dup", noop) })
	if v == nil {
		t.Fatal("expected panic for duplicate name, got none")
	}
	msg, ok := v.(string)
	if !ok {
		t.Fatalf("panic value is not a string: %T %v", v, v)
	}
	if !strings.Contains(strings.ToLower(msg), "duplicate") {
		t.Errorf("panic message %q does not contain 'duplicate'", msg)
	}
}

// TestInvokeConcurrentCallsRaceFree verifies that concurrent Invoke calls
// after registration do not trigger the race detector.
func TestInvokeConcurrentCallsRaceFree(t *testing.T) {
	var r ServiceRegistry
	r.RegisterService("ping", func(_ context.Context, _ map[string]any, _ hmenum.CommandPriority) error {
		return nil
	})

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			_ = r.Invoke(context.Background(), "ping", nil, hmenum.CommandPriorityLow)
		}()
	}
	wg.Wait()
}

// TestServiceMethodNamesCachedPath verifies that ServiceMethodNames returns
// the same backing slice on consecutive calls (O(1) cache).
func TestServiceMethodNamesCachedPath(t *testing.T) {
	var r ServiceRegistry
	noop := ServiceHandler(func(_ context.Context, _ map[string]any, _ hmenum.CommandPriority) error {
		return nil
	})
	r.RegisterService("alpha", noop)
	r.RegisterService("beta", noop)

	first := r.ServiceMethodNames()
	second := r.ServiceMethodNames()

	// Both slices must have the same length and content.
	if len(first) != 2 || len(second) != 2 {
		t.Fatalf("expected 2 methods, got first=%d second=%d", len(first), len(second))
	}
	if first[0] != "alpha" || second[0] != "alpha" {
		t.Errorf("unexpected first element: first[0]=%q second[0]=%q", first[0], second[0])
	}
	if first[1] != "beta" || second[1] != "beta" {
		t.Errorf("unexpected second element: first[1]=%q second[1]=%q", first[1], second[1])
	}
}

// TestRegisterServiceWithArgScalarKey verifies that RegisterServiceWithArg
// stores the scalar-arg key and that ScalarArgKey returns it for registered
// methods and "value" for unknown ones. It also verifies that GlobalScalarArgKey
// returns the same key (the per-instance registration propagates to the
// package-level table).
func TestRegisterServiceWithArgScalarKey(t *testing.T) {
	var r ServiceRegistry
	noop := ServiceHandler(func(_ context.Context, _ map[string]any, _ hmenum.CommandPriority) error {
		return nil
	})

	r.RegisterServiceWithArg("set_temperature", "temperature", noop)
	r.RegisterServiceWithArg("set_mode", "mode", noop)
	r.RegisterService("turn_on", noop) // legacy API — should get "value"

	// Per-instance ScalarArgKey.
	if got := r.ScalarArgKey("set_temperature"); got != "temperature" {
		t.Errorf("ScalarArgKey(set_temperature) = %q, want %q", got, "temperature")
	}
	if got := r.ScalarArgKey("set_mode"); got != "mode" {
		t.Errorf("ScalarArgKey(set_mode) = %q, want %q", got, "mode")
	}
	if got := r.ScalarArgKey("turn_on"); got != "value" {
		t.Errorf("ScalarArgKey(turn_on) = %q, want %q (legacy API should default to value)", got, "value")
	}
	if got := r.ScalarArgKey("unknown"); got != "value" {
		t.Errorf("ScalarArgKey(unknown) = %q, want %q", got, "value")
	}

	// Package-level GlobalScalarArgKey must reflect the registered keys.
	if got := GlobalScalarArgKey("set_temperature"); got != "temperature" {
		t.Errorf("GlobalScalarArgKey(set_temperature) = %q, want %q", got, "temperature")
	}
	if got := GlobalScalarArgKey("set_mode"); got != "mode" {
		t.Errorf("GlobalScalarArgKey(set_mode) = %q, want %q", got, "mode")
	}
	// "unknown_method" was never registered anywhere in this process at this
	// point — falls back to "value".
	if got := GlobalScalarArgKey("unknown_method_xyz_not_registered"); got != "value" {
		t.Errorf("GlobalScalarArgKey(unknown) = %q, want %q", got, "value")
	}
}

// TestRegisterServiceWithArgEmptyKeyDefaultsToValue verifies that an empty
// scalarArgKey string defaults to "value" (not the empty string).
func TestRegisterServiceWithArgEmptyKeyDefaultsToValue(t *testing.T) {
	var r ServiceRegistry
	noop := ServiceHandler(func(_ context.Context, _ map[string]any, _ hmenum.CommandPriority) error {
		return nil
	})
	r.RegisterServiceWithArg("set_something", "", noop)
	if got := r.ScalarArgKey("set_something"); got != "value" {
		t.Errorf("ScalarArgKey with empty scalarArgKey = %q, want %q", got, "value")
	}
}

// TestServiceMethodNamesConcurrentReadRaceFree verifies that concurrent
// ServiceMethodNames reads concurrent with Invoke calls do not trigger the
// race detector.
func TestServiceMethodNamesConcurrentReadRaceFree(t *testing.T) {
	var r ServiceRegistry
	r.RegisterService("m1", func(_ context.Context, _ map[string]any, _ hmenum.CommandPriority) error {
		return nil
	})
	r.RegisterService("m2", func(_ context.Context, _ map[string]any, _ hmenum.CommandPriority) error {
		return nil
	})

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines * 2)

	for range goroutines {
		go func() {
			defer wg.Done()
			_ = r.ServiceMethodNames()
		}()
		go func() {
			defer wg.Done()
			_ = r.Invoke(context.Background(), "m1", nil, hmenum.CommandPriorityCritical)
		}()
	}
	wg.Wait()
}
