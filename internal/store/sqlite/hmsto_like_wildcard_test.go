// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package sqlite

import (
	"context"
	"testing"
	"time"
)

// The device address these stores build a LIKE prefix from is not validated
// anywhere upstream: the cache-clear scope checks only that the field is
// non-empty (internal/central/cachereset/cachereset.go Scope.Validate) and
// the energy handler passes the raw `device` query parameter through. So a
// caller-supplied `%` or `_` reaches the LIKE pattern, and every prefix bind
// in this package must be escaped to match literally — the same rule the
// audit device filter has always applied.
const (
	hmStoWildcardDevice = "%"      // matches every address when unescaped
	hmStoUnderscoreDev  = "A_C123" // '_' matches any single character
	hmStoUnderscorePeer = "AXC123" // ... including this unrelated device
)

// TestParamsetStore_DeleteDevice_WildcardAddressMatchesLiterally verifies
// that a device address carrying LIKE metacharacters deletes only the rows
// of a device literally named that, never every row of the interface.
func TestParamsetStore_DeleteDevice_WildcardAddressMatchesLiterally(t *testing.T) {
	t.Parallel()
	s := freshParamsetStore(t)
	ctx := context.Background()

	upsertDeviceChannels(t, s, "ccu1", "HmIP-RF", "DEVB", []string{"DEVB:0"})
	upsertDeviceChannels(t, s, "ccu1", "HmIP-RF", hmStoUnderscorePeer, []string{hmStoUnderscorePeer + ":0"})

	n, err := s.DeleteDevice(ctx, "ccu1", "HmIP-RF", hmStoWildcardDevice)
	if err != nil {
		t.Fatalf("DeleteDevice(%q): %v", hmStoWildcardDevice, err)
	}
	if n != 0 {
		t.Errorf("DeleteDevice(%q) removed %d rows, want 0", hmStoWildcardDevice, n)
	}

	n, err = s.DeleteDevice(ctx, "ccu1", "HmIP-RF", hmStoUnderscoreDev)
	if err != nil {
		t.Fatalf("DeleteDevice(%q): %v", hmStoUnderscoreDev, err)
	}
	if n != 0 {
		t.Errorf("DeleteDevice(%q) removed %d rows, want 0 (must not match %q)",
			hmStoUnderscoreDev, n, hmStoUnderscorePeer)
	}
}

// TestParamsetStore_GetChannelAddressesByParamsetKey_WildcardAddressMatchesLiterally
// verifies the read path builds a literal prefix too: the statement declares
// ESCAPE '\', so the bound pattern must be escaped.
func TestParamsetStore_GetChannelAddressesByParamsetKey_WildcardAddressMatchesLiterally(t *testing.T) {
	t.Parallel()
	s := freshParamsetStore(t)
	ctx := context.Background()

	upsertDeviceChannels(t, s, "ccu1", "HmIP-RF", "DEVB", []string{"DEVB:0", "DEVB:1"})

	got, err := s.GetChannelAddressesByParamsetKey(ctx, "ccu1", "HmIP-RF", hmStoWildcardDevice)
	if err != nil {
		t.Fatalf("GetChannelAddressesByParamsetKey: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("device %q returned %v, want no channels", hmStoWildcardDevice, got)
	}
}

// TestMasterValues_DeleteDevice_WildcardAddressMatchesLiterally pins the
// MASTER cache side of the same rule.
func TestMasterValues_DeleteDevice_WildcardAddressMatchesLiterally(t *testing.T) {
	t.Parallel()
	s := freshMasterValuesStore(t)
	ctx := context.Background()

	if err := s.SaveChannel(ctx, "ccu1", "HmIP-RF", "DEVB:1", map[string]any{"P": 1}); err != nil {
		t.Fatalf("SaveChannel: %v", err)
	}
	if err := s.DeleteDevice(ctx, "ccu1", "HmIP-RF", hmStoWildcardDevice); err != nil {
		t.Fatalf("DeleteDevice: %v", err)
	}
	_, ok, err := s.LoadChannel(ctx, "ccu1", "HmIP-RF", "DEVB:1")
	if err != nil {
		t.Fatalf("LoadChannel: %v", err)
	}
	if !ok {
		t.Errorf("DeleteDevice(%q) removed the unrelated DEVB:1 row", hmStoWildcardDevice)
	}
}

// TestValuesCache_DeleteDevice_WildcardAddressMatchesLiterally pins the
// VALUES cache side of the same rule.
func TestValuesCache_DeleteDevice_WildcardAddressMatchesLiterally(t *testing.T) {
	t.Parallel()
	s := freshValuesCacheStore(t)
	ctx := context.Background()
	now := time.Now()

	if err := s.SaveValue(ctx, "ccu1", "HmIP-RF", "DEVB:1", "STATE", true, now, now); err != nil {
		t.Fatalf("SaveValue: %v", err)
	}
	if err := s.DeleteDevice(ctx, "ccu1", "HmIP-RF", hmStoWildcardDevice); err != nil {
		t.Fatalf("DeleteDevice: %v", err)
	}
	vals, err := s.LoadChannel(ctx, "ccu1", "HmIP-RF", "DEVB:1")
	if err != nil {
		t.Fatalf("LoadChannel: %v", err)
	}
	if len(vals) == 0 {
		t.Errorf("DeleteDevice(%q) removed the unrelated DEVB:1 values", hmStoWildcardDevice)
	}
}

// TestChannelFlags_DeleteDevice_WildcardAddressMatchesLiterally pins the
// channel-flag side of the same rule.
func TestChannelFlags_DeleteDevice_WildcardAddressMatchesLiterally(t *testing.T) {
	t.Parallel()
	s := freshChannelFlagsStore(t)
	ctx := context.Background()

	if err := s.Set(ctx, "ccu1", "DEVB:1", true, false, "test"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := s.DeleteDevice(ctx, "ccu1", hmStoWildcardDevice); err != nil {
		t.Fatalf("DeleteDevice: %v", err)
	}
	flags, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(flags) != 1 {
		t.Errorf("DeleteDevice(%q) left %d flags, want the unrelated DEVB:1 flag to survive",
			hmStoWildcardDevice, len(flags))
	}
}

// TestRecordingOverrides_DeleteDevice_WildcardAddressMatchesLiterally pins
// the recording-override side of the same rule.
func TestRecordingOverrides_DeleteDevice_WildcardAddressMatchesLiterally(t *testing.T) {
	t.Parallel()
	s := freshRecordingOverrideStore(t)
	ctx := context.Background()

	if err := s.Set(ctx, "ccu1", "HmIP-RF", "DEVB:1", "POWER", true, "test"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := s.DeleteDevice(ctx, "ccu1", "HmIP-RF", hmStoWildcardDevice); err != nil {
		t.Fatalf("DeleteDevice: %v", err)
	}
	list, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("DeleteDevice(%q) left %d overrides, want the unrelated DEVB:1 override to survive",
			hmStoWildcardDevice, len(list))
	}
}

// TestMeasurement_DeleteDevice_WildcardAddressMatchesLiterally pins the
// measurement purge, the destructive end of the cache-clear scope.
func TestMeasurement_DeleteDevice_WildcardAddressMatchesLiterally(t *testing.T) {
	t.Parallel()
	s := freshMeasurementStore(t)
	ctx := context.Background()

	if err := s.SaveBatch(ctx, []MeasurementSample{{
		CentralName: "ccu1", InterfaceID: "HmIP-RF", ChannelAddress: "DEVB:1",
		Parameter: "POWER", TS: msTime(time.Now()), Value: 12,
	}}); err != nil {
		t.Fatalf("SaveBatch: %v", err)
	}
	if err := s.DeleteDevice(ctx, "ccu1", "HmIP-RF", hmStoWildcardDevice); err != nil {
		t.Fatalf("DeleteDevice: %v", err)
	}
	st, err := s.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if st.Rows != 1 {
		t.Errorf("DeleteDevice(%q) left %d measurement rows, want the unrelated DEVB:1 row to survive",
			hmStoWildcardDevice, st.Rows)
	}
}

// TestMeasurement_QueryEnergy_WildcardDeviceMatchesLiterally pins the energy
// read path: a `%` in the `device` query parameter must not report every
// device's channels under the one requested device.
func TestMeasurement_QueryEnergy_WildcardDeviceMatchesLiterally(t *testing.T) {
	t.Parallel()
	s := freshMeasurementStore(t)
	ctx := context.Background()

	base := msTime(time.Now().Add(-2 * time.Hour))
	if err := s.SaveBatch(ctx, []MeasurementSample{{
		CentralName: "ccu1", InterfaceID: "HmIP-RF", ChannelAddress: "DEVB:1",
		Parameter: "POWER", TS: base, Value: 42,
	}}); err != nil {
		t.Fatalf("SaveBatch: %v", err)
	}

	rows, err := s.QueryEnergy(ctx, "ccu1", hmStoWildcardDevice,
		base.Add(-time.Hour), time.Now().Add(time.Hour), "hour")
	if err != nil {
		t.Fatalf("QueryEnergy: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("QueryEnergy(device=%q) returned %d rows, want 0 — the wildcard matched foreign channels",
			hmStoWildcardDevice, len(rows))
	}
}

// hmStoTrailingColonDevice is a device address as it can arrive from an
// unvalidated caller — the cache-clear scope only checks the field is
// non-empty. Before the prefix was single-sourced through channelLikePrefix,
// six of the nine call sites appended a second colon to it and matched
// nothing, while three trimmed it first; these subtests pin every store on
// the trimming behaviour.
const hmStoTrailingColonDevice = "DEVB:"

// TestStores_TrailingColonDeviceAddressMatchesEveryChannel drives each store's
// device-scoped statement through its public method with a trailing-colon
// address and asserts the device's channels are matched.
func TestStores_TrailingColonDeviceAddressMatchesEveryChannel(t *testing.T) {
	t.Parallel()

	t.Run("ParamsetStore.DeleteDevice", func(t *testing.T) {
		t.Parallel()
		s := freshParamsetStore(t)
		upsertDeviceChannels(t, s, "ccu1", "HmIP-RF", "DEVB", []string{"DEVB:0", "DEVB:1"})
		n, err := s.DeleteDevice(context.Background(), "ccu1", "HmIP-RF", hmStoTrailingColonDevice)
		if err != nil {
			t.Fatalf("DeleteDevice(%q): %v", hmStoTrailingColonDevice, err)
		}
		if n == 0 {
			t.Errorf("DeleteDevice(%q) removed 0 rows, want the DEVB channels", hmStoTrailingColonDevice)
		}
	})

	t.Run("ParamsetStore.GetChannelAddressesByParamsetKey", func(t *testing.T) {
		t.Parallel()
		s := freshParamsetStore(t)
		upsertDeviceChannels(t, s, "ccu1", "HmIP-RF", "DEVB", []string{"DEVB:0", "DEVB:1"})
		got, err := s.GetChannelAddressesByParamsetKey(context.Background(), "ccu1", "HmIP-RF", hmStoTrailingColonDevice)
		if err != nil {
			t.Fatalf("GetChannelAddressesByParamsetKey: %v", err)
		}
		if len(got) == 0 {
			t.Errorf("device %q returned no channels, want the DEVB channels", hmStoTrailingColonDevice)
		}
	})

	t.Run("MasterValuesStore.DeleteDevice", func(t *testing.T) {
		t.Parallel()
		s := freshMasterValuesStore(t)
		ctx := context.Background()
		if err := s.SaveChannel(ctx, "ccu1", "HmIP-RF", "DEVB:1", map[string]any{"P": 1}); err != nil {
			t.Fatalf("SaveChannel: %v", err)
		}
		if err := s.DeleteDevice(ctx, "ccu1", "HmIP-RF", hmStoTrailingColonDevice); err != nil {
			t.Fatalf("DeleteDevice: %v", err)
		}
		_, ok, err := s.LoadChannel(ctx, "ccu1", "HmIP-RF", "DEVB:1")
		if err != nil {
			t.Fatalf("LoadChannel: %v", err)
		}
		if ok {
			t.Errorf("DeleteDevice(%q) left the DEVB:1 row", hmStoTrailingColonDevice)
		}
	})

	t.Run("ValuesCacheStore.DeleteDevice", func(t *testing.T) {
		t.Parallel()
		s := freshValuesCacheStore(t)
		ctx := context.Background()
		now := time.Now()
		if err := s.SaveValue(ctx, "ccu1", "HmIP-RF", "DEVB:1", "STATE", true, now, now); err != nil {
			t.Fatalf("SaveValue: %v", err)
		}
		if err := s.DeleteDevice(ctx, "ccu1", "HmIP-RF", hmStoTrailingColonDevice); err != nil {
			t.Fatalf("DeleteDevice: %v", err)
		}
		vals, err := s.LoadChannel(ctx, "ccu1", "HmIP-RF", "DEVB:1")
		if err != nil {
			t.Fatalf("LoadChannel: %v", err)
		}
		if len(vals) != 0 {
			t.Errorf("DeleteDevice(%q) left %d DEVB:1 values", hmStoTrailingColonDevice, len(vals))
		}
	})

	t.Run("ChannelFlagsStore.DeleteDevice", func(t *testing.T) {
		t.Parallel()
		s := freshChannelFlagsStore(t)
		ctx := context.Background()
		if err := s.Set(ctx, "ccu1", "DEVB:1", true, false, "test"); err != nil {
			t.Fatalf("Set: %v", err)
		}
		if err := s.DeleteDevice(ctx, "ccu1", hmStoTrailingColonDevice); err != nil {
			t.Fatalf("DeleteDevice: %v", err)
		}
		flags, err := s.List(ctx)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(flags) != 0 {
			t.Errorf("DeleteDevice(%q) left %d flags, want the DEVB:1 flag gone",
				hmStoTrailingColonDevice, len(flags))
		}
	})

	t.Run("RecordingOverrideStore.DeleteDevice", func(t *testing.T) {
		t.Parallel()
		s := freshRecordingOverrideStore(t)
		ctx := context.Background()
		if err := s.Set(ctx, "ccu1", "HmIP-RF", "DEVB:1", "POWER", true, "test"); err != nil {
			t.Fatalf("Set: %v", err)
		}
		if err := s.DeleteDevice(ctx, "ccu1", "HmIP-RF", hmStoTrailingColonDevice); err != nil {
			t.Fatalf("DeleteDevice: %v", err)
		}
		list, err := s.List(ctx)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(list) != 0 {
			t.Errorf("DeleteDevice(%q) left %d overrides, want the DEVB:1 override gone",
				hmStoTrailingColonDevice, len(list))
		}
	})

	t.Run("MeasurementStore.DeleteDevice", func(t *testing.T) {
		t.Parallel()
		s := freshMeasurementStore(t)
		ctx := context.Background()
		if err := s.SaveBatch(ctx, []MeasurementSample{{
			CentralName: "ccu1", InterfaceID: "HmIP-RF", ChannelAddress: "DEVB:1",
			Parameter: "POWER", TS: msTime(time.Now()), Value: 12,
		}}); err != nil {
			t.Fatalf("SaveBatch: %v", err)
		}
		if err := s.DeleteDevice(ctx, "ccu1", "HmIP-RF", hmStoTrailingColonDevice); err != nil {
			t.Fatalf("DeleteDevice: %v", err)
		}
		st, err := s.Stats(ctx)
		if err != nil {
			t.Fatalf("Stats: %v", err)
		}
		if st.Rows != 0 {
			t.Errorf("DeleteDevice(%q) left %d measurement rows, want the DEVB:1 row gone",
				hmStoTrailingColonDevice, st.Rows)
		}
	})

	t.Run("MeasurementStore.QueryEnergy", func(t *testing.T) {
		t.Parallel()
		s := freshMeasurementStore(t)
		ctx := context.Background()
		base := msTime(time.Now().Add(-2 * time.Hour))
		if err := s.SaveBatch(ctx, []MeasurementSample{{
			CentralName: "ccu1", InterfaceID: "HmIP-RF", ChannelAddress: "DEVB:1",
			Parameter: "POWER", TS: base, Value: 42,
		}}); err != nil {
			t.Fatalf("SaveBatch: %v", err)
		}
		rows, err := s.QueryEnergy(ctx, "ccu1", hmStoTrailingColonDevice,
			base.Add(-time.Hour), time.Now().Add(time.Hour), "hour")
		if err != nil {
			t.Fatalf("QueryEnergy: %v", err)
		}
		if len(rows) == 0 {
			t.Errorf("QueryEnergy(device=%q) returned 0 rows, want the DEVB:1 samples",
				hmStoTrailingColonDevice)
		}
	})
}
