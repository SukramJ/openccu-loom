// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package coordinators

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/events"
)

// fakeSysvarCreator is a test double for SysvarCreator.
type fakeSysvarCreator struct {
	boolCalled  bool
	enumCalled  bool
	floatCalled bool
	failWith    error
}

func (f *fakeSysvarCreator) CreateSysvarBool(_ context.Context, _ string, _ bool) (map[string]any, error) {
	f.boolCalled = true
	if f.failWith != nil {
		return nil, f.failWith
	}
	return map[string]any{"type": "bool"}, nil
}

func (f *fakeSysvarCreator) CreateSysvarEnum(_ context.Context, _ string, _ []string) (map[string]any, error) {
	f.enumCalled = true
	if f.failWith != nil {
		return nil, f.failWith
	}
	return map[string]any{"type": "enum"}, nil
}

func (f *fakeSysvarCreator) CreateSysvarFloat(_ context.Context, _ string, _, _ float64) (map[string]any, error) {
	f.floatCalled = true
	if f.failWith != nil {
		return nil, f.failWith
	}
	return map[string]any{"type": "float"}, nil
}

// TestHubCreateSysvarBoolNoCreator verifies error when not wired.
func TestHubCreateSysvarBoolNoCreator(t *testing.T) {
	bus := events.NewBus()
	h := NewHubCoordinator("main", bus)
	_, err := h.CreateSysvarBool(context.Background(), "test", true)
	if err == nil {
		t.Fatal("expected error when creator not wired")
	}
}

// TestHubCreateSysvarBoolDelegates verifies delegates to wired creator.
func TestHubCreateSysvarBoolDelegates(t *testing.T) {
	bus := events.NewBus()
	h := NewHubCoordinator("main", bus)
	fc := &fakeSysvarCreator{}
	h.SetSysvarCreator(fc)

	result, err := h.CreateSysvarBool(context.Background(), "myBool", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !fc.boolCalled {
		t.Fatal("CreateSysvarBool was not delegated to creator")
	}
	if result["type"] != "bool" {
		t.Fatalf("unexpected result: %v", result)
	}
}

// TestHubCreateSysvarEnumDelegates verifies for enum type.
func TestHubCreateSysvarEnumDelegates(t *testing.T) {
	bus := events.NewBus()
	h := NewHubCoordinator("main", bus)
	fc := &fakeSysvarCreator{}
	h.SetSysvarCreator(fc)

	_, err := h.CreateSysvarEnum(context.Background(), "myEnum", []string{"a", "b"})
	if err != nil || !fc.enumCalled {
		t.Fatalf("CreateSysvarEnum: err=%v called=%v", err, fc.enumCalled)
	}
}

// TestHubCreateSysvarFloatDelegates verifies for float type.
func TestHubCreateSysvarFloatDelegates(t *testing.T) {
	bus := events.NewBus()
	h := NewHubCoordinator("main", bus)
	fc := &fakeSysvarCreator{}
	h.SetSysvarCreator(fc)

	_, err := h.CreateSysvarFloat(context.Background(), "myFloat", 0.0, 100.0)
	if err != nil || !fc.floatCalled {
		t.Fatalf("CreateSysvarFloat: err=%v called=%v", err, fc.floatCalled)
	}
}

// TestHubCreateSysvarBoolPropagatesError verifies that creator errors
// are surfaced to the caller.
func TestHubCreateSysvarBoolPropagatesError(t *testing.T) {
	bus := events.NewBus()
	h := NewHubCoordinator("main", bus)
	sentinel := errors.New("connection refused")
	h.SetSysvarCreator(&fakeSysvarCreator{failWith: sentinel})

	_, err := h.CreateSysvarBool(context.Background(), "x", true)
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error, got %v", err)
	}
}
