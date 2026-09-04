// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package alarm

import (
	"context"
	"errors"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// motionResetter implements engine.MotionResetPort by writing the reset
// parameter of an enrolled sensor's own channel.
//
// A latching presence sensor exposes two parameters on one channel: the
// state the alarm watches, and a write-only action that clears it. The
// enrolled sensor row already names the first, so the second follows
// from it — no second enrolment, no configuration.
type motionResetter struct {
	reg *central.Registry
}

// resetParameterFor maps a watched state parameter to the action that
// clears it.
//
// The two families are separate devices, not synonyms: an HmIP motion
// detector (HmIP-SMI, HmIP-SMO-2) latches MOTION and clears it with
// RESET_MOTION, while an HmIP presence detector (HmIP-SPI, HmIPW-SPI)
// latches PRESENCE_DETECTION_STATE and clears it with RESET_PRESENCE.
// Both enrol as sensor type "motion", so keying the reset off the sensor
// type instead of the parameter would silently skip every presence
// detector.
//
// Both are registered as stop-shaped state parameters (HMIPServer
// de.eq3.cbcs.devicedescription.channelspecification.StateParameterFactory,
// via #createStop — which also registers the unrelated STOP parameter, so
// the pair is "the only two RESET_* parameters", not "the only two stops"),
// and the descriptor corpus shows the pairing is
// strict: RESET_MOTION occurs only beside MOTION (on
// MOTIONDETECTOR_TRANSCEIVER and MOTIONDETECTOR_VIRTUAL_TRANSCEIVER),
// RESET_PRESENCE only beside PRESENCE_DETECTION_STATE (on
// PRESENCEDETECTOR_TRANSCEIVER), never crossed, and no third RESET_*
// parameter exists on any channel.
//
// Both are HmIP-only. Nothing under ../OpenCCU-Base/src/devicetypes/
// mentions RESET_MOTION, and a classic BidCos detector — HM-Sec-MDIR's
// MOTION_DETECTOR channel, like all eleven of that channel type — carries
// MOTION alone. Those devices are un-latched by the CCU rather than by a
// write: the device XML declares MOTION with
// `<domino_event value="0" delay_id="NEXT_TRANSMISSION"/>`
// (../OpenCCU-Base/src/devicetypes/rftypes/rf_sec_mdir.xml:227-231), which
// rfd turns into a server-side autotimer. They fall out of the resettable
// set here by having no reset data point, which is the right answer.
func resetParameterFor(stateParameter string) (hmenum.Parameter, bool) {
	switch hmenum.Parameter(stateParameter) {
	case hmenum.ParameterMotion:
		return hmenum.ParameterResetMotion, true
	case hmenum.ParameterPresenceDetectionState:
		return hmenum.ParameterResetPresence, true
	default:
		return "", false
	}
}

// newMotionResetter binds the port to the central registry. A nil
// registry yields a resetter that supports nothing, which leaves the
// feature inert rather than panicking on a partially wired daemon.
func newMotionResetter(reg *central.Registry) *motionResetter {
	return &motionResetter{reg: reg}
}

// action resolves the sensor's reset data point.
//
// The lookup is the definition of "resettable" for the whole feature:
// Supports and Reset both go through it, so the count the UI shows and
// the set the button writes to cannot disagree.
//
// The result is a [generic.ActionTrigger], not a concrete shape. Both
// reset parameters are classified as button actions, so the resolver
// builds a [generic.Button] for them, while a parameter outside that
// classification becomes a [generic.Action]. Depending on either
// concrete type makes the lookup fail for every real detector without a
// compile error.
func (m *motionResetter) action(row sqlitestore.AlarmSensorRow) (generic.ActionTrigger, error) {
	if m == nil || m.reg == nil {
		return nil, errors.New("alarm: motion reset not wired")
	}
	resetParam, ok := resetParameterFor(row.Parameter)
	if !ok {
		return nil, fmt.Errorf("alarm: %q on %q has no reset action",
			row.Parameter, row.ChannelAddress)
	}
	u, ok := m.reg.Get(row.CentralName)
	if !ok {
		return nil, fmt.Errorf("alarm: unknown central %q", row.CentralName)
	}
	ch := u.GetChannel(row.ChannelAddress)
	if ch == nil {
		return nil, fmt.Errorf("alarm: unknown channel %q on %q", row.ChannelAddress, row.CentralName)
	}
	dp := ch.Parameter(resetParam)
	if dp == nil {
		return nil, fmt.Errorf("alarm: channel %q has no %s", row.ChannelAddress, resetParam)
	}
	act, ok := dp.(generic.ActionTrigger)
	if !ok {
		return nil, fmt.Errorf("alarm: %s on %q is not a triggerable data point (%T)",
			resetParam, row.ChannelAddress, dp)
	}
	return act, nil
}

// Supports implements engine.MotionResetPort.
func (m *motionResetter) Supports(row sqlitestore.AlarmSensorRow) bool {
	_, err := m.action(row)
	return err == nil
}

// Reset implements engine.MotionResetPort.
//
// The write runs at high priority rather than critical: it is an
// operator-initiated action on the arming path, so it must not queue
// behind routine traffic, but it is not a safety command and must not
// compete with siren control during an incident.
func (m *motionResetter) Reset(ctx context.Context, row sqlitestore.AlarmSensorRow) error {
	act, err := m.action(row)
	if err != nil {
		return err
	}
	return act.FireAction(ctx, hmenum.CommandPriorityHigh)
}
