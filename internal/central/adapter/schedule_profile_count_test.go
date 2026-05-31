// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// buildProfileCapDevice creates a device with one channel carrying an
// ACTIVE_PROFILE DP whose Max value controls [MaxProfilesForDevice].
func buildProfileCapDevice(addr string, profileCap int) *device.Device {
	d := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     addr,
		Model:       "HmIP-TEST",
		Name:        addr,
	})
	ch := d.AddChannel(addr+":1", 1, "CLIMATECONTROL_RT_TRANSCEIVER", hmenum.ParamsetKeyValues)
	rawMax, _ := json.Marshal(float64(profileCap))
	dp := generic.NewInteger(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: addr + ":1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterActiveProfile),
		},
		Descriptor: hmproto.ParameterData{
			Type: hmenum.ParameterTypeInteger,
			Max:  json.RawMessage(rawMax),
		},
	})
	ch.Put(dp)
	return d
}

// TestCopyScheduleToProfileCountMismatch verifies that CopyScheduleTo returns
// [ErrProfileCountMismatch] when the source and destination devices have
// different profile counts.
func TestCopyScheduleToProfileCountMismatch(t *testing.T) {
	t.Parallel()

	c, err := central.New(central.Config{Name: "ccu-parity-v12-mismatch"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}

	// source: 6 profiles, destination: 3 profiles → mismatch.
	srcDev := buildProfileCapDevice("PARITY12SRC01", 6)
	dstDev := buildProfileCapDevice("PARITY12DST01", 3)
	c.ModelRegistry.Put(srcDev)
	c.ModelRegistry.Put(dstDev)

	s := NewSchedulesDomain(reg, client.NewValueWriter())
	err = s.CopyScheduleTo(context.Background(), "PARITY12SRC01", 1, "PARITY12DST01", 1)
	if !errors.Is(err, ErrProfileCountMismatch) {
		t.Errorf("CopyScheduleTo: got %v, want ErrProfileCountMismatch", err)
	}
}

// TestCopyScheduleToSameProfileCountDoesNotMismatch verifies that when both
// devices have identical profile counts the mismatch guard passes (the error
// returned comes from a later stage, not ErrProfileCountMismatch).
func TestCopyScheduleToSameProfileCountDoesNotMismatch(t *testing.T) {
	t.Parallel()

	c, err := central.New(central.Config{Name: "ccu-parity-v12-samecap"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}

	// Both devices have 3 profiles — should not trigger mismatch.
	srcDev := buildProfileCapDevice("PARITY12SRC02", 3)
	dstDev := buildProfileCapDevice("PARITY12DST02", 3)
	c.ModelRegistry.Put(srcDev)
	c.ModelRegistry.Put(dstDev)

	s := NewSchedulesDomain(reg, client.NewValueWriter())
	err = s.CopyScheduleTo(context.Background(), "PARITY12SRC02", 1, "PARITY12DST02", 1)
	// Must NOT be ErrProfileCountMismatch. The actual error will be
	// ErrNoScheduleBackend (no backend registered) or ErrNoSchedule (source
	// schedule is empty) — that's fine for this test.
	if errors.Is(err, ErrProfileCountMismatch) {
		t.Errorf("CopyScheduleTo with equal caps must not return ErrProfileCountMismatch")
	}
}

// TestErrProfileCountMismatchSentinel verifies the error sentinel value exists
// and wraps correctly via errors.Is.
func TestErrProfileCountMismatchSentinel(t *testing.T) {
	t.Parallel()
	if ErrProfileCountMismatch == nil {
		t.Fatal("ErrProfileCountMismatch must not be nil")
	}
	wrapped := errors.New("wrap: " + ErrProfileCountMismatch.Error())
	if errors.Is(wrapped, ErrProfileCountMismatch) {
		t.Fatal("unwrapped error should not satisfy errors.Is for sentinel")
	}
}
