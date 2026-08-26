// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// scheduleDomainFromPublishedAttrs drives the real publish path and returns the
// schedule_domain the MQTT Zeitplan attributes carry for the given schedule
// channel of the device already registered on reg.
func scheduleDomainFromPublishedAttrs(
	t *testing.T, reg *central.Registry, address string, channelNo int, wp *weekprofile.ProfileDataPoint,
) string {
	t.Helper()
	pub := mqtt.NewNoopClient()
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{
		Base:        "openccu-loom",
		CentralName: "ccu-01",
		RawEnabled:  true,
	}, pub)
	eb := NewEventBridge(reg, nil, mqtt.NewWiring(bridge, nil))

	eb.publishScheduleEntityPayload(context.Background(), "ccu-01", "HmIP-RF", address, channelNo, wp)

	for _, p := range pub.Published() {
		var attrs map[string]any
		if err := json.Unmarshal(p.Payload, &attrs); err != nil {
			continue
		}
		if d, ok := attrs["schedule_domain"].(string); ok {
			return d
		}
	}
	return ""
}

// lockScheduleDevice registers a door-lock device shaped like HmIP-DLD: a
// DOOR_LOCK actor channel plus the generic SWITCH_WEEK_PROFILE schedule
// channel, with a non-climate week profile attached to the schedule channel.
//
//nolint:gocritic // multi-value test helper; positional results read fine at the two call sites
func lockScheduleDevice(t *testing.T, model, lockChannelType string) (*central.Registry, string, int, *weekprofile.ProfileDataPoint) {
	t.Helper()
	reg := central.NewRegistry()
	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	if err := reg.Register(c); err != nil {
		t.Fatalf("register: %v", err)
	}
	dev := device.New(device.Config{
		Address: "000LOCK", InterfaceID: "HmIP-RF", Interface: hmenum.InterfaceHmIPRF, Model: model,
	})
	dev.AddChannel("000LOCK:1", 1, lockChannelType, hmenum.ParamsetKeyValues)
	schedCh := dev.AddChannel("000LOCK:10", 10, "SWITCH_WEEK_PROFILE", hmenum.ParamsetKeyMaster)
	wp := weekprofile.NewProfileDataPoint(weekprofile.ProfileDataPointConfig{
		CentralName:    "ccu-01",
		ChannelAddress: schedCh.Address,
		ScheduleType:   weekprofile.ScheduleTypeDefault,
		ProfileCount:   1,
	})
	schedCh.AttachWeekProfile(wp)
	c.ModelRegistry.Put(dev)
	return reg, dev.Address, 10, wp
}

// TestPublishScheduleEntityPayloadResolvesLockDomain pins that the MQTT
// schedule_domain attribute reports the device's real domain instead of the
// hard-coded "switch". A door lock (HmIP-DLD/DLP) publishes its schedule on a
// generic SWITCH_WEEK_PROFILE channel; the literal made it masquerade as a
// switch on MQTT while REST reported "lock".
func TestPublishScheduleEntityPayloadResolvesLockDomain(t *testing.T) {
	t.Parallel()
	reg, addr, chNo, wp := lockScheduleDevice(t, "HmIP-DLD", "DOOR_LOCK_STATE_TRANSMITTER")
	if got := scheduleDomainFromPublishedAttrs(t, reg, addr, chNo, wp); got != "lock" {
		t.Errorf("published schedule_domain = %q, want lock", got)
	}
}

// TestPublishScheduleEntityPayloadResolvesCoverDomain pins that a genuine cover
// still publishes "cover", proving the resolution is real and not lock-only.
func TestPublishScheduleEntityPayloadResolvesCoverDomain(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	if err := reg.Register(c); err != nil {
		t.Fatalf("register: %v", err)
	}
	dev := device.New(device.Config{
		Address: "000ROLL", InterfaceID: "HmIP-RF", Interface: hmenum.InterfaceHmIPRF, Model: "HmIP-BROLL",
	})
	dev.AddChannel("000ROLL:3", 3, "SHUTTER_TRANSMITTER", hmenum.ParamsetKeyValues)
	schedCh := dev.AddChannel("000ROLL:7", 7, "BLIND_WEEK_PROFILE", hmenum.ParamsetKeyMaster)
	wp := weekprofile.NewProfileDataPoint(weekprofile.ProfileDataPointConfig{
		CentralName:    "ccu-01",
		ChannelAddress: schedCh.Address,
		ScheduleType:   weekprofile.ScheduleTypeDefault,
		ProfileCount:   1,
	})
	schedCh.AttachWeekProfile(wp)
	c.ModelRegistry.Put(dev)

	if got := scheduleDomainFromPublishedAttrs(t, reg, dev.Address, 7, wp); got != "cover" {
		t.Errorf("published schedule_domain = %q, want cover", got)
	}
}
