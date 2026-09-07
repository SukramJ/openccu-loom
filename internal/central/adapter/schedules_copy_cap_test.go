// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strconv"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// addIPClimateDevice registers a HmIP-style thermostat whose climate
// channel declares ACTIVE_PROFILE with MAX == profileCap, i.e. the
// device advertises P1..P<profileCap>.
func addIPClimateDevice(c *central.Unit, deviceAddress string, profileCap int) {
	dev := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     deviceAddress,
		Model:       "HmIP-eTRV-2",
	})
	ch := dev.AddChannel(deviceAddress+":1", 1, "HEATING_CLIMATECONTROL_TRANSCEIVER", hmenum.ParamsetKeyValues)
	ch.Put(generic.NewInteger(generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: deviceAddress + ":1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterActiveProfile),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeInteger,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
			Min:        json.RawMessage("1"),
			Max:        json.RawMessage(strconv.Itoa(profileCap)),
		},
	}))
	c.ModelRegistry.Put(dev)
}

// climateRawWithProfiles builds a MASTER paramset carrying `profiles`
// full week programs, so the parsed DTO really holds that many.
func climateRawWithProfiles(profiles int) map[string]any {
	out := map[string]any{}
	for p := 1; p <= profiles; p++ {
		for slot := 1; slot <= 13; slot++ {
			out[fmt.Sprintf("P%d_ENDTIME_MONDAY_%d", p, slot)] = 1440
			out[fmt.Sprintf("P%d_TEMPERATURE_MONDAY_%d", p, slot)] = 18.0
		}
	}
	return out
}

// TestCopyScheduleRefusesAProfileCountMismatch pins that copying a
// six-profile schedule onto a three-profile thermostat is refused.
//
// The destination descriptor filter drops every undeclared P4..P6 key,
// so the write used to succeed, report success to the caller and record
// a full copy in the audit log while three of the six programs never
// left the daemon. The typed sibling [SchedulesDomain.CopyScheduleTo]
// and the reference's `copy_schedule` both reject the mismatch instead.
func TestCopyScheduleRefusesAProfileCountMismatch(t *testing.T) {
	t.Parallel()
	const src, dst = "CPSRC001", "CPDST001"

	c, err := central.New(central.Config{Name: "ccu-cap"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	addIPClimateDevice(c, src, 6)
	addIPClimateDevice(c, dst, 3)

	var (
		mu   sync.Mutex
		puts []map[string]any
	)
	backend := &scheduleIOFakeBackend{}
	backend.getParamsetFn = func(_ context.Context, _ string, key hmenum.ParamsetKey) (map[string]any, error) {
		if key == hmenum.ParamsetKeyMaster {
			return climateRawWithProfiles(6), nil
		}
		return map[string]any{}, nil
	}
	backend.putParamsetFn = func(_ context.Context, _ string, _ hmenum.ParamsetKey, values map[string]any) error {
		cp := make(map[string]any, len(values))
		maps.Copy(cp, values)
		mu.Lock()
		puts = append(puts, cp)
		mu.Unlock()
		return nil
	}
	w := client.NewValueWriter()
	w.Register("ccu-cap", "HmIP-RF", backend)

	err = NewSchedulesDomain(reg, w).CopySchedule(t.Context(), src, dst)
	if !errors.Is(err, ErrProfileCountMismatch) {
		t.Fatalf("CopySchedule 6→3: got %v, want ErrProfileCountMismatch", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(puts) != 0 {
		t.Errorf("a truncated schedule still reached the destination: %v", puts)
	}
}

// TestCopyScheduleAllowsMatchingProfileCounts is the counterpart: two
// devices of the same profile capacity still copy.
func TestCopyScheduleAllowsMatchingProfileCounts(t *testing.T) {
	t.Parallel()
	const src, dst = "CPSRC002", "CPDST002"

	c, err := central.New(central.Config{Name: "ccu-cap"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	addIPClimateDevice(c, src, 3)
	addIPClimateDevice(c, dst, 3)

	backend := &scheduleIOFakeBackend{}
	backend.getParamsetFn = func(_ context.Context, _ string, key hmenum.ParamsetKey) (map[string]any, error) {
		if key == hmenum.ParamsetKeyMaster {
			return climateRawWithProfiles(3), nil
		}
		return map[string]any{}, nil
	}
	backend.putParamsetFn = func(_ context.Context, addr string, _ hmenum.ParamsetKey, values map[string]any) error {
		backend.recordPut(addr, values)
		return nil
	}
	w := client.NewValueWriter()
	w.Register("ccu-cap", "HmIP-RF", backend)

	if err := NewSchedulesDomain(reg, w).CopySchedule(t.Context(), src, dst); err != nil {
		t.Fatalf("CopySchedule 3→3: %v", err)
	}
	if backend.putCallCount() == 0 {
		t.Error("a matching copy wrote nothing")
	}
}
