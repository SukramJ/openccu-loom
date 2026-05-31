// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package device

import (
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

type fakeAttachable struct {
	key hmtypes.DataPointKey
}

func (f *fakeAttachable) DataPointKey() hmtypes.DataPointKey { return f.key }

type fakeEvent struct {
	key  hmtypes.DataPointKey
	kind string
}

func (f *fakeEvent) DataPointKey() hmtypes.DataPointKey { return f.key }
func (f *fakeEvent) EventKind() string                  { return f.kind }

func newAggregateDevice() *Device {
	return New(Config{
		InterfaceID: "HmIP-RF",
		Address:     "ABC0001",
		Model:       "HmIP-X",
	})
}

func TestChannelAttachCalculatedAndCustomDataPoints(t *testing.T) {
	d := newAggregateDevice()
	ch := d.AddChannel("ABC0001:1", 1, "SHUTTER_TRANSMITTER", hmenum.ParamsetKeyValues)

	calc1 := &fakeAttachable{key: hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: "DEW_POINT"}}
	calc2 := &fakeAttachable{key: hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: "BATTERY_PCT"}}
	ch.AttachCalculatedDataPoint(calc1)
	ch.AttachCalculatedDataPoint(calc2)
	ch.AttachCalculatedDataPoint(nil) // no-op

	got := ch.CalculatedDataPoints()
	if len(got) != 2 {
		t.Fatalf("expected 2 calculated DPs, got %d", len(got))
	}
	// Sorted by key.Parameter (BATTERY_PCT < DEW_POINT).
	if got[0].DataPointKey().Parameter != "BATTERY_PCT" {
		t.Fatalf("expected BATTERY_PCT first, got %s", got[0].DataPointKey().Parameter)
	}

	custom := &fakeAttachable{key: hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: "COVER"}}
	ch.SetCustomDataPoint(custom)
	if ch.CustomDataPoint() != custom {
		t.Fatalf("custom DP not bound")
	}
	ch.SetCustomDataPoint(nil)
	if ch.CustomDataPoint() != nil {
		t.Fatalf("custom DP should clear on nil")
	}
}

func TestDeviceAggregatesAcrossChannels(t *testing.T) {
	d := newAggregateDevice()
	ch1 := d.AddChannel("ABC0001:1", 1, "T1", hmenum.ParamsetKeyValues)
	ch2 := d.AddChannel("ABC0001:2", 2, "T2", hmenum.ParamsetKeyValues)

	ch1.AttachCalculatedDataPoint(&fakeAttachable{key: hmtypes.DataPointKey{ChannelAddress: ch1.Address, Parameter: "DEW_POINT"}})
	ch2.AttachCalculatedDataPoint(&fakeAttachable{key: hmtypes.DataPointKey{ChannelAddress: ch2.Address, Parameter: "BATTERY_PCT"}})

	cust1 := &fakeAttachable{key: hmtypes.DataPointKey{ChannelAddress: ch1.Address, Parameter: "COVER"}}
	ch1.SetCustomDataPoint(cust1)

	if got := d.CalculatedDataPoints(); len(got) != 2 {
		t.Fatalf("device calculated aggregate = %d want 2", len(got))
	}
	if got := d.CustomDataPoints(); len(got) != 1 || got[0] != cust1 {
		t.Fatalf("device custom aggregate = %d / mismatch", len(got))
	}
}

func TestDeviceGenericEventsAggregate(t *testing.T) {
	d := newAggregateDevice()
	ch := d.AddChannel("ABC0001:1", 1, "BUTTON", hmenum.ParamsetKeyValues)
	ch.AttachGenericEvent(&fakeEvent{
		key:  hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: "PRESS_SHORT"},
		kind: "homematic.keypress",
	})
	ch.AttachGenericEvent(&fakeEvent{
		key:  hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: "PRESS_LONG"},
		kind: "homematic.keypress",
	})

	if got := d.GenericEvents(); len(got) != 2 {
		t.Fatalf("device generic-events aggregate = %d want 2", len(got))
	}
}

func TestDeviceSubscribeToDeviceUpdated(t *testing.T) {
	d := newAggregateDevice()
	var hits atomic.Int32
	unsub := d.SubscribeToDeviceUpdated(func() { hits.Add(1) })
	d.NotifyUpdated()
	d.NotifyUpdated()
	if hits.Load() != 2 {
		t.Fatalf("handler hits = %d want 2", hits.Load())
	}
	unsub()
	unsub() // idempotent
	d.NotifyUpdated()
	if hits.Load() != 2 {
		t.Fatalf("after unsubscribe, hits = %d want 2", hits.Load())
	}
}

type categorised struct {
	key      hmtypes.DataPointKey
	category hmenum.DataPointCategory
}

func (c *categorised) DataPointKey() hmtypes.DataPointKey { return c.key }
func (c *categorised) Category() hmenum.DataPointCategory { return c.category }

func TestChannelAndDeviceDataPointsByCategoryFilter(t *testing.T) {
	d := newAggregateDevice()
	ch1 := d.AddChannel("ABC0001:1", 1, "T1", hmenum.ParamsetKeyValues)
	ch2 := d.AddChannel("ABC0001:2", 2, "T2", hmenum.ParamsetKeyValues)

	ch1.AttachCalculatedDataPoint(&categorised{
		key:      hmtypes.DataPointKey{ChannelAddress: ch1.Address, Parameter: "DEW_POINT"},
		category: hmenum.DataPointCategorySensor,
	})
	ch1.AttachCalculatedDataPoint(&categorised{
		key:      hmtypes.DataPointKey{ChannelAddress: ch1.Address, Parameter: "WINDOW_OPEN"},
		category: hmenum.DataPointCategoryBinarySensor,
	})
	ch2.SetCustomDataPoint(&categorised{
		key:      hmtypes.DataPointKey{ChannelAddress: ch2.Address, Parameter: "COVER"},
		category: hmenum.DataPointCategoryCover,
	})

	if got := ch1.DataPointsByCategory(hmenum.DataPointCategorySensor); len(got) != 1 {
		t.Fatalf("ch1 sensors = %d want 1", len(got))
	}
	if got := ch1.DataPointsByCategory(hmenum.DataPointCategoryBinarySensor); len(got) != 1 {
		t.Fatalf("ch1 binary_sensors = %d want 1", len(got))
	}
	if got := d.DataPointsByCategory(hmenum.DataPointCategoryCover); len(got) != 1 {
		t.Fatalf("device covers = %d want 1", len(got))
	}
	if got := d.DataPointsByCategory(hmenum.DataPointCategorySensor); len(got) != 1 {
		t.Fatalf("device sensors = %d want 1", len(got))
	}
}

func TestDeviceAddChannelToGroup(t *testing.T) {
	d := newAggregateDevice()
	d.AddChannelToGroup(1, 1)
	d.AddChannelToGroup(1, 2)
	d.AddChannelToGroup(1, 1) // duplicate ignored
	d.AddChannelToGroup(2, 5)

	g1 := d.GroupChannels(1)
	if len(g1) != 2 || g1[0] != 1 || g1[1] != 2 {
		t.Fatalf("group 1 members = %v want [1 2]", g1)
	}
	g2 := d.GroupChannels(2)
	if len(g2) != 1 || g2[0] != 5 {
		t.Fatalf("group 2 members = %v want [5]", g2)
	}
	if got := d.GroupChannels(99); got != nil {
		t.Fatalf("unknown group should be nil, got %v", got)
	}
}
