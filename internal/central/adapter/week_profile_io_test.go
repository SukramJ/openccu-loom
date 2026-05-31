// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/schedule"
	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// wpFakeRefresher records GetParamset calls and returns a canned map.
type wpFakeRefresher struct {
	calls    int
	gotAddr  string
	gotKey   hmenum.ParamsetKey
	response map[string]any
	err      error
}

func (r *wpFakeRefresher) GetParamset(_ context.Context, addr string, key hmenum.ParamsetKey) (map[string]any, error) {
	r.calls++
	r.gotAddr = addr
	r.gotKey = key
	if r.err != nil {
		return nil, r.err
	}
	return r.response, nil
}

// wpFakeWriter records PutParamset calls.
type wpFakeWriter struct {
	calls       int
	gotAddr     string
	gotKey      hmenum.ParamsetKey
	gotValues   map[string]any
	gotPriority hmenum.CommandPriority
	err         error
}

func (w *wpFakeWriter) SetValue(_ context.Context, _ string, _ hmenum.Parameter, _ any, _ hmenum.CommandPriority) error {
	return errors.New("not used in this test")
}

func (w *wpFakeWriter) PutParamset(_ context.Context, addr string, key hmenum.ParamsetKey, values map[string]any, prio hmenum.CommandPriority) error {
	w.calls++
	w.gotAddr = addr
	w.gotKey = key
	w.gotValues = values
	w.gotPriority = prio
	return w.err
}

func TestClimateChannelLoaderReturnsErrChannelNotWiredWithoutRefresher(t *testing.T) {
	t.Parallel()
	ch := &device.Channel{Address: "ABC:1"}
	loader := &climateChannelLoader{ch: ch}
	if _, err := loader.Load(context.Background()); !errors.Is(err, ErrChannelNotWired) {
		t.Errorf("Load without refresher = %v, want ErrChannelNotWired", err)
	}
}

func TestClimateChannelSaverReturnsErrChannelNotWiredWithoutWriter(t *testing.T) {
	t.Parallel()
	ch := &device.Channel{Address: "ABC:1"}
	saver := &climateChannelSaver{ch: ch}
	c := schedule.NewClimate()
	if err := saver.Save(context.Background(), c); !errors.Is(err, ErrChannelNotWired) {
		t.Errorf("Save without writer = %v, want ErrChannelNotWired", err)
	}
}

func TestClimateChannelLoaderRoutesToRefresher(t *testing.T) {
	t.Parallel()
	ch := &device.Channel{Address: "VCU0001:1"}
	r := &wpFakeRefresher{response: map[string]any{}} // empty paramset
	ch.SetRefresher(r)
	loader := &climateChannelLoader{ch: ch}

	if _, err := loader.Load(context.Background()); err != nil {
		t.Fatalf("Load returned err: %v", err)
	}
	if r.calls != 1 {
		t.Errorf("refresher calls = %d, want 1", r.calls)
	}
	if r.gotAddr != "VCU0001:1" {
		t.Errorf("addr = %q, want %q", r.gotAddr, "VCU0001:1")
	}
	if r.gotKey != hmenum.ParamsetKeyMaster {
		t.Errorf("paramset key = %q, want MASTER", r.gotKey)
	}
}

func TestClimateChannelSaverRoutesToWriter(t *testing.T) {
	t.Parallel()
	ch := &device.Channel{Address: "VCU0001:1"}
	w := &wpFakeWriter{}
	ch.SetWriter(w)
	saver := &climateChannelSaver{ch: ch, priority: hmenum.CommandPriorityHigh}

	// Build a valid Climate fixture with one fully-covered day so the
	// saver's encode path completes.
	c := schedule.NewClimate()
	prof := schedule.NewClimateProfile()
	if err := prof.Put(schedule.WeekdayMonday, schedule.ClimateWeekday{
		Periods: []schedule.ClimatePeriod{
			{StartTime: "00:00", EndTime: "06:00", Temperature: 17.0},
			{StartTime: "06:00", EndTime: "22:00", Temperature: 21.0},
			{StartTime: "22:00", EndTime: "24:00", Temperature: 17.0},
		},
	}); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := c.Put("P1", prof); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if err := saver.Save(context.Background(), c); err != nil {
		t.Fatalf("Save returned err: %v", err)
	}
	if w.calls != 1 {
		t.Errorf("writer calls = %d, want 1", w.calls)
	}
	if w.gotAddr != "VCU0001:1" {
		t.Errorf("addr = %q, want %q", w.gotAddr, "VCU0001:1")
	}
	if w.gotKey != hmenum.ParamsetKeyMaster {
		t.Errorf("paramset key = %q, want MASTER", w.gotKey)
	}
	if w.gotPriority != hmenum.CommandPriorityHigh {
		t.Errorf("priority = %v, want High", w.gotPriority)
	}
	// Each Monday-period emits TEMPERATURE + ENDTIME under P1, so 6 keys total.
	if len(w.gotValues) < 6 {
		t.Errorf("values size = %d, want at least 6 keys (3 periods × {TEMPERATURE,ENDTIME})", len(w.gotValues))
	}
}

func TestBindClimateScheduleIOAttachesToDP(t *testing.T) {
	t.Parallel()
	ch := &device.Channel{Address: "ABC:1"}
	wp := weekprofile.NewProfileDataPoint(weekprofile.ProfileDataPointConfig{
		CentralName:    "Test",
		ChannelAddress: "ABC:1",
		ScheduleType:   weekprofile.ScheduleTypeClimate,
		ProfileCount:   6,
	})
	if wp.Climate() != nil {
		t.Fatal("fresh DP must not have a Climate profile yet")
	}
	bindClimateScheduleIO(ch, wp)
	if wp.Climate() == nil {
		t.Fatal("bindClimateScheduleIO must attach a Climate profile")
	}
}

func TestBindClimateScheduleIONilNoop(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("bindClimateScheduleIO(nil, nil) panicked: %v", r)
		}
	}()
	bindClimateScheduleIO(nil, nil)
}
