// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

func upsertDeviceChannels(t *testing.T, s *ParamsetStore, central, iface, deviceAddr string, channels []string) {
	t.Helper()
	for _, ch := range channels {
		rec := ParamsetRecord{
			CentralName:    central,
			InterfaceID:    iface,
			ChannelAddress: ch,
			ParamsetKey:    hmenum.ParamsetKeyMaster,
			Hash:           "h1",
			Paramset: hmproto.Paramset{
				"LEVEL": {Type: hmenum.ParameterTypeFloat},
			},
		}
		if err := s.Upsert(context.Background(), rec); err != nil {
			t.Fatalf("Upsert %s: %v", ch, err)
		}
	}
}

// TestParamsetStore_DeleteDevice_ZeroRowsNoOp verifies that calling
// DeleteDevice for a device that does not exist returns (0, nil) and
// leaves the cache intact.
func TestParamsetStore_DeleteDevice_ZeroRowsNoOp(t *testing.T) {
	t.Parallel()
	s := freshParamsetStore(t)
	ctx := context.Background()

	n, err := s.DeleteDevice(ctx, "ccu1", "HmIP-RF", "GHOST")
	if err != nil {
		t.Fatalf("DeleteDevice non-existent device: %v", err)
	}
	if n != 0 {
		t.Errorf("rows affected = %d, want 0", n)
	}
}

// TestParamsetStore_DeleteDevice verifies that DeleteDevice removes every
// channel row for the device, leaves unrelated rows intact and reports the
// correct affected-row count.
func TestParamsetStore_DeleteDevice(t *testing.T) {
	t.Parallel()
	s := freshParamsetStore(t)
	ctx := context.Background()

	// Device A: two channels (the delete target).
	upsertDeviceChannels(t, s, "ccu1", "HmIP-RF", "DEVA", []string{"DEVA:0", "DEVA:1"})
	// Device B: one channel (must survive).
	upsertDeviceChannels(t, s, "ccu1", "HmIP-RF", "DEVB", []string{"DEVB:0"})

	n, err := s.DeleteDevice(ctx, "ccu1", "HmIP-RF", "DEVA")
	if err != nil {
		t.Fatalf("DeleteDevice: %v", err)
	}
	if n != 2 {
		t.Errorf("rows affected = %d, want 2", n)
	}

	if _, err := s.Get(ctx, "ccu1", "HmIP-RF", "DEVA:0", hmenum.ParamsetKeyMaster); err == nil {
		t.Error("Get DEVA:0 after DeleteDevice returned nil error, want ErrParamsetNotFound")
	}
	if _, err := s.Get(ctx, "ccu1", "HmIP-RF", "DEVB:0", hmenum.ParamsetKeyMaster); err != nil {
		t.Errorf("Get DEVB:0 after DeleteDevice: %v", err)
	}
}
