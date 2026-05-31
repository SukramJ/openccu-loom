// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestServiceMethodScalarArgPinned pins ADR 0009's contract: the
// payload-decoder helpers in `internal/payload/params.go` recognise
// the canonical argument keys for every standard service method.
//
// Renaming a service method (e.g. `set_temperature` → `set_target`)
// is an HA-Discovery-payload-shape change because the operator-
// visible `*_command_topic` URL contains the method name. This test
// ensures the rename is loud: anything that touches the method name
// or its expected param key has to update both call sites.
//
// `kind` selects the decoder (`payload.Param*`) by the Go type the
// service-method handler expects.
func TestServiceMethodScalarArgPinned(t *testing.T) {
	t.Parallel()

	type kind int
	const (
		kFloat kind = iota
		kString
		kInt32
		kBool
	)

	cases := []struct {
		method   string
		paramKey string
		paramVal any
		k        kind
	}{
		// Climate
		{"set_temperature", "temperature", 21.5, kFloat},
		{"set_mode", "mode", "heat", kString},
		{"set_profile", "profile", "boost", kString},
		{"set_temperature_offset", "offset", -1.5, kFloat},

		// Cover / Blind
		{"set_position", "position", 75.0, kFloat},
		{"set_tilt", "tilt", 30.0, kFloat},

		// Light
		{"set_level", "level", 0.5, kFloat},
		{"set_kelvin", "kelvin", int32(3000), kInt32},
		{"set_effect", "effect", "SLOW_COLOR_CHANGE", kString},

		// TextDisplay
		{"set_text", "text", "hello", kString},

		// Generic-DP fallback
		{"set", "value", true, kBool},
	}

	decode := func(k kind, params map[string]any, key string) (any, error) {
		switch k {
		case kFloat:
			return payload.ParamFloat64(params, key)
		case kString:
			return payload.ParamString(params, key)
		case kInt32:
			return payload.ParamInt32(params, key)
		case kBool:
			return payload.ParamBool(params, key)
		}
		return nil, errors.New("unknown kind")
	}

	for _, c := range cases {
		c := c
		t.Run(c.method+"/"+c.paramKey, func(t *testing.T) {
			t.Parallel()
			params := map[string]any{c.paramKey: c.paramVal}
			if _, err := decode(c.k, params, c.paramKey); err != nil {
				t.Fatalf("decode %s/%s: unexpected error %v", c.method, c.paramKey, err)
			}
		})
	}

	// Missing-key path: a key the call site did not provide produces
	// ErrServiceMissingParam.
	t.Run("missing_key", func(t *testing.T) {
		t.Parallel()
		_, err := payload.ParamFloat64(map[string]any{"wrong_key": 1.0}, "temperature")
		if !errors.Is(err, payload.ErrServiceMissingParam) {
			t.Errorf("expected ErrServiceMissingParam, got %v", err)
		}
	})
}

// TestServiceMethodInvokeContract pins the Source.Invoke wiring:
// every service method registered through ServiceRegistry must be
// callable through Invoke, and unknown methods must return
// ErrUnknownServiceMethod (errors.Is-matchable).
func TestServiceMethodInvokeContract(t *testing.T) {
	t.Parallel()

	var reg payload.ServiceRegistry
	reg.RegisterService("noop", func(_ context.Context, _ map[string]any, _ hmenum.CommandPriority) error {
		return nil
	})

	if err := reg.Invoke(context.Background(), "noop", nil, hmenum.CommandPriorityHigh); err != nil {
		t.Errorf("invoke noop: unexpected error %v", err)
	}

	err := reg.Invoke(context.Background(), "ghost", nil, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("invoke ghost: expected error, got nil")
	}
	if !errors.Is(err, payload.ErrUnknownServiceMethod) {
		t.Errorf("invoke ghost: expected ErrUnknownServiceMethod, got %v", err)
	}
}
