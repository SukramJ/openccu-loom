// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Tests for convert.go (ParseDeviceDescriptions, normaliseStringSlice,
// normaliseBoolFields, toDeviceDescription) and for the ErrUnsupported stubs
// on CuxdBackend and HomegearBackend that are not yet covered by other files.

package backends

import (
	"context"
	"errors"
	"testing"
)

// ---------------------------------------------------------------------------
// ParseDeviceDescriptions
// ---------------------------------------------------------------------------

func TestParseDeviceDescriptionsBasic(t *testing.T) {
	t.Parallel()
	raw := []any{
		map[string]any{
			"ADDRESS":   "0001AABB",
			"TYPE":      "HmIP-STH",
			"PARAMSETS": []any{"MASTER", "VALUES"},
		},
	}
	out := ParseDeviceDescriptions(raw)
	if len(out) != 1 {
		t.Fatalf("len=%d, want 1", len(out))
	}
	if out[0].Address != "0001AABB" {
		t.Fatalf("Address=%s", out[0].Address)
	}
	if out[0].Type != "HmIP-STH" {
		t.Fatalf("Type=%s", out[0].Type)
	}
}

func TestParseDeviceDescriptionsSkipsNonMap(t *testing.T) {
	t.Parallel()
	raw := []any{
		"not a map",
		map[string]any{"ADDRESS": "0001AABB", "TYPE": "HmIP-STH"},
	}
	out := ParseDeviceDescriptions(raw)
	if len(out) != 1 {
		t.Fatalf("len=%d, want 1 (non-map skipped)", len(out))
	}
}

func TestParseDeviceDescriptionsEmpty(t *testing.T) {
	t.Parallel()
	out := ParseDeviceDescriptions([]any{})
	if len(out) != 0 {
		t.Fatalf("len=%d, want 0", len(out))
	}
}

func TestParseDeviceDescriptionsNormalisesStringParamsets(t *testing.T) {
	t.Parallel()
	// PARAMSETS as a single string should be wrapped into a []string.
	raw := []any{
		map[string]any{
			"ADDRESS":   "AABB",
			"TYPE":      "HmIP-PSM",
			"PARAMSETS": "VALUES",
		},
	}
	out := ParseDeviceDescriptions(raw)
	if len(out) != 1 {
		t.Fatalf("len=%d, want 1", len(out))
	}
}

func TestParseDeviceDescriptionsNormalisesBoolAsInt(t *testing.T) {
	t.Parallel()
	// ROAMING as int 1 should be treated as true.
	raw := []any{
		map[string]any{
			"ADDRESS": "CCDD",
			"TYPE":    "HmIP-WRC2",
			"ROAMING": 1,
		},
	}
	out := ParseDeviceDescriptions(raw)
	if len(out) != 1 {
		t.Fatalf("len=%d, want 1", len(out))
	}
}

func TestParseDeviceDescriptionsNormalisesZeroParamsetString(t *testing.T) {
	t.Parallel()
	// PARAMSETS as an empty string should become an empty slice.
	raw := []any{
		map[string]any{
			"ADDRESS":   "EEFF",
			"TYPE":      "HmIP-TEST",
			"PARAMSETS": "",
		},
	}
	out := ParseDeviceDescriptions(raw)
	if len(out) != 1 {
		t.Fatalf("len=%d, want 1", len(out))
	}
	if len(out[0].Paramsets) != 0 {
		t.Fatalf("Paramsets=%v, want empty", out[0].Paramsets)
	}
}

func TestParseDeviceDescriptionsNormalisesBoolAsFloat64(t *testing.T) {
	t.Parallel()
	raw := []any{
		map[string]any{
			"ADDRESS": "FF00",
			"TYPE":    "HmIP-X",
			"ROAMING": float64(1),
		},
	}
	out := ParseDeviceDescriptions(raw)
	if len(out) != 1 {
		t.Fatalf("len=%d, want 1", len(out))
	}
}

func TestParseDeviceDescriptionsNormalisesInt32Bool(t *testing.T) {
	t.Parallel()
	raw := []any{
		map[string]any{
			"ADDRESS": "FF01",
			"TYPE":    "HmIP-Y",
			"ROAMING": int32(0),
		},
	}
	out := ParseDeviceDescriptions(raw)
	if len(out) != 1 {
		t.Fatalf("len=%d, want 1", len(out))
	}
}

func TestParseDeviceDescriptionsNormalisesInt64Bool(t *testing.T) {
	t.Parallel()
	raw := []any{
		map[string]any{
			"ADDRESS": "FF02",
			"TYPE":    "HmIP-Z",
			"ROAMING": int64(1),
		},
	}
	out := ParseDeviceDescriptions(raw)
	if len(out) != 1 {
		t.Fatalf("len=%d, want 1", len(out))
	}
}

// ---------------------------------------------------------------------------
// CuxdBackend — ErrUnsupported stubs
// ---------------------------------------------------------------------------

func TestCuxdGetAllPrograms(t *testing.T) {
	t.Parallel()
	b := NewCuxdBackend(&fakeCaller{}, nil)
	_, err := b.GetAllPrograms(context.Background())
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCuxdSetProgramState(t *testing.T) {
	t.Parallel()
	b := NewCuxdBackend(&fakeCaller{}, nil)
	err := b.SetProgramState(context.Background(), "1", true)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCuxdGetSystemUpdateInfo(t *testing.T) {
	t.Parallel()
	b := NewCuxdBackend(&fakeCaller{}, nil)
	_, err := b.GetSystemUpdateInfo(context.Background())
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCuxdGetInboxDevices(t *testing.T) {
	t.Parallel()
	b := NewCuxdBackend(&fakeCaller{}, nil)
	_, err := b.GetInboxDevices(context.Background(), "HmIP-RF")
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCuxdSetSystemVariable(t *testing.T) {
	t.Parallel()
	b := NewCuxdBackend(&fakeCaller{}, nil)
	err := b.SetSystemVariable(context.Background(), "v", true)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCuxdCreateSystemVariableBool(t *testing.T) {
	t.Parallel()
	b := NewCuxdBackend(&fakeCaller{}, nil)
	_, err := b.CreateSystemVariableBool(context.Background(), "v", true)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCuxdCreateSystemVariableEnum(t *testing.T) {
	t.Parallel()
	b := NewCuxdBackend(&fakeCaller{}, nil)
	_, err := b.CreateSystemVariableEnum(context.Background(), "v", []string{"a"})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCuxdCreateSystemVariableFloat(t *testing.T) {
	t.Parallel()
	b := NewCuxdBackend(&fakeCaller{}, nil)
	_, err := b.CreateSystemVariableFloat(context.Background(), "v", 0, 100)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCuxdGetInstallMode(t *testing.T) {
	t.Parallel()
	b := NewCuxdBackend(&fakeCaller{}, nil)
	_, err := b.GetInstallMode(context.Background())
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCuxdSetInstallMode(t *testing.T) {
	t.Parallel()
	b := NewCuxdBackend(&fakeCaller{}, nil)
	err := b.SetInstallMode(context.Background(), true, 60, 1, "")
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCuxdGetServiceMessages(t *testing.T) {
	t.Parallel()
	b := NewCuxdBackend(&fakeCaller{}, nil)
	_, err := b.GetServiceMessages(context.Background(), "")
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCuxdSuppressServiceMessage(t *testing.T) {
	t.Parallel()
	b := NewCuxdBackend(&fakeCaller{}, nil)
	err := b.SuppressServiceMessage(context.Background(), "ADDR:1", "LOWBAT", true)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCuxdGetAlarmMessages(t *testing.T) {
	t.Parallel()
	b := NewCuxdBackend(&fakeCaller{}, nil)
	_, err := b.GetAlarmMessages(context.Background())
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCuxdGetAllRooms(t *testing.T) {
	t.Parallel()
	b := NewCuxdBackend(&fakeCaller{}, nil)
	_, err := b.GetAllRooms(context.Background())
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCuxdGetAllFunctions(t *testing.T) {
	t.Parallel()
	b := NewCuxdBackend(&fakeCaller{}, nil)
	_, err := b.GetAllFunctions(context.Background())
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCuxdRenameDevice(t *testing.T) {
	t.Parallel()
	b := NewCuxdBackend(&fakeCaller{}, nil)
	_, err := b.RenameDevice(context.Background(), 1, "x")
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCuxdRenameChannel(t *testing.T) {
	t.Parallel()
	b := NewCuxdBackend(&fakeCaller{}, nil)
	_, err := b.RenameChannel(context.Background(), 1, "x")
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCuxdAcceptDeviceInInbox(t *testing.T) {
	t.Parallel()
	b := NewCuxdBackend(&fakeCaller{}, nil)
	_, err := b.AcceptDeviceInInbox(context.Background(), "ADDR")
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCuxdExecuteProgram(t *testing.T) {
	t.Parallel()
	b := NewCuxdBackend(&fakeCaller{}, nil)
	_, err := b.ExecuteProgram(context.Background(), "42")
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCuxdGetSystemVariable(t *testing.T) {
	t.Parallel()
	b := NewCuxdBackend(&fakeCaller{}, nil)
	_, err := b.GetSystemVariable(context.Background(), "v")
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCuxdGetAllSystemVariables(t *testing.T) {
	t.Parallel()
	b := NewCuxdBackend(&fakeCaller{}, nil)
	_, err := b.GetAllSystemVariables(context.Background())
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCuxdGetAllDeviceData(t *testing.T) {
	t.Parallel()
	b := NewCuxdBackend(&fakeCaller{}, nil)
	_, err := b.GetAllDeviceData(context.Background())
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCuxdGetDeviceDetails(t *testing.T) {
	t.Parallel()
	b := NewCuxdBackend(&fakeCaller{}, nil)
	_, err := b.GetDeviceDetails(context.Background(), nil)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCuxdGetDeviceDescription(t *testing.T) {
	t.Parallel()
	b := NewCuxdBackend(&fakeCaller{}, nil)
	_, err := b.GetDeviceDescription(context.Background(), "ADDR")
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCuxdCreateBackupAndDownload(t *testing.T) {
	t.Parallel()
	b := NewCuxdBackend(&fakeCaller{}, nil)
	_, err := b.CreateBackupAndDownload(context.Background(), 30, 5)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCuxdTriggerFirmwareUpdate(t *testing.T) {
	t.Parallel()
	b := NewCuxdBackend(&fakeCaller{}, nil)
	_, err := b.TriggerFirmwareUpdate(context.Background())
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCuxdDeleteSystemVariable(t *testing.T) {
	t.Parallel()
	b := NewCuxdBackend(&fakeCaller{}, nil)
	_, err := b.DeleteSystemVariable(context.Background(), "v")
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCuxdGetIseIDByAddress(t *testing.T) {
	t.Parallel()
	b := NewCuxdBackend(&fakeCaller{}, nil)
	_, err := b.GetIseIDByAddress(context.Background(), "ADDR")
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCuxdGetLinkInfo(t *testing.T) {
	t.Parallel()
	b := NewCuxdBackend(&fakeCaller{}, nil)
	_, err := b.GetLinkInfo(context.Background(), "iface", "S:1", "R:1")
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCuxdSetLinkInfo(t *testing.T) {
	t.Parallel()
	b := NewCuxdBackend(&fakeCaller{}, nil)
	_, err := b.SetLinkInfo(context.Background(), "iface", "S:1", "R:1", "n", "d")
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCuxdGetSuppressedServiceMessages(t *testing.T) {
	t.Parallel()
	b := NewCuxdBackend(&fakeCaller{}, nil)
	_, err := b.GetSuppressedServiceMessages(context.Background(), "iface", "ADDR:1")
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCuxdHasProgramIDs(t *testing.T) {
	t.Parallel()
	b := NewCuxdBackend(&fakeCaller{}, nil)
	_, err := b.HasProgramIDs(context.Background(), "42")
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCuxdDownloadFirmware(t *testing.T) {
	t.Parallel()
	b := NewCuxdBackend(&fakeCaller{}, nil)
	err := b.DownloadFirmware(context.Background())
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// HomegearBackend — ErrUnsupported stubs for the extended interface
// ---------------------------------------------------------------------------

func TestHomegearCreateSystemVariableBool(t *testing.T) {
	t.Parallel()
	b := NewHomegearBackend(&fakeCaller{}, nil)
	_, err := b.CreateSystemVariableBool(context.Background(), "v", true)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestHomegearCreateSystemVariableEnum(t *testing.T) {
	t.Parallel()
	b := NewHomegearBackend(&fakeCaller{}, nil)
	_, err := b.CreateSystemVariableEnum(context.Background(), "v", []string{"a"})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestHomegearCreateSystemVariableFloat(t *testing.T) {
	t.Parallel()
	b := NewHomegearBackend(&fakeCaller{}, nil)
	_, err := b.CreateSystemVariableFloat(context.Background(), "v", 0, 100)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestHomegearGetServiceMessages(t *testing.T) {
	t.Parallel()
	b := NewHomegearBackend(&fakeCaller{}, nil)
	_, err := b.GetServiceMessages(context.Background(), "")
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestHomegearSuppressServiceMessage(t *testing.T) {
	t.Parallel()
	b := NewHomegearBackend(&fakeCaller{}, nil)
	err := b.SuppressServiceMessage(context.Background(), "ADDR:1", "LOWBAT", true)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestHomegearGetAlarmMessages(t *testing.T) {
	t.Parallel()
	b := NewHomegearBackend(&fakeCaller{}, nil)
	_, err := b.GetAlarmMessages(context.Background())
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestHomegearGetAllRooms(t *testing.T) {
	t.Parallel()
	b := NewHomegearBackend(&fakeCaller{}, nil)
	_, err := b.GetAllRooms(context.Background())
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestHomegearGetAllFunctions(t *testing.T) {
	t.Parallel()
	b := NewHomegearBackend(&fakeCaller{}, nil)
	_, err := b.GetAllFunctions(context.Background())
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestHomegearRenameDevice(t *testing.T) {
	t.Parallel()
	b := NewHomegearBackend(&fakeCaller{}, nil)
	_, err := b.RenameDevice(context.Background(), 1, "x")
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestHomegearRenameChannel(t *testing.T) {
	t.Parallel()
	b := NewHomegearBackend(&fakeCaller{}, nil)
	_, err := b.RenameChannel(context.Background(), 1, "x")
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestHomegearAcceptDeviceInInbox(t *testing.T) {
	t.Parallel()
	b := NewHomegearBackend(&fakeCaller{}, nil)
	_, err := b.AcceptDeviceInInbox(context.Background(), "ADDR")
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestHomegearExecuteProgram(t *testing.T) {
	t.Parallel()
	b := NewHomegearBackend(&fakeCaller{}, nil)
	_, err := b.ExecuteProgram(context.Background(), "42")
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestHomegearGetAllDeviceData(t *testing.T) {
	t.Parallel()
	b := NewHomegearBackend(&fakeCaller{}, nil)
	_, err := b.GetAllDeviceData(context.Background())
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestHomegearGetDeviceDescriptionNoXML(t *testing.T) {
	t.Parallel()
	// HomegearBackend.GetDeviceDescription uses XML-RPC; returns ErrNotWired
	// (not ErrUnsupported) when the XML caller is nil.
	b := NewHomegearBackend(nil, nil)
	_, err := b.GetDeviceDescription(context.Background(), "ADDR")
	if !errors.Is(err, ErrNotWired) {
		t.Fatalf("want ErrNotWired, got %v", err)
	}
}

func TestHomegearCreateBackupAndDownload(t *testing.T) {
	t.Parallel()
	b := NewHomegearBackend(&fakeCaller{}, nil)
	_, err := b.CreateBackupAndDownload(context.Background(), 30, 5)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestHomegearGetIseIDByAddress(t *testing.T) {
	t.Parallel()
	b := NewHomegearBackend(&fakeCaller{}, nil)
	_, err := b.GetIseIDByAddress(context.Background(), "ADDR")
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestHomegearGetLinkInfo(t *testing.T) {
	t.Parallel()
	b := NewHomegearBackend(&fakeCaller{}, nil)
	_, err := b.GetLinkInfo(context.Background(), "iface", "S:1", "R:1")
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestHomegearSetLinkInfo(t *testing.T) {
	t.Parallel()
	b := NewHomegearBackend(&fakeCaller{}, nil)
	_, err := b.SetLinkInfo(context.Background(), "iface", "S:1", "R:1", "n", "d")
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestHomegearGetSuppressedServiceMessages(t *testing.T) {
	t.Parallel()
	b := NewHomegearBackend(&fakeCaller{}, nil)
	_, err := b.GetSuppressedServiceMessages(context.Background(), "iface", "ADDR:1")
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestHomegearHasProgramIDs(t *testing.T) {
	t.Parallel()
	b := NewHomegearBackend(&fakeCaller{}, nil)
	_, err := b.HasProgramIDs(context.Background(), "42")
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}
