// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// scheduleFleetFixture is the channel layout of every device in the
// simulated 399-model fleet that carries a `*_WEEK_PROFILE` channel,
// extracted verbatim from the simulator's embedded device descriptions
// (godevccu internal/embed/data/device_descriptions). It is the
// ground-truth counterpart to the hand-built single-device fixtures in
// schedules_test.go: those prove a rule fires, this proves the rule set
// covers the devices that actually exist.
type scheduleFleetFixture struct {
	Devices []scheduleFleetDevice `json:"devices"`
}

type scheduleFleetDevice struct {
	Model               string                 `json:"model"`
	ScheduleChannel     int                    `json:"schedule_channel"`
	ScheduleChannelType string                 `json:"schedule_channel_type"`
	Channels            []scheduleFleetChannel `json:"channels"`
}

type scheduleFleetChannel struct {
	No   int    `json:"no"`
	Type string `json:"type"`
}

// loadScheduleChannelFleet decodes the fleet fixture.
func loadScheduleChannelFleet(t *testing.T) []scheduleFleetDevice {
	t.Helper()
	raw, err := os.ReadFile("testdata/schedule_channel_fleet.json")
	if err != nil {
		t.Fatalf("read fleet fixture: %v", err)
	}
	var fx scheduleFleetFixture
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatalf("decode fleet fixture: %v", err)
	}
	if len(fx.Devices) == 0 {
		t.Fatal("fleet fixture carries no devices — the walk would pass vacuously")
	}
	return fx.Devices
}

// buildFleetDevice materialises one fixture entry as a real
// [device.Device] with the channel numbers and types the CCU reports.
func buildFleetDevice(d scheduleFleetDevice) *device.Device {
	addr := "FLEET" + d.Model
	dev := device.New(device.Config{
		Address:     addr,
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Model:       d.Model,
	})
	for _, ch := range d.Channels {
		paramset := hmenum.ParamsetKeyValues
		if ch.No == d.ScheduleChannel {
			paramset = hmenum.ParamsetKeyMaster
		}
		dev.AddChannel(fmt.Sprintf("%s:%d", addr, ch.No), ch.No, ch.Type, paramset)
	}
	return dev
}

// scheduleDomainByChannelType is the resolution the fleet must produce,
// keyed by the channel type the device carries on its schedule channel.
// The seven entries are the complete set of `*_WEEK_PROFILE` types the
// CCU firmware defines and the simulated fleet exhibits — a type absent
// here has either been invented or newly appeared and needs a verdict.
var scheduleDomainByChannelType = map[string]string{
	"SWITCH_WEEK_PROFILE":                  "switch",
	"DIMMER_WEEK_PROFILE":                  "light",
	"DIMMER_OUTPUT_BEHAVIOUR_WEEK_PROFILE": "light",
	"UNIVERSAL_LIGHT_WEEK_PROFILE":         "light",
	"BLIND_WEEK_PROFILE":                   "cover",
	"SHADING_WEEK_PROFILE":                 "cover",
	"WATER_SWITCH_WEEK_PROFILE":            "valve",
}

// weekProfileTypesWithoutAFleetDevice registers a channel type
// [weekProfileDomains] classifies although the simulated fleet carries no
// device for it. An entry needs a ground-truth source outside the fleet;
// anything else in the production table has to be witnessed by a real
// device, or it is a name somebody invented.
var weekProfileTypesWithoutAFleetDevice = map[string]string{
	"SERVO_WEEK_PROFILE": "declared by the CCU WebUI channel-description table (chType_SERVO_WEEK_PROFILE); " +
		"the servo actor drives a percentage level (SERVO_TRANSMITTER.LEVEL plus RAMP_TIME)",
}

// scheduleDomainLockOverride names the devices whose lock actor channel
// deliberately outranks the schedule channel's own type: a door lock
// publishes its schedule on a generic SWITCH_WEEK_PROFILE channel and
// must still render the lock action picker, not an on/off switch.
var scheduleDomainLockOverride = map[string]string{
	"HmIP-DLD": "lock",
	"HmIP-DLP": "lock",
}

// TestEveryScheduleChannelInTheFleetResolvesToItsDomain walks every
// device in the fleet that owns a `*_WEEK_PROFILE` channel and asserts
// the resolved schedule domain matches the channel type.
//
// An unresolved domain is not cosmetic: the SPA editor gates the
// brightness slider, the ramp-time field and the slat (level_2) control
// on it, so a light schedule with an empty domain renders raw numbers
// and hides the fields the device supports, while the MQTT publisher
// substitutes the literal "switch" and reports the device to Home
// Assistant as something it is not.
func TestEveryScheduleChannelInTheFleetResolvesToItsDomain(t *testing.T) {
	t.Parallel()
	fleet := loadScheduleChannelFleet(t)
	seenTypes := map[string]struct{}{}
	for _, fd := range fleet {
		seenTypes[fd.ScheduleChannelType] = struct{}{}
		want, ok := scheduleDomainByChannelType[fd.ScheduleChannelType]
		if !ok {
			t.Errorf("%s: schedule channel type %q has no expected domain — a new week-profile "+
				"channel type appeared in the fleet and needs a verdict in "+
				"scheduleDomainByChannelType and in domainFromWeekProfileType",
				fd.Model, fd.ScheduleChannelType)
			continue
		}
		if override, isLock := scheduleDomainLockOverride[fd.Model]; isLock {
			want = override
		}
		got := resolveScheduleDomain(buildFleetDevice(fd), fd.ScheduleChannel)
		if got != want {
			t.Errorf("%s (%s on channel %d): schedule domain = %q, want %q",
				fd.Model, fd.ScheduleChannelType, fd.ScheduleChannel, got, want)
		}
	}
	for chType := range scheduleDomainByChannelType {
		if _, ok := seenTypes[chType]; !ok {
			t.Errorf("%s is listed in scheduleDomainByChannelType but no device in the fleet carries "+
				"it — drop the entry so the table keeps naming real channel types", chType)
		}
	}
	// Every production entry has to name a channel type that exists.
	// The prefix rule set this table replaced carried nine branches that
	// matched nothing on any device, so the code read as if it covered
	// locks, valves and heating schedules while every one of those
	// branches was unreachable.
	for chType := range weekProfileDomains {
		if _, inFleet := seenTypes[chType]; inFleet {
			continue
		}
		if _, declared := weekProfileTypesWithoutAFleetDevice[chType]; declared {
			continue
		}
		t.Errorf("weekProfileDomains classifies %q, which no device in the fleet carries — either it "+
			"is a name nothing declares, or it needs an entry in weekProfileTypesWithoutAFleetDevice "+
			"naming the source that does declare it", chType)
	}
	for chType, reason := range weekProfileTypesWithoutAFleetDevice {
		if _, ok := weekProfileDomains[chType]; !ok {
			t.Errorf("%s is declared fleet-less (%q) but weekProfileDomains does not classify it — "+
				"drop the entry", chType, reason)
		}
		if _, inFleet := seenTypes[chType]; inFleet {
			t.Errorf("%s is declared fleet-less (%q) but the fleet does carry it — drop the entry so "+
				"the list keeps meaning what it says", chType, reason)
		}
	}
}

// TestScheduleDomainIsDecidedByChannelTypeNotChannelNumber pins that two
// devices sharing a schedule channel type resolve to the same domain.
//
// The fallback that scans the device's other actor channels answers with
// whatever type sorts first, so a rule set that lets a real channel type
// fall through to it hands the same schedule two different domains
// depending on where the manufacturer numbered the actor channels.
func TestScheduleDomainIsDecidedByChannelTypeNotChannelNumber(t *testing.T) {
	t.Parallel()
	fleet := loadScheduleChannelFleet(t)
	byType := map[string]map[string][]string{}
	for _, fd := range fleet {
		if _, isLock := scheduleDomainLockOverride[fd.Model]; isLock {
			continue
		}
		got := resolveScheduleDomain(buildFleetDevice(fd), fd.ScheduleChannel)
		if byType[fd.ScheduleChannelType] == nil {
			byType[fd.ScheduleChannelType] = map[string][]string{}
		}
		byType[fd.ScheduleChannelType][got] = append(byType[fd.ScheduleChannelType][got], fd.Model)
	}
	types := make([]string, 0, len(byType))
	for chType := range byType {
		types = append(types, chType)
	}
	sort.Strings(types)
	for _, chType := range types {
		if len(byType[chType]) <= 1 {
			continue
		}
		t.Errorf("%s resolves to %d different domains across the fleet (%v) — the schedule domain is "+
			"being decided by the device's channel numbering instead of by the schedule channel's own type",
			chType, len(byType[chType]), byType[chType])
	}
}

// TestPublishScheduleEntityPayloadResolvesUniversalLightDomain drives the
// real MQTT publish path for an HmIP-RGBW and asserts the Zeitplan
// attributes carry the light domain.
//
// The publisher falls back to the literal "switch" whenever the
// resolution comes back empty, so an unclassified universal-light
// schedule reaches Home Assistant announcing itself as a switch.
func TestPublishScheduleEntityPayloadResolvesUniversalLightDomain(t *testing.T) {
	t.Parallel()
	reg, dev, schedCh := registerFleetDevice(t, "HmIP-RGBW")
	wp := weekprofile.NewProfileDataPoint(weekprofile.ProfileDataPointConfig{
		CentralName:    "ccu-01",
		ChannelAddress: schedCh.Address,
		ScheduleType:   weekprofile.ScheduleTypeDefault,
		ProfileCount:   1,
	})
	schedCh.AttachWeekProfile(wp)

	if got := scheduleDomainFromPublishedAttrs(t, reg, dev.Address, schedCh.Number, wp); got != "light" {
		t.Errorf("published schedule_domain = %q, want light", got)
	}
}

// TestPublishScheduleEntityPayloadResolvesShadingDomain is the cover-side
// counterpart: an HmIP-HDM shading actuator must publish "cover" so the
// editor offers the slat control the device carries.
func TestPublishScheduleEntityPayloadResolvesShadingDomain(t *testing.T) {
	t.Parallel()
	reg, dev, schedCh := registerFleetDevice(t, "HmIP-HDM1")
	wp := weekprofile.NewProfileDataPoint(weekprofile.ProfileDataPointConfig{
		CentralName:    "ccu-01",
		ChannelAddress: schedCh.Address,
		ScheduleType:   weekprofile.ScheduleTypeDefault,
		ProfileCount:   1,
	})
	schedCh.AttachWeekProfile(wp)

	if got := scheduleDomainFromPublishedAttrs(t, reg, dev.Address, schedCh.Number, wp); got != "cover" {
		t.Errorf("published schedule_domain = %q, want cover", got)
	}
}

// registerFleetDevice materialises the named fleet model on a fresh
// registry and returns it together with its schedule channel.
func registerFleetDevice(t *testing.T, model string) (*central.Registry, *device.Device, *device.Channel) {
	t.Helper()
	var fd *scheduleFleetDevice
	for _, cand := range loadScheduleChannelFleet(t) {
		if cand.Model == model {
			fd = &cand
			break
		}
	}
	if fd == nil {
		t.Fatalf("%s is not in the fleet fixture", model)
	}
	reg := central.NewRegistry()
	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	if err := reg.Register(c); err != nil {
		t.Fatalf("register: %v", err)
	}
	dev := buildFleetDevice(*fd)
	c.ModelRegistry.Put(dev)
	schedCh := dev.Channel(fmt.Sprintf("%s:%d", dev.Address, fd.ScheduleChannel))
	if schedCh == nil {
		t.Fatalf("%s: schedule channel %d missing", model, fd.ScheduleChannel)
	}
	return reg, dev, schedCh
}
