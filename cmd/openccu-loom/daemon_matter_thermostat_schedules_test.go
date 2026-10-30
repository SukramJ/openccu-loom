// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"testing"

	"github.com/SukramJ/go-fabric/bridge/bridgetest"
	"github.com/SukramJ/go-fabric/cluster/spec"
	thermostatspec "github.com/SukramJ/go-fabric/cluster/spec/thermostat"
	"github.com/SukramJ/go-fabric/im"
	"github.com/SukramJ/go-fabric/tlv"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/custom/climate"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/model/schedule"
	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

const (
	thermoSchedPasscode   uint32 = 20202021
	thermoSchedDiscrim    uint16 = 3840
	thermoSchedFabricID   uint64 = 0x0FAC
	thermoSchedDeviceNode uint64 = 0x0D0E
	// atomicBeginWrite / atomicCommitWrite are the AtomicRequest types
	// go-fabric's Thermostat server takes (cluster/thermo/atomic.go:29-30).
	atomicBeginWrite  uint8 = 0
	atomicCommitWrite uint8 = 1
)

// fixedClimateLoader answers a week-profile read with one schedule.
type fixedClimateLoader struct{ sched *schedule.Climate }

func (l fixedClimateLoader) Load(context.Context) (*schedule.Climate, error) { return l.sched, nil }

// recordingScheduleWriter records the week programs the bridge writes.
type recordingScheduleWriter struct {
	mu     sync.Mutex
	writes map[string]hmapi.ClimateProfile
}

func (w *recordingScheduleWriter) PutClimateProfile(_ context.Context, _, profile string, p hmapi.ClimateProfile) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.writes == nil {
		w.writes = map[string]hmapi.ClimateProfile{}
	}
	w.writes[profile] = p
	return nil
}

// flatClimateSchedule is P1 at 20 °C and P2 at 18 °C all week.
func flatClimateSchedule() *schedule.Climate {
	out := &schedule.Climate{Profiles: map[string]*schedule.ClimateProfile{}}
	for key, temp := range map[string]float64{"P1": 20, "P2": 18} {
		p := &schedule.ClimateProfile{Days: map[schedule.Weekday]schedule.ClimateWeekday{}}
		for _, d := range []schedule.Weekday{
			schedule.WeekdayMonday, schedule.WeekdayTuesday, schedule.WeekdayWednesday, schedule.WeekdayThursday,
			schedule.WeekdayFriday, schedule.WeekdaySaturday, schedule.WeekdaySunday,
		} {
			p.Days[d] = schedule.ClimateWeekday{BaseTemperature: temp}
		}
		out.Profiles[key] = p
	}
	return out
}

// bootThermostatBridge starts the daemon's Matter bridge over one HmIP
// thermostat whose channel carries a loaded two-program week profile,
// with north.matter.thermostat_hold_and_running_state set to hold and
// north.matter.thermostat_schedules to schedules, and exposes it. It
// returns the bundle and the thermostat's endpoint.
func bootThermostatBridge(t *testing.T, hold, schedules bool, writer *recordingScheduleWriter) (context.Context, *matterBridgeBundle, uint16) {
	t.Helper()
	return bootThermostatBridgeWith(t, hold, schedules, writer, thermostatBridgeExtras{})
}

// thermostatBridgeExtras widens [bootThermostatBridgeWith]: configure
// edits the Matter config before validation, populate adds channels to
// the thermostat's device before the bridge starts.
type thermostatBridgeExtras struct {
	configure func(*config.NorthMatter)
	populate  func(*device.Device)
}

// bootThermostatBridgeWith is [bootThermostatBridge] with extras. It fails
// the test when the thermostat is not bridged.
func bootThermostatBridgeWith(t *testing.T, hold, schedules bool, writer *recordingScheduleWriter, extras thermostatBridgeExtras) (context.Context, *matterBridgeBundle, uint16) {
	t.Helper()
	cfg := config.Default()
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.MDNSAdvertise = "noop"
	cfg.North.Matter.Listen = "127.0.0.1:0"
	cfg.North.Matter.Commissioning.Passcode = thermoSchedPasscode
	cfg.North.Matter.Commissioning.Iterations = 1000
	cfg.North.Matter.CASE.NodeID = thermoSchedDeviceNode
	cfg.North.Matter.CASE.FabricID = thermoSchedFabricID
	cfg.North.Matter.Discriminator = thermoSchedDiscrim
	cfg.North.Matter.ThermostatHoldAndRunningState = hold
	cfg.North.Matter.ThermostatSchedules = schedules
	if extras.configure != nil {
		extras.configure(&cfg.North.Matter)
	}
	cfg.DataDir = t.TempDir()
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-01", Host: "127.0.0.1", Interfaces: []config.InterfaceSpec{{Name: "HmIP-RF"}}}}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	reg := buildTestRegistry(t, "ccu-01")
	unit, ok := reg.Get("ccu-01")
	if !ok {
		t.Fatal("central ccu-01 not registered")
	}
	dev := device.New(device.Config{Address: "BWTH0001", Name: "Wandthermostat"})
	ch := dev.AddChannel("BWTH0001:1", 1, "HEATING_CLIMATECONTROL_TRANSCEIVER", hmenum.ParamsetKeyValues)
	// LEVEL is the activity source a valve thermostat carries; without one
	// the climate serves no running state.
	ch.Put(generic.NewFloatSensor(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: "BWTH0001:1", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterLevel)},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead | hmenum.OperationsEvent},
	}))
	wp := weekprofile.NewProfileDataPoint(weekprofile.ProfileDataPointConfig{
		ChannelAddress: "BWTH0001:1", ScheduleType: weekprofile.ScheduleTypeClimate,
		MinTemp: 5, MaxTemp: 30, ProfileCount: 2,
	})
	wp.AttachClimateProfile(weekprofile.NewClimate(fixedClimateLoader{flatClimateSchedule()}, nil))
	if _, err := wp.Climate().Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	ch.AttachWeekProfile(wp)
	ch.SetCustomDataPoint(climate.New(climate.Config{
		Channel: ch, Kind: climate.KindIP,
		Capabilities: custom.ClimateCapabilities{SupportsAuto: true, SupportsHeat: true, SupportsProfile: true},
	}))
	if extras.populate != nil {
		extras.populate(dev)
	}
	unit.ModelRegistry.Put(dev)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	var w interfaces.ClimateScheduleWriter
	if writer != nil {
		w = writer
	}
	bundle := startMatterBridge(ctx, cfg, reg, openTestLoomDB(t), health.NewTracker(), nil, slog.New(slog.DiscardHandler), w)
	if bundle == nil {
		t.Fatal("startMatterBridge returned nil")
	}
	t.Cleanup(bundle.stop)
	exposeEveryCandidate(ctx, t, bundle, reg)

	for _, ep := range bundle.node.Bridge().Topology().Bridged() {
		if ep.DeviceType == 0x0301 {
			return ctx, bundle, ep.ID
		}
	}
	t.Fatal("no Thermostat endpoint bridged")
	return nil, nil, 0
}

// readThermostat reads one Thermostat attribute of ep through the running
// bridge: its status and value.
func readThermostat(ctx context.Context, bundle *matterBridgeBundle, ep uint16, attr uint32) (status im.StatusCode, value any) {
	res := bundle.node.Bridge().Dispatcher().Read(ctx, im.ConcreteAttributePath{
		Endpoint: ep, Cluster: thermostatspec.ClusterID, Attribute: attr,
		HasEndpoint: true, HasCluster: true, HasAttribute: true,
	})
	if len(res) != 1 {
		return im.StatusFailure, nil
	}
	return res[0].Status, res[0].Value.Value
}

// TestMatterThermostatSchedulesFollowTheConfigFlags pins
// north.matter.thermostat_hold_and_running_state and
// north.matter.thermostat_schedules through the daemon's Matter start path,
// one case per value of each: both off serve the thermostat as before; the
// hold flag alone serves the hold and the running state and no MSCH; the
// schedules flag alone serves MSCH and no hold. Each half is tried with
// Apple Home on its own, so neither may pull the other in.
func TestMatterThermostatSchedulesFollowTheConfigFlags(t *testing.T) {
	for _, tc := range []struct{ hold, schedules bool }{{false, false}, {true, false}, {false, true}} {
		t.Run(fmt.Sprintf("hold=%v,schedules=%v", tc.hold, tc.schedules), func(t *testing.T) {
			ctx, bundle, ep := bootThermostatBridge(t, tc.hold, tc.schedules, nil)
			st, fm := readThermostat(ctx, bundle, ep, 0xFFFC)
			if st != im.StatusSuccess {
				t.Fatalf("FeatureMap read = %v", st)
			}
			msch := toUint32(fm)&uint32(thermostatspec.FeatureMatterScheduleConfiguration) != 0
			if msch != tc.schedules {
				t.Errorf("FeatureMap 0x%X: MSCH = %v, want %v", toUint32(fm), msch, tc.schedules)
			}
			for _, a := range []struct {
				attr uint32
				want bool
			}{
				{thermostatspec.AttrTemperatureSetpointHold, tc.hold},
				{thermostatspec.AttrThermostatRunningState, tc.hold},
				{thermostatspec.AttrSchedules, tc.schedules},
			} {
				st, _ := readThermostat(ctx, bundle, ep, a.attr)
				if served := st == im.StatusSuccess; served != a.want {
					t.Errorf("attribute 0x%04X read %v, want served = %v", a.attr, st, a.want)
				}
			}
		})
	}
}

func toUint32(v any) uint32 {
	var n uint32
	_, _ = fmt.Sscan(fmt.Sprint(v), &n)
	return n
}

// TestMatterThermostatScheduleCommitReachesTheWriter pins the write path
// through the daemon's Matter start path: a commissioned controller's
// atomic write of Schedules reaches the week-program writer startMatterBridge
// was handed, with the changed program only.
func TestMatterThermostatScheduleCommitReachesTheWriter(t *testing.T) {
	writer := &recordingScheduleWriter{}
	ctx, bundle, ep := bootThermostatBridge(t, false, true, writer)

	cmr := bridgetest.Dial(t, bundle.node.Bridge().LocalAddr(), bridgetest.Options{
		Passcode: thermoSchedPasscode, Discriminator: thermoSchedDiscrim,
		FabricID: thermoSchedFabricID, NodeID: thermoSchedDeviceNode, Seed: 1,
	})
	sess, err := cmr.Commission(ctx)
	if err != nil {
		t.Fatalf("Commission: %v", err)
	}

	atomic := func(kind uint8) {
		t.Helper()
		timeout := uint16(5000)
		req := thermostatspec.AtomicRequestRequest{RequestType: kind, AttributeRequests: []uint32{thermostatspec.AttrSchedules}, Timeout: &timeout}
		res, err := sess.Invoke(ctx, im.ConcreteCommandPath{Endpoint: ep, Cluster: thermostatspec.ClusterID, Command: thermostatspec.CmdAtomicRequest},
			func(e *tlv.Encoder, tag tlv.Tag) { req.EncodeTLV(e, tag) })
		if err != nil || res.Status != im.StatusSuccess {
			t.Fatalf("AtomicRequest %d = %+v, %v", kind, res, err)
		}
	}
	flat := func(handle byte, sp int16, extra ...thermostatspec.ScheduleTransitionStruct) thermostatspec.ScheduleStruct {
		return thermostatspec.ScheduleStruct{
			ScheduleHandle: spec.ValueOf([]byte{handle}),
			SystemMode:     thermostatspec.SystemModeHeat,
			Transitions:    append([]thermostatspec.ScheduleTransitionStruct{{DayOfWeek: 0x7F, TransitionTime: 0, HeatingSetpoint: &sp}}, extra...),
			BuiltIn:        spec.ValueOf(true),
		}
	}
	hot := int16(2250)
	list := []thermostatspec.ScheduleStruct{
		flat(1, 2000),
		flat(2, 1800, thermostatspec.ScheduleTransitionStruct{DayOfWeek: thermostatspec.ScheduleDayOfWeekMonday, TransitionTime: 17 * 60, HeatingSetpoint: &hot}),
	}

	atomic(atomicBeginWrite)
	status, err := sess.Write(ctx, im.ConcreteAttributePath{
		Endpoint: ep, Cluster: thermostatspec.ClusterID, Attribute: thermostatspec.AttrSchedules,
		HasEndpoint: true, HasCluster: true, HasAttribute: true,
	}, func(e *tlv.Encoder, tag tlv.Tag) {
		e.StartArray(tag)
		for i := range list {
			list[i].EncodeTLV(e, tlv.AnonymousTag())
		}
		_ = e.EndContainer()
	})
	if err != nil || status != im.StatusSuccess {
		t.Fatalf("pending Schedules write = %v, %v", status, err)
	}
	atomic(atomicCommitWrite)

	writer.mu.Lock()
	defer writer.mu.Unlock()
	if _, p1 := writer.writes["P1"]; p1 || len(writer.writes) != 1 {
		t.Fatalf("writes = %v, want P2 alone", writer.writes)
	}
	mon := writer.writes["P2"].Weekdays["MONDAY"]
	if len(mon.Periods) != 2 || mon.Periods[1] != (hmapi.ClimatePeriod{StartTime: "17:00", EndTime: "24:00", Temperature: 22.5}) {
		t.Errorf("P2 Monday = %+v, want 18 °C until 17:00, then 22.5 °C", mon)
	}
}
