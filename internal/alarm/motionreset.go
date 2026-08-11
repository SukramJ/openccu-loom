// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

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

// motionResetter implements engine.MotionResetPort by writing the
// RESET_MOTION parameter of an enrolled sensor's own channel.
//
// A motion detector exposes MOTION (the state the alarm watches) and
// RESET_MOTION (a write-only action that clears it) on the same
// channel, so the enrolled sensor row already carries everything
// needed to find the target — no second enrolment, no configuration.
type motionResetter struct {
	reg *central.Registry
}

// newMotionResetter binds the port to the central registry. A nil
// registry yields a resetter that supports nothing, which leaves the
// feature inert rather than panicking on a partially wired daemon.
func newMotionResetter(reg *central.Registry) *motionResetter {
	return &motionResetter{reg: reg}
}

// action resolves the sensor's RESET_MOTION data point.
//
// The lookup is the definition of "resettable" for the whole feature:
// Supports and Reset both go through it, so the count the UI shows and
// the set the button writes to cannot disagree.
func (m *motionResetter) action(row sqlitestore.AlarmSensorRow) (*generic.Action, error) {
	if m == nil || m.reg == nil {
		return nil, errors.New("alarm: motion reset not wired")
	}
	u, ok := m.reg.Get(row.CentralName)
	if !ok {
		return nil, fmt.Errorf("alarm: unknown central %q", row.CentralName)
	}
	ch := u.GetChannel(row.ChannelAddress)
	if ch == nil {
		return nil, fmt.Errorf("alarm: unknown channel %q on %q", row.ChannelAddress, row.CentralName)
	}
	dp := ch.Parameter(hmenum.ParameterResetMotion)
	if dp == nil {
		return nil, fmt.Errorf("alarm: channel %q has no %s", row.ChannelAddress, hmenum.ParameterResetMotion)
	}
	act, ok := dp.(*generic.Action)
	if !ok {
		return nil, fmt.Errorf("alarm: %s on %q is not an action data point",
			hmenum.ParameterResetMotion, row.ChannelAddress)
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
	return act.Trigger(ctx, true, hmenum.CommandPriorityHigh)
}
