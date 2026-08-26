// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package alarm

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// recordingWriter captures what the resetter puts on the wire.
type recordingWriter struct {
	calls []recordedWrite
}

type recordedWrite struct {
	channelAddress string
	parameter      hmenum.Parameter
	value          any
	priority       hmenum.CommandPriority
}

func (w *recordingWriter) SetValue(
	_ context.Context,
	channelAddress string,
	parameter hmenum.Parameter,
	value any,
	priority hmenum.CommandPriority,
) error {
	w.calls = append(w.calls, recordedWrite{channelAddress, parameter, value, priority})
	return nil
}

// putTriggerDataPoint attaches a write-only trigger parameter in the
// concrete shape the adapter's resolver would build for it.
//
// `shape` is the point of the whole fixture: the resolver picks Button
// for a parameter classified as a button action and Action otherwise,
// and the resetter must cope with both. Passing the shape in explicitly
// means a future reclassification is a one-line test change rather than
// a silent production regression.
func putTriggerDataPoint(
	ch *device.Channel,
	p hmenum.Parameter,
	w generic.Writer,
	shape string,
) {
	spec := generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(p),
		},
		Writer: w,
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeAction,
			Operations: hmenum.OperationsWrite,
		},
	}
	switch shape {
	case "button":
		ch.Put(generic.NewButton(spec))
	case "action":
		ch.Put(generic.NewAction(spec))
	default:
		panic("unknown shape " + shape)
	}
}

func newResetterFixture(
	t *testing.T,
	centralName, deviceAddress, channelAddress string,
) (*central.Registry, *device.Channel) {
	t.Helper()
	d, ch := newTestChannel(t, deviceAddress, channelAddress, 1, "MOTIONDETECTOR_TRANSCEIVER")
	reg := newCandidatesRegistry(t, centralName, d)
	return reg, ch
}

func sensorRow(centralName, channelAddress string, parameter hmenum.Parameter) sqlitestore.AlarmSensorRow {
	return sqlitestore.AlarmSensorRow{
		CentralName:    centralName,
		InterfaceID:    "HmIP-RF",
		ChannelAddress: channelAddress,
		Parameter:      string(parameter),
	}
}

// TestMotionResetterSupportsEveryResolvedTriggerShape is the regression
// guard for the defect that made the whole motion-reset feature inert on
// real hardware.
//
// RESET_MOTION is classified as a button action, so the model holds a
// [generic.Button] for it — while the resetter asserted the concrete
// [generic.Action] shape. The assertion was false for every real
// detector, Supports() returned false for all of them, and the API
// reported an empty set of resettable detectors no matter how many were
// latched. Every existing test passed because they all supplied their
// own MotionResetPort fake, so nothing exercised this lookup.
//
// The table drives the shapes the resolver can actually produce. A
// future change that narrows the lookup back to one concrete type fails
// here.
func TestMotionResetterSupportsEveryResolvedTriggerShape(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		stateParam hmenum.Parameter
		resetParam hmenum.Parameter
		shape      string
	}{
		{"motion detector, button shape", hmenum.ParameterMotion, hmenum.ParameterResetMotion, "button"},
		{"motion detector, action shape", hmenum.ParameterMotion, hmenum.ParameterResetMotion, "action"},
		{"presence detector, button shape", hmenum.ParameterPresenceDetectionState, hmenum.ParameterResetPresence, "button"},
		{"presence detector, action shape", hmenum.ParameterPresenceDetectionState, hmenum.ParameterResetPresence, "action"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			const centralName, deviceAddress = "my-ccu", "0001D3C99C1234"
			channelAddress := deviceAddress + ":1"

			reg, ch := newResetterFixture(t, centralName, deviceAddress, channelAddress)
			w := &recordingWriter{}
			putTriggerDataPoint(ch, tc.resetParam, w, tc.shape)

			m := newMotionResetter(reg)
			row := sensorRow(centralName, channelAddress, tc.stateParam)

			if !m.Supports(row) {
				_, err := m.action(row)
				t.Fatalf("Supports = false, want true (%v)", err)
			}

			if err := m.Reset(t.Context(), row); err != nil {
				t.Fatalf("Reset: %v", err)
			}

			if len(w.calls) != 1 {
				t.Fatalf("wrote %d values, want exactly 1", len(w.calls))
			}
			got := w.calls[0]
			if got.channelAddress != channelAddress {
				t.Errorf("wrote to channel %q, want %q", got.channelAddress, channelAddress)
			}
			if got.parameter != tc.resetParam {
				t.Errorf("wrote parameter %q, want %q", got.parameter, tc.resetParam)
			}
			if got.value != true {
				t.Errorf("wrote value %v, want true", got.value)
			}
			if got.priority != hmenum.CommandPriorityHigh {
				t.Errorf("wrote at priority %v, want High", got.priority)
			}
		})
	}
}

// TestMotionResetterRejectsWhatItCannotReset pins the negative half. The
// count the UI shows comes from the same predicate, so anything that
// cannot actually be written must report false rather than inflate the
// number and then fail at write time.
func TestMotionResetterRejectsWhatItCannotReset(t *testing.T) {
	t.Parallel()

	const centralName, deviceAddress = "my-ccu", "0001D3C99C1234"
	channelAddress := deviceAddress + ":1"

	t.Run("channel without a reset parameter", func(t *testing.T) {
		t.Parallel()
		reg, _ := newResetterFixture(t, centralName, deviceAddress, channelAddress)
		m := newMotionResetter(reg)
		if m.Supports(sensorRow(centralName, channelAddress, hmenum.ParameterMotion)) {
			t.Error("Supports = true for a channel carrying no RESET_MOTION")
		}
	})

	t.Run("state parameter with no reset action", func(t *testing.T) {
		t.Parallel()
		// A door contact enrols as STATE. It latches nothing and has no
		// reset action, so it must fall out of the resettable set by
		// construction rather than by a name check somewhere upstream.
		reg, ch := newResetterFixture(t, centralName, deviceAddress, channelAddress)
		putTriggerDataPoint(ch, hmenum.ParameterResetMotion, &recordingWriter{}, "button")
		m := newMotionResetter(reg)
		if m.Supports(sensorRow(centralName, channelAddress, hmenum.ParameterState)) {
			t.Error("Supports = true for a STATE sensor")
		}
	})

	t.Run("unknown central", func(t *testing.T) {
		t.Parallel()
		reg, ch := newResetterFixture(t, centralName, deviceAddress, channelAddress)
		putTriggerDataPoint(ch, hmenum.ParameterResetMotion, &recordingWriter{}, "button")
		m := newMotionResetter(reg)
		if m.Supports(sensorRow("other-ccu", channelAddress, hmenum.ParameterMotion)) {
			t.Error("Supports = true for a central the registry does not carry")
		}
	})

	t.Run("unwired registry", func(t *testing.T) {
		t.Parallel()
		m := newMotionResetter(nil)
		if m.Supports(sensorRow(centralName, channelAddress, hmenum.ParameterMotion)) {
			t.Error("Supports = true on an unwired resetter")
		}
		if err := m.Reset(t.Context(), sensorRow(centralName, channelAddress, hmenum.ParameterMotion)); err == nil {
			t.Error("Reset on an unwired resetter returned no error")
		}
	})
}
