// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// TestEnsureDisconnectedClientStateClassifiesCause pins that a failed
// bring-up names its own cause. The interface state is what REST, WS and the
// SPA render for a CCU that never came up, and rejected credentials need a
// different operator response than an unreachable host — reporting every
// failure as "network" sends the operator to the wrong place.
func TestEnsureDisconnectedClientStateClassifiesCause(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.DiscardHandler)

	for _, tc := range []struct {
		name  string
		cause error
		want  hmenum.FailureReason
	}{
		{"auth", fmt.Errorf("init: %w", hmerr.ErrAuthFailure), hmenum.FailureReasonAuth},
		{"no connection", fmt.Errorf("init: %w", hmerr.ErrNoConnection), hmenum.FailureReasonNetwork},
		{"circuit breaker", hmerr.ErrCircuitBreakerOpen, hmenum.FailureReasonCircuitBreaker},
		{"unclassified", errors.New("boom"), hmenum.FailureReasonNetwork},
		{"none", nil, hmenum.FailureReasonNetwork},
	} {
		ic, err := client.New(client.Config{
			CentralName: "ccu-01",
			Interface:   hmenum.InterfaceHmIPRF,
			Caller: client.CallerFunc(func(context.Context, string, []any) (any, error) {
				return nil, nil
			}),
		})
		if err != nil {
			t.Fatalf("%s: client.New: %v", tc.name, err)
		}
		ensureDisconnectedClientState(ic, tc.cause, logger)
		if got := ic.StateMachine().State(); got != hmenum.ClientStateDisconnected {
			t.Errorf("%s: state = %v, want disconnected", tc.name, got)
		}
		if got := ic.FailureReason(); got != tc.want {
			t.Errorf("%s: failure reason = %v, want %v", tc.name, got, tc.want)
		}
	}
}
