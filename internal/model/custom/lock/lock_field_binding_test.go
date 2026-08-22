// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package lock_test

import (
	"fmt"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/custom/lock"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// putLockWireDP attaches one wire data point to ch, in the shape the CCU
// describes it.
func putLockWireDP(
	t *testing.T,
	ch *device.Channel,
	param hmenum.Parameter,
	typ hmenum.ParameterType,
	valueList []string,
) {
	t.Helper()
	spec := generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(param),
		},
		Descriptor: hmproto.ParameterData{
			Type:       typ,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
			ValueList:  valueList,
		},
	}
	switch typ {
	case hmenum.ParameterTypeBool:
		ch.Put(generic.NewBinarySensor(spec))
	case hmenum.ParameterTypeEnum:
		ch.Put(generic.NewIntegerSensor(spec))
	default:
		t.Fatalf("putLockWireDP: unsupported type %v", typ)
	}
}

// newIPLockDevice builds an HmIP door lock through the real registry.
// lockChannel is the channel the lock sits on; jammedChannel is where the CCU
// reports ERROR_JAMMED. The two differ per model, which is the whole point:
// HmIP-DLD reports it on channel 0 with the lock on 1, HmIP-DLP reports it on
// the lock's own channel.
func newIPLockDevice(t *testing.T, model string, lockChannel, jammedChannel int) map[int]*device.Channel {
	t.Helper()
	dev := device.New(device.Config{
		InterfaceID:  "HmIP-RF",
		Interface:    hmenum.InterfaceHmIPRF,
		Address:      "000A1BE9957782",
		Model:        model,
		ProductGroup: hmenum.ProductGroupHmIP,
	})
	chs := map[int]*device.Channel{}
	for _, no := range []int{0, lockChannel, jammedChannel} {
		if _, seen := chs[no]; seen {
			continue
		}
		typ := "MAINTENANCE"
		if no == lockChannel {
			typ = "LOCK"
		}
		chs[no] = dev.AddChannel(fmt.Sprintf("000A1BE9957782:%d", no), no, typ, hmenum.ParamsetKeyValues)
	}
	lockCh := chs[lockChannel]
	putLockWireDP(t, lockCh, hmenum.ParameterLockState, hmenum.ParameterTypeEnum,
		[]string{"UNKNOWN", "LOCKED", "UNLOCKED"})
	putLockWireDP(t, lockCh, hmenum.ParameterLockTargetLevel, hmenum.ParameterTypeEnum,
		[]string{"LOCKED", "UNLOCKED", "OPEN"})
	// HmIP door locks report the motor's last direction under
	// ACTIVITY_STATE; they carry no DIRECTION parameter at all.
	putLockWireDP(t, lockCh, hmenum.ParameterActivityState, hmenum.ParameterTypeEnum,
		[]string{"UNKNOWN", "STABLE", "UP", "DOWN"})
	putLockWireDP(t, chs[jammedChannel], hmenum.ParameterErrorJammed, hmenum.ParameterTypeBool, nil)

	if err := custom.CreateCustomDataPoints(dev, custom.DefaultRegistry()); err != nil {
		t.Fatalf("materialize custom data points: %v", err)
	}
	return chs
}

func lockOn(t *testing.T, ch *device.Channel) *lock.Lock {
	t.Helper()
	cdp := ch.CustomDataPoint()
	if cdp == nil {
		t.Fatalf("no custom data point on %s", ch.Address)
	}
	l, ok := cdp.(*lock.Lock)
	if !ok {
		t.Fatalf("custom data point on %s is %T, want *lock.Lock", ch.Address, cdp)
	}
	return l
}

// TestIPLockBindsJammedFromTheChannelTheDeviceReportsItOn is the regression
// guard for a door lock that never reports a jammed motor.
//
// The IPLock profile maps FieldError → ERROR_JAMMED at ChannelFields[-1], and
// the binding read the parameter off the lock's own channel instead. The two
// disagree on the HmIP-DLD, which sits on channel 1 and reports the fault on
// channel 0, so `is_jammed` stayed false however hard the motor jammed.
//
// Both models are asserted together on purpose. A schema-only switch fixes
// the DLD and breaks the DLP, whose ERROR_JAMMED is on the lock's own
// channel — the schema offset is right for one and wrong for the other, so
// the binding has to fall back to the channel that actually carries the
// parameter.
func TestIPLockBindsJammedFromTheChannelTheDeviceReportsItOn(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		model                      string
		lockChannel, jammedChannel int
	}{
		{"HmIP-DLD", 1, 0},   // schema's -1 offset is correct here
		{"HmIP-DLP", 12, 12}, // ... and wrong here
	} {
		t.Run(tc.model, func(t *testing.T) {
			t.Parallel()
			chs := newIPLockDevice(t, tc.model, tc.lockChannel, tc.jammedChannel)
			l := lockOn(t, chs[tc.lockChannel])

			if l.IsJammed() {
				t.Fatal("IsJammed() is true before the fault was reported")
			}
			jammed, ok := chs[tc.jammedChannel].Parameter(hmenum.ParameterErrorJammed).(*generic.BinarySensor)
			if !ok {
				t.Fatalf("ERROR_JAMMED on channel %d is not a binary sensor", tc.jammedChannel)
			}
			jammed.OnEvent(true)

			if !l.IsJammed() {
				t.Errorf("IsJammed() = false after ERROR_JAMMED=true on channel %d — the slot is unbound",
					tc.jammedChannel)
			}
		})
	}
}

// TestIPLockBindsDirectionFromActivityState is the second half: the profile
// maps FieldDirection → ACTIVITY_STATE for the IP families, while the binding
// looked for DIRECTION — a parameter HmIP door locks do not carry. The
// accessor therefore reported no direction on every IP lock ever built.
func TestIPLockBindsDirectionFromActivityState(t *testing.T) {
	t.Parallel()
	chs := newIPLockDevice(t, "HmIP-DLD", 1, 0)
	l := lockOn(t, chs[1])

	activity, ok := chs[1].Parameter(hmenum.ParameterActivityState).(*generic.Sensor[int32])
	if !ok {
		t.Fatalf("ACTIVITY_STATE is not an enum sensor")
	}
	idx, found := custom.EnumLabelIndex(activity, "UP")
	if !found {
		t.Fatal(`"UP" not in the ACTIVITY_STATE value list`)
	}
	activity.OnEvent(idx)

	got, ok := l.Direction()
	if !ok {
		t.Fatal("Direction() reported nothing after ACTIVITY_STATE was fed — the slot is unbound")
	}
	if string(got) != "UP" {
		t.Errorf("Direction() = %q, want %q", got, "UP")
	}
}
