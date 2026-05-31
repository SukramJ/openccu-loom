// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package coordinators

// connection_recovery_probes_test.go — migrates the "probe injection"
// Sub-group from py
//
//   - test_check_rpc_available_*  (6 tests)
//   - test_check_tcp_port_available_* (3 tests)
//   - test_stage_reconnect_* (2 tests)
//   - test_stage_rpc_check_* (2 tests)
//   - test_stage_stability_check_* (2 tests)
//
// The six tcp-stage port-selection tests (test_stage_tcp_check_*)
// are not migrated: they test the private helper _stage_tcp_check's
// port-discovery logic (get_client_port / JSON-RPC fallback / config
// fallback). That logic lives inside the InterfaceClient in Go and is
// not exposed through RecoveryStageDeps — the TCPProbe field is a
// caller-supplied closure, so port selection is the caller's concern.
// Tests for that belong in the client package, not here.
//
// Mapping table (21 Python tests):
//
//	test_check_rpc_available_exception               → migrated (TestRPCProbeInjection/error_propagated)
//	test_check_rpc_available_json_rpc_only_interface → skip: tests InterfaceClient detail, not probe seam
//	test_check_rpc_available_no_proxy_found          → skip: tests private _check_rpc_available, no Go pendant
//	test_check_rpc_available_proxy_without_reset_transport → skip: tests private transport reset, no Go pendant
//	test_check_rpc_available_with_backend_proxy      → skip: tests private proxy attribute discovery
//	test_check_rpc_available_with_direct_proxy       → skip: tests private proxy attribute discovery
//	test_check_tcp_port_available_os_error           → migrated (TestTCPProbeInjection/os_error_fails_pipeline)
//	test_check_tcp_port_available_success            → migrated (TestTCPProbeInjection/success_continues_pipeline)
//	test_check_tcp_port_available_timeout            → migrated (TestTCPProbeInjection/context_timeout_fails_pipeline)
//	test_stage_reconnect_failure_client_not_available → migrated (TestReconnectProbeInjection/probe_error_fails_pipeline)
//	test_stage_reconnect_success                     → migrated (TestReconnectProbeInjection/probe_success_continues)
//	test_stage_rpc_check_failure                     → migrated (TestRPCProbeInjection/error_propagated)
//	test_stage_rpc_check_success                     → migrated (TestRPCProbeInjection/nil_error_succeeds)
//	test_stage_stability_check_failure               → migrated (TestStabilityProbeInjection/error_propagated)
//	test_stage_stability_check_success               → migrated (TestStabilityProbeInjection/nil_error_succeeds)
//	test_stage_tcp_check_client_json_rpc_fallback    → skip: port-fallback logic lives in InterfaceClient
//	test_stage_tcp_check_json_rpc_port_fallback      → skip: port-fallback logic lives in InterfaceClient
//	test_stage_tcp_check_json_rpc_port_from_config   → skip: port-from-config logic lives in InterfaceClient
//	test_stage_tcp_check_no_port_configured          → skip: port-discovery logic lives in InterfaceClient
//	test_stage_tcp_check_port_from_interface_config  → skip: port-from-config logic lives in InterfaceClient
//	test_stage_tcp_check_timeout                     → migrated (TestTCPProbeInjection/probe_returns_error_fails_pipeline)

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// errProbeFailure is a reusable non-nil error for probe-injection tests.
var errProbeFailure = errors.New("probe: simulated failure")

// TestTCPProbeInjection mirrors test_check_tcp_port_available_* and
// Test_stage_tcp_check_timeout. All four cases are
// expressed as pipeline runs so the assertion is always against the
// public Run API, not internal helpers.
//
// Migrated cases:
//   - test_check_tcp_port_available_os_error
//   - test_check_tcp_port_available_success
//   - test_check_tcp_port_available_timeout (via context deadline exceeded)
//   - test_stage_tcp_check_timeout (probe returns error → pipeline fails)
func TestTCPProbeInjection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		probe      func(context.Context) error
		wantResult hmenum.RecoveryResult
	}{
		{
			name:       "os_error_fails_pipeline",
			probe:      func(_ context.Context) error { return errors.New("dial tcp: connection refused") },
			wantResult: hmenum.RecoveryResultFailed,
		},
		{
			name:       "success_continues_pipeline",
			probe:      func(_ context.Context) error { return nil },
			wantResult: hmenum.RecoveryResultSuccess,
		},
		{
			name: "context_timeout_fails_pipeline",
			probe: func(_ context.Context) error {
				return context.DeadlineExceeded
			},
			wantResult: hmenum.RecoveryResultFailed,
		},
		{
			name:       "probe_returns_error_fails_pipeline",
			probe:      func(_ context.Context) error { return errProbeFailure },
			wantResult: hmenum.RecoveryResultFailed,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			bus := events.NewBus()
			c := NewConnectionRecoveryCoordinator("ccu-test", bus)

			pipeline := DefaultRecoveryPipeline(RecoveryStageDeps{
				TCPProbe: tc.probe,
			})

			got := c.Run(context.Background(), "HmIP-RF", pipeline)
			if got != tc.wantResult {
				t.Errorf("result = %s, want %s", got, tc.wantResult)
			}
		})
	}
}

// TestRPCProbeInjection mirrors test_check_rpc_available_exception,
// test_stage_rpc_check_failure, and test_stage_rpc_check_success.
// (test_check_rpc_available_with_direct_proxy,
// test_check_rpc_available_with_backend_proxy, etc.) have no Go
// pendant because the InterfaceClient encapsulates transport selection.
func TestRPCProbeInjection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		probe      func(context.Context) error
		wantResult hmenum.RecoveryResult
	}{
		{
			name:       "error_propagated",
			probe:      func(_ context.Context) error { return errors.New("rpc: listMethods failed") },
			wantResult: hmenum.RecoveryResultFailed,
		},
		{
			name:       "nil_error_succeeds",
			probe:      func(_ context.Context) error { return nil },
			wantResult: hmenum.RecoveryResultSuccess,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			bus := events.NewBus()
			c := NewConnectionRecoveryCoordinator("ccu-test", bus)

			pipeline := DefaultRecoveryPipeline(RecoveryStageDeps{
				RPCProbe: tc.probe,
			})

			got := c.Run(context.Background(), "HmIP-RF", pipeline)
			if got != tc.wantResult {
				t.Errorf("result = %s, want %s", got, tc.wantResult)
			}
		})
	}
}

// TestStabilityProbeInjection mirrors test_stage_stability_check_failure
// And test_stage_stability_check_success.
func TestStabilityProbeInjection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		probe      func(context.Context) error
		wantResult hmenum.RecoveryResult
	}{
		{
			name:       "error_propagated",
			probe:      func(_ context.Context) error { return errors.New("stability: rpc check failed") },
			wantResult: hmenum.RecoveryResultFailed,
		},
		{
			name:       "nil_error_succeeds",
			probe:      func(_ context.Context) error { return nil },
			wantResult: hmenum.RecoveryResultSuccess,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			bus := events.NewBus()
			c := NewConnectionRecoveryCoordinator("ccu-test", bus)

			pipeline := DefaultRecoveryPipeline(RecoveryStageDeps{
				StabilityProbe: tc.probe,
			})

			got := c.Run(context.Background(), "HmIP-RF", pipeline)
			if got != tc.wantResult {
				t.Errorf("result = %s, want %s", got, tc.wantResult)
			}
		})
	}
}

// TestReconnectProbeInjection mirrors test_stage_reconnect_success and
// Test_stage_reconnect_failure_client_not_available.
// In Python the "client not available" case sets mock_client.available=False
// and the stage helper checks it; in Go the reconnect logic is the caller's
// responsibility — the injected Reconnect probe is the contract point.
func TestReconnectProbeInjection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		probe      func(context.Context) error
		wantResult hmenum.RecoveryResult
	}{
		{
			name:       "probe_error_fails_pipeline",
			probe:      func(_ context.Context) error { return errors.New("reconnect: client not available") },
			wantResult: hmenum.RecoveryResultFailed,
		},
		{
			name:       "probe_success_continues",
			probe:      func(_ context.Context) error { return nil },
			wantResult: hmenum.RecoveryResultSuccess,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			bus := events.NewBus()
			c := NewConnectionRecoveryCoordinator("ccu-test", bus)

			pipeline := DefaultRecoveryPipeline(RecoveryStageDeps{
				Reconnect: tc.probe,
			})

			got := c.Run(context.Background(), "HmIP-RF", pipeline)
			if got != tc.wantResult {
				t.Errorf("result = %s, want %s", got, tc.wantResult)
			}
		})
	}
}
