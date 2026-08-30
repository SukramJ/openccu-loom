// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package devicedetails

import (
	"reflect"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func TestNewCacheIsEmpty(t *testing.T) {
	t.Parallel()
	c := New()
	if !c.IsEmpty() {
		t.Fatal("New() must return an empty cache")
	}
	if c.GetName("VCU1") != "" {
		t.Errorf("zero-state GetName must return \"\"")
	}
	if c.GetAddressID("VCU1") != 0 {
		t.Errorf("zero-state GetAddressID must return 0")
	}
	if c.GetInterface("VCU1") != hmenum.InterfaceBidCosRF {
		t.Errorf("zero-state GetInterface must default to BidCos-RF")
	}
}

func TestAddNameAndGetName(t *testing.T) {
	t.Parallel()
	c := New()
	c.AddName("VCU1234567", "Wohnzimmer Heizung")
	if got := c.GetName("VCU1234567"); got != "Wohnzimmer Heizung" {
		t.Errorf("GetName = %q, want Wohnzimmer Heizung", got)
	}
	if !c.HasName("VCU1234567") {
		t.Error("HasName must return true after AddName")
	}
}

func TestAddInterface(t *testing.T) {
	t.Parallel()
	c := New()
	c.AddInterface("VCU1234567:1", hmenum.InterfaceHmIPRF)
	if got := c.GetInterface("VCU1234567:1"); got != hmenum.InterfaceHmIPRF {
		t.Errorf("GetInterface = %v, want HmIP-RF", got)
	}
}

func TestAddAddressISEID(t *testing.T) {
	t.Parallel()
	c := New()
	c.AddAddressISEID("VCU1234567:1", 7654)
	if got := c.GetAddressID("VCU1234567:1"); got != 7654 {
		t.Errorf("GetAddressID = %d, want 7654", got)
	}
	ids := c.DeviceChannelISEIDs()
	if ids["VCU1234567:1"] != 7654 {
		t.Errorf("DeviceChannelISEIDs[VCU…:1] = %d, want 7654", ids["VCU1234567:1"])
	}
	// Returned map must be a copy.
	ids["VCU1234567:1"] = 99
	if c.GetAddressID("VCU1234567:1") != 7654 {
		t.Error("DeviceChannelISEIDs() must return a copy, not a live reference")
	}
}

func TestAddChannelRoomAggregatesDeviceRooms(t *testing.T) {
	t.Parallel()
	c := New()
	c.AddChannelRoom("VCU1234567:1", "Wohnzimmer")
	c.AddChannelRoom("VCU1234567:2", "Wohnzimmer")
	c.AddChannelRoom("VCU1234567:3", "Küche")

	if got := c.GetChannelRooms("VCU1234567:1"); !reflect.DeepEqual(got, []string{"Wohnzimmer"}) {
		t.Errorf("ch1 rooms = %v, want [Wohnzimmer]", got)
	}
	deviceRooms := c.GetDeviceRooms("VCU1234567")
	want := []string{"Küche", "Wohnzimmer"} // sorted
	if !reflect.DeepEqual(deviceRooms, want) {
		t.Errorf("device rooms = %v, want %v", deviceRooms, want)
	}
}

func TestAddFunctionAggregates(t *testing.T) {
	t.Parallel()
	c := New()
	c.AddFunction("VCU1234567:1", "Heizung")
	c.AddFunction("VCU1234567:1", "Klima")
	if got := c.GetFunctionText("VCU1234567:1"); got != "Heizung,Klima" {
		t.Errorf("GetFunctionText = %q, want \"Heizung,Klima\"", got)
	}
}

func TestRemoveDeviceClearsAllRows(t *testing.T) {
	t.Parallel()
	c := New()
	c.AddName("VCU1234567", "Heizung")
	c.AddName("VCU1234567:1", "Heizung Wohnzimmer")
	c.AddAddressISEID("VCU1234567", 100)
	c.AddAddressISEID("VCU1234567:1", 101)
	c.AddChannelRoom("VCU1234567:1", "Wohnzimmer")
	c.AddFunction("VCU1234567", "Heizung")

	c.RemoveDevice("VCU1234567", []string{"VCU1234567:1"})

	if c.GetName("VCU1234567") != "" {
		t.Error("device-level name must be cleared")
	}
	if c.GetName("VCU1234567:1") != "" {
		t.Error("channel-level name must be cleared")
	}
	if c.GetAddressID("VCU1234567:1") != 0 {
		t.Error("channel ISE-ID must be cleared")
	}
	if len(c.GetChannelRooms("VCU1234567:1")) != 0 {
		t.Error("channel rooms must be cleared")
	}
	if c.GetFunctionText("VCU1234567") != "" {
		t.Error("functions must be cleared")
	}
}

func TestClearWipes(t *testing.T) {
	t.Parallel()
	c := New()
	c.AddName("VCU1234567", "X")
	c.MarkRefreshed(time.Now())
	c.Clear()
	if !c.IsEmpty() {
		t.Fatal("Clear() must empty the cache")
	}
	if !c.RefreshedAt().IsZero() {
		t.Fatal("Clear() must reset RefreshedAt")
	}
}

func TestMarkRefreshed(t *testing.T) {
	t.Parallel()
	c := New()
	now := time.Now().UTC().Truncate(time.Millisecond)
	c.MarkRefreshed(now)
	if !c.RefreshedAt().Equal(now) {
		t.Errorf("RefreshedAt = %v, want %v", c.RefreshedAt(), now)
	}
}

func TestAddFunctionEmptyIsNoop(t *testing.T) {
	t.Parallel()
	c := New()
	// AddFunction with empty string must be silently dropped.
	c.AddFunction("VCU001:1", "")
	if fns := c.GetFunctions("VCU001:1"); len(fns) != 0 {
		t.Errorf("expected 0 functions after empty add, got %v", fns)
	}
}

func TestAddChannelRoomEmptyIsNoop(t *testing.T) {
	t.Parallel()
	c := New()
	c.AddChannelRoom("VCU001:1", "")
	if rooms := c.GetChannelRooms("VCU001:1"); len(rooms) != 0 {
		t.Errorf("expected 0 rooms after empty add, got %v", rooms)
	}
}
