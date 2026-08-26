// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// incident_parity_test.go — unit tests for IncidentType constants.

package hmenum_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestIncidentTypeParityConstants verifies that the five new IncidentType
// constants have the correct wire values and that String()
// returns the underlying value.
func TestIncidentTypeParityConstants(t *testing.T) {
	t.Parallel()

	cases := []struct {
		id   string
		got  hmenum.IncidentType
		want string
	}{
		// RPC_ERROR
		{"M6057", hmenum.IncidentTypeRPCError, "rpc_error"},
		// CALLBACK_TIMEOUT
		{"M6058", hmenum.IncidentTypeCallbackTimeout, "callback_timeout"},
		// CIRCUIT_BREAKER_TRIPPED — circuit breaker opened due to
		// excessive failures.
		{"M6059", hmenum.IncidentTypeCircuitBreakerTripped, "circuit_breaker_tripped"},
		// CIRCUIT_BREAKER_RECOVERED — circuit breaker closed after
		// successful probes.
		{"M6060", hmenum.IncidentTypeCircuitBreakerRecovered, "circuit_breaker_recovered"},
		// PARAMSET_INCONSISTENCY
		{"M6061", hmenum.IncidentTypeParamsetInconsistency, "paramset_inconsistency"},
	}

	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			t.Parallel()
			if string(tc.got) != tc.want {
				t.Errorf("IncidentType value=%q want %q", string(tc.got), tc.want)
			}
			if tc.got.String() != tc.want {
				t.Errorf("IncidentType.String()=%q want %q", tc.got.String(), tc.want)
			}
		})
	}
}

// TestIncidentTypeParityDistinct checks that the five new constants are
// distinct from each other and from the pre-existing constants.
func TestIncidentTypeParityDistinct(t *testing.T) {
	t.Parallel()

	all := []hmenum.IncidentType{
		hmenum.IncidentTypeAuthFailure,
		hmenum.IncidentTypeConnectionLost,
		hmenum.IncidentTypeCircuitBreakerOpen,
		hmenum.IncidentTypePingPongMismatch,
		hmenum.IncidentTypeRPCFault,
		hmenum.IncidentTypeRecoveryFailed,
		hmenum.IncidentTypeParamsetPatch,
		hmenum.IncidentTypeDeviceProfileMiss,
		hmenum.IncidentTypeConfigError,
		hmenum.IncidentTypeRPCError,
		hmenum.IncidentTypeCallbackTimeout,
		hmenum.IncidentTypeCircuitBreakerTripped,
		hmenum.IncidentTypeCircuitBreakerRecovered,
		hmenum.IncidentTypeParamsetInconsistency,
	}

	seen := make(map[hmenum.IncidentType]struct{}, len(all))
	for _, v := range all {
		if _, dup := seen[v]; dup {
			t.Errorf("duplicate IncidentType value: %q", v)
		}
		seen[v] = struct{}{}
	}
}
