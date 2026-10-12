// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build chiptool

package chiptool

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/tests/chiptool/harness"
)

// TestSendReceive_GroupsScenes drives the stack-owned Groups and
// ScenesManagement clusters of an OnOff endpoint the way a commissioner
// does when it groups and scenes a bridged light (ADR 0084 phase 1 and 2):
// the fabric's group key map and key set, AddGroup / ViewGroup, a scene
// stored from the device's live state and recalled through the OnOff
// server's Sink — so the recall is a CCU write, measured on the
// simulator's STATE.
//
// The group key map is written BEFORE its key set on purpose: matter.js
// accepts that order (GroupKeyManagementServer.ts #validateGroupKeyMap
// leaves the key-set check out because certification tests write the map
// first), and the hand-carried schema used to refuse it with a foreign
// key the store no longer has — this is the hermetic pin for that fix.
func TestSendReceive_GroupsScenes(t *testing.T) {
	b := requireBridge(t)
	onOffEPs := discoverEndpointsWith(t, b, 0x0006, 1)
	if len(onOffEPs) == 0 {
		t.Skip("no OnOff endpoint — godevccu fleet lacks a Switch/PSM/BSM/BDT device")
	}
	ep := onOffEPs[0]
	groupsEPs := discoverEndpointsWith(t, b, 0x0004, 1)
	scenesEPs := discoverEndpointsWith(t, b, 0x0062, 1)
	if !containsEndpoint(groupsEPs, ep) || !containsEndpoint(scenesEPs, ep) {
		t.Fatalf("OnOff endpoint %d does not serve Groups (0x0004) and ScenesManagement (0x0062); groups=%v scenes=%v", ep, groupsEPs, scenesEPs)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	address, _, ok := b.ResolveCCUAddress(ctx, t, ep, 0x0006)
	if !ok {
		t.Fatalf("could not resolve CCU address for OnOff endpoint %d", ep)
	}

	const (
		groupID  = 257
		keySetID = 42
		sceneID  = 1
	)
	// The shared bridge is one fabric; the key map names its index as
	// chip-tool's guide does, and the server overwrites it with the
	// accessing fabric anyway.
	groupKeyMap := fmt.Sprintf(`[{"groupId": %d, "groupKeySetID": %d, "fabricIndex": 1}]`, groupID, keySetID)
	keySet := fmt.Sprintf(`{"groupKeySetID": %d, "groupKeySecurityPolicy": 0, `+
		`"epochKey0": "hex:d0d1d2d3d4d5d6d7d8d9dadbdcdddedf", "epochStartTime0": 2220000, `+
		`"epochKey1": "hex:d1d1d2d3d4d5d6d7d8d9dadbdcdddedf", "epochStartTime1": 2220001, `+
		`"epochKey2": "hex:d2d1d2d3d4d5d6d7d8d9dadbdcdddedf", "epochStartTime2": 2220002}`, keySetID)

	// Housekeeping first, so a half-run leaves neither the group nor the
	// lamp behind: the group is removed and the switch is OFF.
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer ccancel()
		_, _ = b.SharedCtl.Invoke(cctx, t, "groups", "remove-group", ep, strconv.Itoa(groupID))
		_, _ = b.SharedCtl.Invoke(cctx, t, "onoff", "off", ep)
	})

	if _, err := b.SharedCtl.Invoke(ctx, t, "onoff", "off", ep); err != nil {
		t.Fatalf("baseline off: %v", err)
	}
	if got, ok := b.CCU.GetDPValue(address, "STATE"); !ok || !onoffIsFalse(got) {
		t.Fatalf("CCU STATE after baseline off = %v (ok=%v), want false", got, ok)
	}

	t.Run("send/group-key-map-before-key-set", func(t *testing.T) {
		out, _ := b.SharedCtl.WriteAttr(ctx, t, "groupkeymanagement", "group-key-map", groupKeyMap, 0)
		// A list write answers one AttributeStatusIB for the list and one
		// per element; every one must be SUCCESS.
		statuses := reIMWriteStatus.FindAllStringSubmatch(out, -1)
		if len(statuses) == 0 {
			t.Fatalf("GroupKeyMap write before its key set: no WriteResponse status\n%s", out)
		}
		for _, m := range statuses {
			if m[1] != "0x00" {
				t.Fatalf("GroupKeyMap write before its key set: status %s, want SUCCESS (matter.js accepts the map first; the store must not demand the key set)\n%s", m[1], out)
			}
		}
	})
	t.Run("send/key-set-write", func(t *testing.T) {
		out, err := b.SharedCtl.Invoke(ctx, t, "groupkeymanagement", "key-set-write", 0, keySet)
		if err != nil || !harness.CommandSuccess(out) {
			t.Fatalf("KeySetWrite: err=%v\n%s", err, out)
		}
	})
	t.Run("send/add-group", func(t *testing.T) {
		out, err := b.SharedCtl.Invoke(ctx, t, "groups", "add-group", ep, strconv.Itoa(groupID), "loom-chiptool")
		if err != nil {
			t.Fatalf("AddGroup: %v\n%s", err, out)
		}
		if status, ok := harness.FindAttrUint(out, "status"); !ok || status != 0 {
			t.Fatalf("AddGroupResponse status=%d ok=%v, want 0 (the group's key map entry exists)\n%s", status, ok, out)
		}
		if id, ok := harness.FindAttrUint(out, "groupID"); !ok || id != groupID {
			t.Fatalf("AddGroupResponse groupID=%d ok=%v, want %d\n%s", id, ok, groupID, out)
		}
	})
	t.Run("send/view-group", func(t *testing.T) {
		out, err := b.SharedCtl.Invoke(ctx, t, "groups", "view-group", ep, strconv.Itoa(groupID))
		if err != nil {
			t.Fatalf("ViewGroup: %v\n%s", err, out)
		}
		if status, ok := harness.FindAttrUint(out, "status"); !ok || status != 0 {
			t.Fatalf("ViewGroupResponse status=%d ok=%v, want 0\n%s", status, ok, out)
		}
		if name, ok := harness.FindAttrString(out, "groupName"); !ok || name != "loom-chiptool" {
			t.Fatalf("ViewGroupResponse groupName=%q ok=%v, want the name AddGroup stored (NameSupport)\n%s", name, ok, out)
		}
	})
	t.Run("send/store-scene-captures-on", func(t *testing.T) {
		if _, err := b.SharedCtl.Invoke(ctx, t, "onoff", "on", ep); err != nil {
			t.Fatalf("invoke on: %v", err)
		}
		if got, ok := b.CCU.GetDPValue(address, "STATE"); !ok || !onoffIsTrue(got) {
			t.Fatalf("CCU STATE after on = %v (ok=%v), want true", got, ok)
		}
		out, err := b.SharedCtl.Invoke(ctx, t, "scenesmanagement", "store-scene", ep, strconv.Itoa(groupID), strconv.Itoa(sceneID))
		if err != nil {
			t.Fatalf("StoreScene: %v\n%s", err, out)
		}
		if status, ok := harness.FindAttrUint(out, "status"); !ok || status != 0 {
			t.Fatalf("StoreScene status=%d ok=%v, want 0\n%s", status, ok, out)
		}
	})
	t.Run("send/recall-scene-writes-the-ccu", func(t *testing.T) {
		if _, err := b.SharedCtl.Invoke(ctx, t, "onoff", "off", ep); err != nil {
			t.Fatalf("invoke off: %v", err)
		}
		if got, ok := b.CCU.GetDPValue(address, "STATE"); !ok || !onoffIsFalse(got) {
			t.Fatalf("CCU STATE after off = %v (ok=%v), want false", got, ok)
		}
		out, err := b.SharedCtl.Invoke(ctx, t, "scenesmanagement", "recall-scene", ep, strconv.Itoa(groupID), strconv.Itoa(sceneID))
		if err != nil || !harness.CommandSuccess(out) {
			t.Fatalf("RecallScene: err=%v\n%s", err, out)
		}
		// The recalled OnOff value reaches the CCU through the OnOff
		// server's Sink — a scene is a device command, not a cached
		// attribute.
		deadline := time.Now().Add(10 * time.Second)
		for {
			if got, ok := b.CCU.GetDPValue(address, "STATE"); ok && onoffIsTrue(got) {
				break
			}
			if time.Now().After(deadline) {
				got, ok := b.CCU.GetDPValue(address, "STATE")
				t.Fatalf("CCU STATE after RecallScene = %v (ok=%v), want true: the recalled OnOff did not reach the CCU", got, ok)
			}
			time.Sleep(100 * time.Millisecond)
		}
	})
	t.Run("send/get-scene-membership", func(t *testing.T) {
		out, err := b.SharedCtl.Invoke(ctx, t, "scenesmanagement", "get-scene-membership", ep, strconv.Itoa(groupID))
		if err != nil {
			t.Fatalf("GetSceneMembership: %v\n%s", err, out)
		}
		if status, ok := harness.FindAttrUint(out, "status"); !ok || status != 0 {
			t.Fatalf("GetSceneMembershipResponse status=%d ok=%v, want 0\n%s", status, ok, out)
		}
		if !sceneListContains(out, sceneID) {
			t.Fatalf("GetSceneMembershipResponse does not list scene %d\n%s", sceneID, out)
		}
	})
}

// reIMWriteStatus matches the StatusIB lines of chip-tool's WriteResponse
// dump (`status = 0x00 (SUCCESS)`), which a list write answers once per
// element — the single `Status:` line [harness.WriteStatus] reads is not
// printed for those.
var reIMWriteStatus = regexp.MustCompile(`status = (0x[0-9A-Fa-f]{2}) \(`)

func containsEndpoint(eps []uint16, ep uint16) bool {
	for _, e := range eps {
		if e == ep {
			return true
		}
	}
	return false
}

// sceneListContains reports whether chip-tool's dump of a
// GetSceneMembershipResponse lists the scene id: the sceneList prints one
// `[n]: <id>` line per entry.
func sceneListContains(out string, sceneID int64) bool {
	return regexp.MustCompile(fmt.Sprintf(`(?m)\[TOO\]\s+\[\d+\]:\s+%d\s*$`, sceneID)).MatchString(out)
}
