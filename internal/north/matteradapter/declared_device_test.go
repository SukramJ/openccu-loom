// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package matteradapter_test

import (
	"testing"

	"github.com/SukramJ/go-fabric/cluster/spec"
	msdef "github.com/SukramJ/go-fabric/cluster/spec/modeselect"
	"github.com/SukramJ/go-fabric/endpoint"

	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestAWritableEnumProjectsAModeSelectDevice pins that a generic Select —
// a source that builds its device from the ModeSelect declaration — is
// assembled into a ModeSelect (0x0027) endpoint serving ModeSelect
// (0x0050), with the modes of its VALUE_LIST.
func TestAWritableEnumProjectsAModeSelectDevice(t *testing.T) {
	const addr = "VCU0000101"
	dev := newDevice(addr, "Siren mode")
	chAddr := addr + ":1"
	ch := dev.AddChannel(chAddr, 1, "ALARM_SWITCH_VIRTUAL_RECEIVER", hmenum.ParamsetKeyValues)
	ch.Put(generic.NewSelect(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: chAddr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterOpticalAlarmSelection),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeEnum,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
			ValueList:  []string{"DISABLE_OPTICAL_SIGNAL", "BLINKING_ALTERNATELY_REPEATING"},
		},
	}))

	top := assembleTopology(t, dev)
	ep := findBridgedByAddress(top, addr)
	if ep == nil {
		t.Fatalf("the select produced no bridged endpoint (%d bridged)", len(top.Bridged()))
	}
	if ep.DeviceType != 0x0027 {
		t.Fatalf("device type = 0x%04X, want ModeSelect 0x0027", ep.DeviceType)
	}
	if !clusterIDSet(ep)[msdef.ClusterID] {
		t.Fatalf("ModeSelect (0x%04X) is not served on the endpoint", msdef.ClusterID)
	}
	var modes int
	for _, srv := range endpoint.ClusterServers(ep) {
		if srv.MatterClusterID() != msdef.ClusterID {
			continue
		}
		v, ok := srv.MatterRead(msdef.AttrSupportedModes)
		if !ok {
			t.Fatal("SupportedModes does not answer")
		}
		list, isList := v.(spec.List[msdef.ModeOptionStruct])
		if !isList {
			t.Fatalf("SupportedModes read as %T", v)
		}
		modes = len(list)
	}
	if modes != 2 {
		t.Errorf("SupportedModes carries %d modes, want the 2 of the VALUE_LIST", modes)
	}
}
