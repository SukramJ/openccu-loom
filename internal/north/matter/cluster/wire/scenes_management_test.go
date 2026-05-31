// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package wire_test

import (
	"context"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestScenesManagementInvokeContainsNoCommands verifies that MatterInvoke
// returns an error whose message contains "no commands" so the bridge
// dispatcher maps it to IM StatusCode UnsupportedCommand (0x81) rather
// than StatusFailure (0x01).
//
// The bridge dispatcher in internal/north/matter/endpoint/dispatcher.go:492
// maps errors via string-heuristic: containsAny(msg, "unknown command", "no commands")
// → UnsupportedCommand. A bare errScenesStub without "no commands" would fall
// through to StatusFailure.
//
// matter.js packages/node/src/behaviors/scenes-management/ + chip
// src/app/clusters/scenes-server/ScenesServer.cpp require proper status-code
// responses for unsupported commands.
func TestScenesManagementInvokeContainsNoCommands(t *testing.T) {
	t.Parallel()
	s := wire.ScenesManagement{}
	_, err := s.MatterInvoke(context.Background(), 0x00, nil, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("MatterInvoke returned nil error, want rejection")
	}
	if !strings.Contains(err.Error(), "no commands") {
		t.Errorf("MatterInvoke error = %q; want message containing 'no commands' so dispatcher encodes UnsupportedCommand (0x81)", err.Error())
	}
}

// TestScenesManagementInvokeRejectsAllCommandIDs verifies that every arbitrary
// cmdID returns a non-nil error.
func TestScenesManagementInvokeRejectsAllCommandIDs(t *testing.T) {
	t.Parallel()
	s := wire.ScenesManagement{}
	for _, cmdID := range []uint32{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x40, 0xFF} {
		_, err := s.MatterInvoke(context.Background(), cmdID, nil, hmenum.CommandPriorityHigh)
		if err == nil {
			t.Errorf("cmdID 0x%02X: MatterInvoke returned nil, want error", cmdID)
		}
	}
}

// TestScenesManagementReadSceneTableSizeIsZero verifies the stub advertises 0.
func TestScenesManagementReadSceneTableSizeIsZero(t *testing.T) {
	t.Parallel()
	s := wire.ScenesManagement{}
	v, ok := s.MatterRead(0x0001)
	if !ok {
		t.Fatal("MatterRead(SceneTableSize) ok = false")
	}
	if got := v.(uint16); got != 0 {
		t.Errorf("SceneTableSize = %d, want 0", got)
	}
}

// TestScenesManagementWriteReturnsError verifies that any write returns a non-nil error.
func TestScenesManagementWriteReturnsError(t *testing.T) {
	t.Parallel()
	s := wire.ScenesManagement{}
	err := s.MatterWrite(context.Background(), 0x0001, uint16(0), hmenum.CommandPriorityHigh)
	if err == nil {
		t.Error("MatterWrite returned nil, want error")
	}
}

func TestScenesManagement_MatterClusterID_NonZero(t *testing.T) {
	t.Parallel()
	s := wire.ScenesManagement{}
	if s.MatterClusterID() == 0 {
		t.Error("MatterClusterID = 0")
	}
}

func TestScenesManagement_MatterReportable_IsNil(t *testing.T) {
	t.Parallel()
	s := wire.ScenesManagement{}
	if r := s.MatterReportable(); r != nil {
		t.Errorf("MatterReportable = %v, want nil", r)
	}
}

func TestScenesManagement_MatterAttributes_NonEmpty(t *testing.T) {
	t.Parallel()
	s := wire.ScenesManagement{}
	if len(s.MatterAttributes()) == 0 {
		t.Error("MatterAttributes: want non-empty")
	}
}

func TestScenesManagement_MatterRead_FabricSceneInfo(t *testing.T) {
	t.Parallel()
	s := wire.ScenesManagement{}
	v, ok := s.MatterRead(0x0002) // FabricSceneInfo
	if !ok {
		t.Fatal("MatterRead(FabricSceneInfo): ok=false")
	}
	list, isList := v.([]any)
	if !isList || len(list) != 0 {
		t.Errorf("FabricSceneInfo = %v, want empty list", v)
	}
}

func TestScenesManagement_MatterRead_UnknownAttr(t *testing.T) {
	t.Parallel()
	s := wire.ScenesManagement{}
	_, ok := s.MatterRead(0x9999)
	if ok {
		t.Error("unknown attr: want ok=false")
	}
}
