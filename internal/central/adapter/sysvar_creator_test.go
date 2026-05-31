// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

// Tests for clientSysvarCreator and WireSysvarCreator.
//
// Strategy: build a minimal central with a fake backends.Operations that
// records CreateSystemVariable* calls, wire it through WireSysvarCreator,
// then call the coordinator's CreateSysvar* methods and assert correct
// delegation.

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	clientpkg "github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// sysvarOps is a minimal backends.Operations that records the three
// CreateSystemVariable* calls. All other methods are no-ops inherited
// from fakeOperations. We embed fakeOperations to satisfy the full
// backends.Operations interface without listing every method.
type sysvarOps struct {
	fakeOperations

	boolCalls  []sysvarBoolArgs
	enumCalls  []sysvarEnumArgs
	floatCalls []sysvarFloatArgs
}

type sysvarBoolArgs struct {
	Name    string
	InitVal bool
}

type sysvarEnumArgs struct {
	Name      string
	ValueList []string
}

type sysvarFloatArgs struct {
	Name     string
	MinValue float64
	MaxValue float64
}

func (s *sysvarOps) Capabilities() backends.Capabilities {
	return backends.Capabilities{CreateSystemVariable: true}
}

func (s *sysvarOps) CreateSystemVariableBool(_ context.Context, name string, initVal bool) (map[string]any, error) {
	s.boolCalls = append(s.boolCalls, sysvarBoolArgs{Name: name, InitVal: initVal})
	return map[string]any{"name": name, "type": "BOOL"}, nil
}

func (s *sysvarOps) CreateSystemVariableEnum(_ context.Context, name string, valueList []string) (map[string]any, error) {
	s.enumCalls = append(s.enumCalls, sysvarEnumArgs{Name: name, ValueList: valueList})
	return map[string]any{"name": name, "type": "ENUM"}, nil
}

func (s *sysvarOps) CreateSystemVariableFloat(_ context.Context, name string, minValue, maxValue float64) (map[string]any, error) {
	s.floatCalls = append(s.floatCalls, sysvarFloatArgs{Name: name, MinValue: minValue, MaxValue: maxValue})
	return map[string]any{"name": name, "type": "FLOAT"}, nil
}

// buildSysvarCreatorFixture sets up a minimal CentralUnit + ValueWriter
// with a fake backend registered, then wires the sysvar creator and
// returns the hub coordinator for assertions.
func buildSysvarCreatorFixture(t *testing.T) (
	hub *coordinators.HubCoordinator,
	ops *sysvarOps,
) {
	t.Helper()
	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}

	ops = &sysvarOps{fakeOperations: fakeOperations{kind: backends.KindCCU}}
	w := clientpkg.NewValueWriter()
	w.Register("ccu-01", "HmIP-RF", ops)

	// Register a minimal client entry so PrimaryClient() returns non-nil.
	ic := newTestInterfaceClient(t, "ccu-01", "HmIP-RF", 5)
	if err := c.Clients.Register(&coordinators.ClientEntry{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Client:      ic,
	}); err != nil {
		t.Fatalf("Clients.Register: %v", err)
	}

	WireSysvarCreator(c, w)
	return c.Hub, ops
}

func TestWireSysvarCreatorNilSafe(t *testing.T) {
	t.Parallel()
	// Neither nil central nor nil Hub must panic.
	WireSysvarCreator(nil, nil)

	c, _ := central.New(central.Config{Name: "ccu-02"})
	// Hub is non-nil by default in central.New — call with nil writer.
	WireSysvarCreator(c, nil)
}

func TestSysvarCreatorDelegatesBool(t *testing.T) {
	t.Parallel()
	hub, ops := buildSysvarCreatorFixture(t)

	result, err := hub.CreateSysvarBool(context.Background(), "AlarmActive", true)
	if err != nil {
		t.Fatalf("CreateSysvarBool: %v", err)
	}
	if len(ops.boolCalls) != 1 {
		t.Fatalf("expected 1 bool call, got %d", len(ops.boolCalls))
	}
	if ops.boolCalls[0].Name != "AlarmActive" {
		t.Errorf("name = %q, want AlarmActive", ops.boolCalls[0].Name)
	}
	if !ops.boolCalls[0].InitVal {
		t.Error("initVal should be true")
	}
	if result["name"] != "AlarmActive" {
		t.Errorf("result = %v", result)
	}
}

func TestSysvarCreatorDelegatesEnum(t *testing.T) {
	t.Parallel()
	hub, ops := buildSysvarCreatorFixture(t)

	values := []string{"low", "medium", "high"}
	_, err := hub.CreateSysvarEnum(context.Background(), "Level", values)
	if err != nil {
		t.Fatalf("CreateSysvarEnum: %v", err)
	}
	if len(ops.enumCalls) != 1 {
		t.Fatalf("expected 1 enum call, got %d", len(ops.enumCalls))
	}
	call := ops.enumCalls[0]
	if call.Name != "Level" {
		t.Errorf("name = %q, want Level", call.Name)
	}
	if len(call.ValueList) != 3 || call.ValueList[0] != "low" {
		t.Errorf("valueList = %v, want [low medium high]", call.ValueList)
	}
}

func TestSysvarCreatorDelegatesFloat(t *testing.T) {
	t.Parallel()
	hub, ops := buildSysvarCreatorFixture(t)

	_, err := hub.CreateSysvarFloat(context.Background(), "Temperature", -20.0, 50.0)
	if err != nil {
		t.Fatalf("CreateSysvarFloat: %v", err)
	}
	if len(ops.floatCalls) != 1 {
		t.Fatalf("expected 1 float call, got %d", len(ops.floatCalls))
	}
	call := ops.floatCalls[0]
	if call.Name != "Temperature" {
		t.Errorf("name = %q, want Temperature", call.Name)
	}
	if call.MinValue != -20.0 || call.MaxValue != 50.0 {
		t.Errorf("min/max = %g/%g, want -20/50", call.MinValue, call.MaxValue)
	}
}

func TestSysvarCreatorNoPrimaryClientReturnsError(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-03"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	// No clients registered — PrimaryClient returns nil.
	WireSysvarCreator(c, clientpkg.NewValueWriter())

	_, err = c.Hub.CreateSysvarBool(context.Background(), "x", false)
	if err == nil {
		t.Fatal("expected error when no primary client registered")
	}
}

func TestSysvarCreatorNoBackendRegisteredReturnsError(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-04"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}

	// Register a client entry but don't register any backend in the writer.
	ic := newTestInterfaceClient(t, "ccu-04", "HmIP-RF", 5)
	if err := c.Clients.Register(&coordinators.ClientEntry{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Client:      ic,
	}); err != nil {
		t.Fatalf("Clients.Register: %v", err)
	}

	w := clientpkg.NewValueWriter()
	// Intentionally no w.Register(...)
	WireSysvarCreator(c, w)

	_, err = c.Hub.CreateSysvarFloat(context.Background(), "y", 0, 100)
	if err == nil {
		t.Fatal("expected error when backend not registered")
	}
}
