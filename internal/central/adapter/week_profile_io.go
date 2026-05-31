// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"errors"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/schedule"
	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ErrChannelNotWired is returned by the channel-scoped loader / saver
// adapters when the underlying [device.Channel.Refresher] /
// [device.Channel.Writer] is nil — the channel was hydrated but no backend
// has been bound yet.
var ErrChannelNotWired = errors.New("schedule: channel has no backend bound")

// climateChannelLoader implements [weekprofile.Loader[*schedule.Climate]]
// against a [device.Channel]. It pulls the channel's MASTER paramset
// via the installed refresher, parses the slot parameters into a
// [schedule.Climate], and is reused for every Load call (no caching —
// the surrounding [weekprofile.Profile] holds the snapshot).
type climateChannelLoader struct {
	ch *device.Channel
}

// Load reads the channel's MASTER paramset and returns a parsed
// [schedule.Climate]. Returns [ErrChannelNotWired] when no refresher
// is installed on the channel.
func (l *climateChannelLoader) Load(ctx context.Context) (*schedule.Climate, error) {
	r := l.ch.Refresher()
	if r == nil {
		return nil, ErrChannelNotWired
	}
	values, err := r.GetParamset(ctx, l.ch.Address, hmenum.ParamsetKeyMaster)
	if err != nil {
		return nil, fmt.Errorf("schedule.load: GetParamset: %w", err)
	}
	rawSched, err := weekprofile.ParseClimateRawParamset(values)
	if err != nil {
		return nil, fmt.Errorf("schedule.load: parse raw paramset: %w", err)
	}
	c, err := weekprofile.RawToClimate(rawSched)
	if err != nil {
		return nil, fmt.Errorf("schedule.load: convert to climate: %w", err)
	}
	return c, nil
}

// climateChannelSaver implements [weekprofile.Saver[*schedule.Climate]].
// Encodes the schedule into the wire-form raw paramset and writes it
// back via the installed [device.ChannelWriter.PutParamset].
type climateChannelSaver struct {
	ch       *device.Channel
	priority hmenum.CommandPriority
}

// Save converts c into the CCU MASTER paramset wire form and writes
// it through the channel's writer. Returns [ErrChannelNotWired] when
// no writer has been installed.
func (s *climateChannelSaver) Save(ctx context.Context, c *schedule.Climate) error {
	w := s.ch.Writer()
	if w == nil {
		return ErrChannelNotWired
	}
	rawSched, err := weekprofile.ClimateToRawWire(c)
	if err != nil {
		return fmt.Errorf("schedule.save: convert from climate: %w", err)
	}
	values, err := weekprofile.BuildClimateRawParamset(rawSched)
	if err != nil {
		return fmt.Errorf("schedule.save: build raw paramset: %w", err)
	}
	if err := w.PutParamset(ctx, s.ch.Address, hmenum.ParamsetKeyMaster, values, s.priority); err != nil {
		return fmt.Errorf("schedule.save: PutParamset: %w", err)
	}
	return nil
}

// bindClimateScheduleIO constructs and attaches a fully-wired
// [weekprofile.ClimateProfile] (a `Profile[*schedule.Climate]`) to wp,
// using ch's installed refresher / writer. Subsequent
// `wp.Climate().Load(ctx)` and `wp.Climate().Save(ctx, sched)` calls
// then route directly through ch without going through SchedulesDomain.
//
// Idempotent: replaces a previously attached profile, so a daemon
// restart cycle (re-hydration) updates the wiring cleanly. The two
// pathways (this DP-bound profile + the SchedulesDomain) remain
// independent for now; SchedulesDomain still keeps its own cache,
// but new code that prefers the DP-centric API can use the bound
// profile directly.
func bindClimateScheduleIO(ch *device.Channel, wp *weekprofile.ProfileDataPoint) {
	if ch == nil || wp == nil {
		return
	}
	loader := &climateChannelLoader{ch: ch}
	saver := &climateChannelSaver{ch: ch, priority: hmenum.CommandPriorityHigh}
	profile := weekprofile.NewClimate(loader, saver)
	wp.AttachClimateProfile(profile)
}

// defaultChannelLoader implements [weekprofile.Loader[*schedule.Simple]]
// against a [device.Channel]. Reads the channel's MASTER paramset via the
// installed refresher and parses the `<NN>_WP_<FIELD>` keys into a
// [schedule.Simple].
type defaultChannelLoader struct {
	ch *device.Channel
}

// Load reads the channel's MASTER paramset and returns a parsed Simple
// schedule. Returns [ErrChannelNotWired] when no refresher is installed.
func (l *defaultChannelLoader) Load(ctx context.Context) (*schedule.Simple, error) {
	r := l.ch.Refresher()
	if r == nil {
		return nil, ErrChannelNotWired
	}
	values, err := r.GetParamset(ctx, l.ch.Address, hmenum.ParamsetKeyMaster)
	if err != nil {
		return nil, fmt.Errorf("schedule.load.simple: GetParamset: %w", err)
	}
	s, err := weekprofile.ParseSimpleRawParamset(values)
	if err != nil {
		return nil, fmt.Errorf("schedule.load.simple: parse raw paramset: %w", err)
	}
	return s, nil
}

// defaultChannelSaver implements [weekprofile.Saver[*schedule.Simple]].
// Encodes the schedule into the wire-form raw paramset and writes it
// via the installed [device.ChannelWriter.PutParamset].
type defaultChannelSaver struct {
	ch       *device.Channel
	priority hmenum.CommandPriority
}

// Save converts s into the CCU MASTER paramset wire form and writes it
// through the channel's writer. Inactive groups (1..24) are explicitly
// zeroed so the CCU deactivates deleted entries. Returns
// [ErrChannelNotWired] when no writer has been installed.
func (sv *defaultChannelSaver) Save(ctx context.Context, s *schedule.Simple) error {
	w := sv.ch.Writer()
	if w == nil {
		return ErrChannelNotWired
	}
	values := weekprofile.BuildSimpleRawParamset(s)
	if err := w.PutParamset(ctx, sv.ch.Address, hmenum.ParamsetKeyMaster, values, sv.priority); err != nil {
		return fmt.Errorf("schedule.save.simple: PutParamset: %w", err)
	}
	return nil
}

// bindDefaultScheduleIO attaches a fully-wired
// [weekprofile.DefaultProfile] (a `Profile[*schedule.Simple]`) to wp,
// using ch's installed refresher / writer. After this call,
// `wp.Simple().Load(ctx)` and `wp.Simple().Save(ctx, sched)` route
// directly through ch.
//
// Idempotent: replaces a previously attached profile so a daemon
// restart cycle re-wires cleanly.
func bindDefaultScheduleIO(ch *device.Channel, wp *weekprofile.ProfileDataPoint) {
	if ch == nil || wp == nil {
		return
	}
	loader := &defaultChannelLoader{ch: ch}
	saver := &defaultChannelSaver{ch: ch, priority: hmenum.CommandPriorityHigh}
	profile := weekprofile.NewDefault(loader, saver)
	wp.AttachSimpleProfile(profile)
}
