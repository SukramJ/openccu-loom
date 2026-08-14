// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Tests for the pure service-message helpers (serviceMessageParameter,
// interfaceForChannel) and the durable-suppression wiring
// (clientServiceMessageSuppressor, WireServiceMessageSuppressor) added to
// hub_wiring.go.

package adapter

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	clientpkg "github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ============================================================
// serviceMessageParameter — pure parsing
// ============================================================

func TestServiceMessageParameter(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"typical", "AL-ABC123:1.LOWBAT", "LOWBAT"},
		{"no dot", "NODOTSHERE", ""},
		{"empty", "", ""},
		{"multiple dots takes last segment", "AL-ABC.123:1.UNREACH", "UNREACH"},
		{"trailing dot yields empty parameter", "AL-ABC:1.", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := serviceMessageParameter(tc.raw); got != tc.want {
				t.Errorf("serviceMessageParameter(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

// ============================================================
// interfaceForChannel — model-registry resolution
// ============================================================

func TestInterfaceForChannelNilUnit(t *testing.T) {
	t.Parallel()
	if got := interfaceForChannel(nil, "ABC123:1"); got != "" {
		t.Errorf("expected empty string for nil unit, got %q", got)
	}
}

func TestInterfaceForChannelEmptyAddress(t *testing.T) {
	t.Parallel()
	unit, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	if got := interfaceForChannel(unit, ""); got != "" {
		t.Errorf("expected empty string for empty channelAddress, got %q", got)
	}
}

func TestInterfaceForChannelUnregisteredDevice(t *testing.T) {
	t.Parallel()
	unit, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	if got := interfaceForChannel(unit, "MISSING:1"); got != "" {
		t.Errorf("expected empty string for unregistered device, got %q", got)
	}
}

func TestInterfaceForChannelResolvesRegisteredDevice(t *testing.T) {
	t.Parallel()
	unit, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	d := device.New(device.Config{
		InterfaceID: "HmIP-RF", Interface: hmenum.InterfaceHmIPRF,
		Address: "ABC123", Model: "HmIP-STH", Name: "Flur",
	})
	unit.ModelRegistry.Put(d)

	if got := interfaceForChannel(unit, "ABC123:1"); got != "HmIP-RF" {
		t.Errorf("interfaceForChannel(channel) = %q, want HmIP-RF", got)
	}
	// Device-level address (no channel suffix) resolves the same way.
	if got := interfaceForChannel(unit, "ABC123"); got != "HmIP-RF" {
		t.Errorf("interfaceForChannel(device) = %q, want HmIP-RF", got)
	}
}

// ============================================================
// clientServiceMessageSuppressor — fixture
// ============================================================

// suppressOps is a minimal backends.Operations that records
// SuppressServiceMessage / GetSuppressedServiceMessages calls. Embeds
// fakeOperations to satisfy the rest of the Operations interface with
// no-ops.
type suppressOps struct {
	fakeOperations

	suppressCalls []suppressOpsCall
	getCalls      []string
	suppressErr   error
	getErr        error
	getResult     []string
}

type suppressOpsCall struct {
	channel, parameter string
	suppress           bool
}

func (s *suppressOps) Capabilities() backends.Capabilities {
	return backends.Capabilities{SuppressServiceMessage: true}
}

func (s *suppressOps) SuppressServiceMessage(_ context.Context, channelAddress, parameterID string, suppress bool) error {
	if s.suppressErr != nil {
		return s.suppressErr
	}
	s.suppressCalls = append(s.suppressCalls, suppressOpsCall{channelAddress, parameterID, suppress})
	return nil
}

func (s *suppressOps) GetSuppressedServiceMessages(_ context.Context, _, channelAddress string) ([]string, error) {
	s.getCalls = append(s.getCalls, channelAddress)
	if s.getErr != nil {
		return nil, s.getErr
	}
	return s.getResult, nil
}

// buildSuppressorFixture wires a *clientServiceMessageSuppressor against a
// central with one registered HmIP-RF client + backend, keyed the same way
// WireServiceMessageSuppressor's backendFor resolves it (via
// [WireInterfaceID]).
func buildSuppressorFixture(t *testing.T) (
	*clientServiceMessageSuppressor, *suppressOps, *central.Unit,
) {
	t.Helper()
	unit, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	ops := &suppressOps{fakeOperations: fakeOperations{kind: backends.KindCCU}}
	w := clientpkg.NewValueWriter()
	wireID := WireInterfaceID("ccu-01", hmenum.InterfaceHmIPRF)
	w.Register("ccu-01", hmtypes.ParseWireInterfaceID(wireID), ops)

	ic := newTestInterfaceClient(t, "ccu-01", "HmIP-RF", 5)
	if err := unit.Clients.Register(&coordinators.ClientEntry{
		InterfaceID: wireID,
		Interface:   hmenum.InterfaceHmIPRF,
		Client:      ic,
	}); err != nil {
		t.Fatalf("Clients.Register: %v", err)
	}

	return &clientServiceMessageSuppressor{unit: unit, writer: w}, ops, unit
}

func TestClientServiceMessageSuppressorSuppressHappyPath(t *testing.T) {
	t.Parallel()
	sup, ops, _ := buildSuppressorFixture(t)

	if err := sup.SuppressServiceMessage(context.Background(), "HmIP-RF", "ABC123:1", "LOWBAT", true); err != nil {
		t.Fatalf("SuppressServiceMessage: %v", err)
	}
	if len(ops.suppressCalls) != 1 {
		t.Fatalf("expected 1 suppress call, got %d", len(ops.suppressCalls))
	}
	got := ops.suppressCalls[0]
	if got.channel != "ABC123:1" || got.parameter != "LOWBAT" || !got.suppress {
		t.Errorf("suppress call = %+v, want {ABC123:1 LOWBAT true}", got)
	}
}

func TestClientServiceMessageSuppressorUnsuppressForwardsFalse(t *testing.T) {
	t.Parallel()
	sup, ops, _ := buildSuppressorFixture(t)

	if err := sup.SuppressServiceMessage(context.Background(), "HmIP-RF", "ABC123:1", "LOWBAT", false); err != nil {
		t.Fatalf("SuppressServiceMessage: %v", err)
	}
	if len(ops.suppressCalls) != 1 || ops.suppressCalls[0].suppress {
		t.Fatalf("expected a single clear (suppress=false) call, got %+v", ops.suppressCalls)
	}
}

func TestClientServiceMessageSuppressorSuppressResolvesInterfaceFromChannel(t *testing.T) {
	t.Parallel()
	sup, ops, unit := buildSuppressorFixture(t)
	d := device.New(device.Config{
		InterfaceID: "HmIP-RF", Interface: hmenum.InterfaceHmIPRF,
		Address: "ABC123", Model: "HmIP-STH", Name: "Flur",
	})
	unit.ModelRegistry.Put(d)

	// interfaceID empty — must be resolved from the channel address via
	// the model registry instead of failing.
	if err := sup.SuppressServiceMessage(context.Background(), "", "ABC123:1", "LOWBAT", true); err != nil {
		t.Fatalf("SuppressServiceMessage: %v", err)
	}
	if len(ops.suppressCalls) != 1 {
		t.Fatalf("expected 1 suppress call after interface resolution, got %d", len(ops.suppressCalls))
	}
}

func TestClientServiceMessageSuppressorSuppressErrorPropagates(t *testing.T) {
	t.Parallel()
	sup, ops, _ := buildSuppressorFixture(t)
	boom := errors.New("ccu unreachable")
	ops.suppressErr = boom

	err := sup.SuppressServiceMessage(context.Background(), "HmIP-RF", "ABC123:1", "LOWBAT", true)
	if !errors.Is(err, boom) {
		t.Fatalf("SuppressServiceMessage error = %v, want %v", err, boom)
	}
}

func TestClientServiceMessageSuppressorSuppressNoClientRegistered(t *testing.T) {
	t.Parallel()
	unit, err := central.New(central.Config{Name: "ccu-02"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	sup := &clientServiceMessageSuppressor{unit: unit, writer: clientpkg.NewValueWriter()}

	err = sup.SuppressServiceMessage(context.Background(), "HmIP-RF", "ABC123:1", "LOWBAT", true)
	if !errors.Is(err, ErrServiceMessageSuppressorNoClient) {
		t.Fatalf("expected ErrServiceMessageSuppressorNoClient, got %v", err)
	}
}

func TestClientServiceMessageSuppressorSuppressNoBackendRegistered(t *testing.T) {
	t.Parallel()
	unit, err := central.New(central.Config{Name: "ccu-03"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	wireID := WireInterfaceID("ccu-03", hmenum.InterfaceHmIPRF)
	ic := newTestInterfaceClient(t, "ccu-03", "HmIP-RF", 5)
	if err := unit.Clients.Register(&coordinators.ClientEntry{
		InterfaceID: wireID,
		Interface:   hmenum.InterfaceHmIPRF,
		Client:      ic,
	}); err != nil {
		t.Fatalf("Clients.Register: %v", err)
	}
	// Intentionally no writer.Register(...) call.
	sup := &clientServiceMessageSuppressor{unit: unit, writer: clientpkg.NewValueWriter()}

	err = sup.SuppressServiceMessage(context.Background(), "HmIP-RF", "ABC123:1", "LOWBAT", true)
	if !errors.Is(err, ErrServiceMessageSuppressorNoClient) {
		t.Fatalf("expected ErrServiceMessageSuppressorNoClient, got %v", err)
	}
}

func TestClientServiceMessageSuppressorSuppressUnresolvableInterface(t *testing.T) {
	t.Parallel()
	sup, ops, _ := buildSuppressorFixture(t)

	// Neither an explicit interfaceID nor a resolvable channel address —
	// the model registry has no device for "MISSING:1".
	err := sup.SuppressServiceMessage(context.Background(), "", "MISSING:1", "LOWBAT", true)
	if !errors.Is(err, ErrServiceMessageSuppressorNoClient) {
		t.Fatalf("expected ErrServiceMessageSuppressorNoClient, got %v", err)
	}
	if len(ops.suppressCalls) != 0 {
		t.Errorf("backend must not be called when the interface cannot be resolved, got %v", ops.suppressCalls)
	}
}

func TestClientServiceMessageSuppressorGetSuppressedHappyPath(t *testing.T) {
	t.Parallel()
	sup, ops, _ := buildSuppressorFixture(t)
	ops.getResult = []string{"LOWBAT", "UNREACH"}

	got, err := sup.GetSuppressedServiceMessages(context.Background(), "HmIP-RF", "ABC123:1")
	if err != nil {
		t.Fatalf("GetSuppressedServiceMessages: %v", err)
	}
	if len(got) != 2 || got[0] != "LOWBAT" || got[1] != "UNREACH" {
		t.Errorf("got %v, want [LOWBAT UNREACH]", got)
	}
	if len(ops.getCalls) != 1 || ops.getCalls[0] != "ABC123:1" {
		t.Errorf("backend call = %v, want [ABC123:1]", ops.getCalls)
	}
}

func TestClientServiceMessageSuppressorGetSuppressedErrorPropagates(t *testing.T) {
	t.Parallel()
	sup, ops, _ := buildSuppressorFixture(t)
	boom := errors.New("read failed")
	ops.getErr = boom

	_, err := sup.GetSuppressedServiceMessages(context.Background(), "HmIP-RF", "ABC123:1")
	if !errors.Is(err, boom) {
		t.Fatalf("GetSuppressedServiceMessages error = %v, want %v", err, boom)
	}
}

func TestClientServiceMessageSuppressorGetSuppressedNoClientRegistered(t *testing.T) {
	t.Parallel()
	unit, err := central.New(central.Config{Name: "ccu-04"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	sup := &clientServiceMessageSuppressor{unit: unit, writer: clientpkg.NewValueWriter()}

	_, err = sup.GetSuppressedServiceMessages(context.Background(), "HmIP-RF", "ABC123:1")
	if !errors.Is(err, ErrServiceMessageSuppressorNoClient) {
		t.Fatalf("expected ErrServiceMessageSuppressorNoClient, got %v", err)
	}
}

// ============================================================
// WireServiceMessageSuppressor — nil-safety + dual-seam wiring
// ============================================================

func TestWireServiceMessageSuppressorNilUnitNoPanic(t *testing.T) {
	t.Parallel()
	WireServiceMessageSuppressor(nil, nil)
	WireServiceMessageSuppressor(nil, clientpkg.NewValueWriter())
}

func TestWireServiceMessageSuppressorNilHubAndHubModelSafe(t *testing.T) {
	t.Parallel()
	unit, err := central.New(central.Config{Name: "ccu-05"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	unit.Hub = nil
	unit.HubModel = nil
	// Must not panic when both seams are absent.
	WireServiceMessageSuppressor(unit, clientpkg.NewValueWriter())
}

func TestWireServiceMessageSuppressorNilServiceMessagesSafe(t *testing.T) {
	t.Parallel()
	unit, err := central.New(central.Config{Name: "ccu-06"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	unit.HubModel.ServiceMessages = nil
	// Must not panic when the hub model has no ServiceMessages aggregate.
	WireServiceMessageSuppressor(unit, clientpkg.NewValueWriter())
}

// TestWireServiceMessageSuppressorWiresBothSeams verifies that a single
// suppressor instance backs both the HubCoordinator seam
// (SetServiceMessageSuppressor / SetServiceMessageReader) and the
// hub.ServiceMessages aggregate's Disable path — the whole point of the
// shared clientServiceMessageSuppressor type.
func TestWireServiceMessageSuppressorWiresBothSeams(t *testing.T) {
	t.Parallel()
	unit, err := central.New(central.Config{Name: "ccu-07"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	ops := &suppressOps{fakeOperations: fakeOperations{kind: backends.KindCCU}}
	w := clientpkg.NewValueWriter()
	wireID := WireInterfaceID("ccu-07", hmenum.InterfaceHmIPRF)
	w.Register("ccu-07", hmtypes.ParseWireInterfaceID(wireID), ops)
	ic := newTestInterfaceClient(t, "ccu-07", "HmIP-RF", 5)
	if err := unit.Clients.Register(&coordinators.ClientEntry{
		InterfaceID: wireID,
		Interface:   hmenum.InterfaceHmIPRF,
		Client:      ic,
	}); err != nil {
		t.Fatalf("Clients.Register: %v", err)
	}

	WireServiceMessageSuppressor(unit, w)

	// Seam 1: HubCoordinator.SuppressServiceMessage.
	if err := unit.Hub.SuppressServiceMessage(context.Background(), "HmIP-RF", "ABC123:1", "LOWBAT", true); err != nil {
		t.Fatalf("HubCoordinator.SuppressServiceMessage: %v", err)
	}
	if len(ops.suppressCalls) != 1 {
		t.Fatalf("expected 1 call via HubCoordinator seam, got %d", len(ops.suppressCalls))
	}

	// Seam 2: hub.ServiceMessages.Disable (used by the REST/WS surfaces).
	unit.HubModel.ServiceMessages.Replace([]hub.ServiceMessage{{
		ID: "svc-1", Address: "ABC123:1", Parameter: "UNREACH",
		InterfaceID: "HmIP-RF",
	}})
	if err := unit.HubModel.ServiceMessages.Disable(context.Background(), "svc-1"); err != nil {
		t.Fatalf("ServiceMessages.Disable: %v", err)
	}
	if len(ops.suppressCalls) != 2 {
		t.Fatalf("expected 2 total calls after Disable via ServiceMessages seam, got %d", len(ops.suppressCalls))
	}

	// Seam 3 (reader half): HubCoordinator.GetSuppressedServiceMessages.
	ops.getResult = []string{"UNREACH"}
	got, err := unit.Hub.GetSuppressedServiceMessages(context.Background(), "HmIP-RF", "ABC123:1")
	if err != nil {
		t.Fatalf("HubCoordinator.GetSuppressedServiceMessages: %v", err)
	}
	if len(got) != 1 || got[0] != "UNREACH" {
		t.Errorf("got %v, want [UNREACH]", got)
	}
}
