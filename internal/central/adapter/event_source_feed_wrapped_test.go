// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	modevent "github.com/SukramJ/openccu-loom/internal/model/event"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// wrappedEventSource is a decorator over an event source, the shape the
// visibility package already attaches to channels when it needs to add a
// gate method. It satisfies device.AttachableEvent and forwards Fire.
type wrappedEventSource struct {
	src *modevent.Source
}

func (w *wrappedEventSource) DataPointKey() hmtypes.DataPointKey { return w.src.DataPointKey() }
func (w *wrappedEventSource) EventKind() string                  { return w.src.EventKind() }
func (w *wrappedEventSource) Fire(value any) bool                { return w.src.Fire(value) }

// TestEventSourceFeedFiresAWrappedSource pins that a decorated event source
// still records its trigger.
//
// The model's memory of a keypress is what
// `GET /devices/{addr}/channels/{no}/event-groups` reports as
// last_triggered_event. Narrowing the attached event to one concrete struct
// drops every wrapper on the floor without an error, so the group keeps
// reporting that nothing was ever pressed — indistinguishable from a fleet
// whose buttons nobody has touched.
func TestEventSourceFeedFiresAWrappedSource(t *testing.T) {
	t.Parallel()

	u, err := central.New(central.Config{Name: "ccu-evfeed"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	dev := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "0001EVFD",
		Model:       "HmIP-WRC2",
		Name:        "Wandtaster",
	})
	ch := dev.AddChannel("0001EVFD:1", 1, "KEY", hmenum.ParamsetKeyValues)
	src := modevent.NewSource("0001EVFD:1", hmenum.ParameterPressShort)
	if src == nil {
		t.Fatal("NewSource returned nil — PRESS_SHORT must classify as an event")
	}
	ch.AttachGenericEvent(&wrappedEventSource{src: src})
	u.ModelRegistry.Put(dev)

	feed := NewEventSourceFeed(nil)
	unsub := feed.StartCentral(u)
	if unsub == nil {
		t.Fatal("StartCentral returned no unsubscribe")
	}
	defer unsub()

	val, err := hmtypes.NewParamValue(true)
	if err != nil {
		t.Fatalf("NewParamValue: %v", err)
	}
	events.Publish(u.EventBus, hmevent.DeviceTriggerEvent{
		CentralName:   "ccu-evfeed",
		InterfaceID:   "HmIP-RF",
		DeviceAddress: "0001EVFD",
		ChannelNo:     1,
		EventType_:    hmenum.DeviceTriggerEventTypeKeypress,
		Parameter:     string(hmenum.ParameterPressShort),
		Value:         val,
	})

	if _, _, fired := src.LastFire(); !fired {
		t.Error("the wrapped event source recorded no fire — the feed dropped it")
	}
}
