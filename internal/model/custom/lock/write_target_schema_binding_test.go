// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package lock_test

import (
	"context"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/custom/lock"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// lockParamRecorder records every parameter the lock writes.
type lockParamRecorder struct {
	mu     sync.Mutex
	params []hmenum.Parameter
}

func (r *lockParamRecorder) SetValue(
	_ context.Context, _ string, parameter hmenum.Parameter, _ any, _ hmenum.CommandPriority,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.params = append(r.params, parameter)
	return nil
}

func (r *lockParamRecorder) PutParamset(
	_ context.Context, _ string, _ hmenum.ParamsetKey, _ map[string]any, _ hmenum.CommandPriority,
) error {
	return nil
}

func (r *lockParamRecorder) wrote(parameter hmenum.Parameter) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.params {
		if p == parameter {
			return true
		}
	}
	return false
}

// TestIPLockCommandFollowsTheProfileSchema pins the HmIP lock command to the
// parameter the profile's channel-group schema names. Every shipping profile
// happens to name LOCK_TARGET_LEVEL, so a hard-coded write is
// indistinguishable from a schema-driven one until a schema names something
// else — which is what this test does.
func TestIPLockCommandFollowsTheProfileSchema(t *testing.T) {
	t.Parallel()
	dev := device.New(device.Config{
		InterfaceID:  "HmIP-RF",
		Interface:    hmenum.InterfaceHmIPRF,
		Address:      "SCHEMALOCK",
		ProductGroup: hmenum.ProductGroupHmIP,
	})
	dev.AddChannel("SCHEMALOCK:0", 0, "MAINTENANCE", hmenum.ParamsetKeyValues)
	ch := dev.AddChannel("SCHEMALOCK:1", 1, "LOCK", hmenum.ParamsetKeyValues)

	w := &lockParamRecorder{}
	l := lock.New(lock.Config{
		Channel: ch,
		Writer:  w,
		Kind:    lock.KindIP,
		Group: custom.RebasedChannelGroupConfig{
			Fields: map[hmenum.Field]custom.FieldValue{
				hmenum.FieldLockTargetLevel: custom.Bare(hmenum.ParameterOpen),
			},
		},
	})
	if err := l.Lock(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("Lock: %v", err)
	}
	if !w.wrote(hmenum.ParameterOpen) {
		t.Fatalf("Lock command wrote %v; the schema named %s", w.params, hmenum.ParameterOpen)
	}
}
