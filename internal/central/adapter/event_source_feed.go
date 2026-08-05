// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"strconv"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	modevent "github.com/SukramJ/openccu-loom/internal/model/event"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// EventSourceFeed fires a channel's model-side event sources from the
// device-trigger events the CCU pushes.
//
// Delivery of a trigger never depended on this: a keypress reaches the
// WebSocket and the diagnostics stream straight off the bus. What depended
// on it is the model's memory of the trigger — [modevent.Group] records the
// last member that fired, which is what
// `GET /devices/{addr}/channels/{no}/event-groups` reports as
// `last_triggered_event`. With nothing calling Fire, every group described
// which triggers a channel offers and none of them had ever fired, so a
// client could enumerate its event entities but never learn that one had
// been pressed.
//
// The feed reads the same bus the trigger is published on rather than
// hooking the coordinator that publishes it: the coordinator holds no model
// and would need one injected to resolve a channel, which is a collaborator
// seam bought for nothing when the event already carries the address.
//
// loom:reachable:reason="constructed by the daemon composition root (cmd/openccu-loom/daemon.go) and started per adopted central through the orchestrator hook; the reachability heuristic counts a type as unreachable when production never writes its name, and production only ever holds this one via := from NewEventSourceFeed"
type EventSourceFeed struct {
	reg    *central.Registry
	unsubs []func()
}

// NewEventSourceFeed returns a feed bound to reg.
func NewEventSourceFeed(reg *central.Registry) *EventSourceFeed {
	return &EventSourceFeed{reg: reg}
}

// Start attaches a subscription to every registered central's event bus.
func (f *EventSourceFeed) Start() {
	if f == nil || f.reg == nil {
		return
	}
	for _, u := range f.reg.List() {
		if unsub := f.StartCentral(u); unsub != nil {
			f.unsubs = append(f.unsubs, unsub)
		}
	}
}

// StartCentral subscribes a single central and returns its unsubscribe.
//
// Start only walks the registry as it stands when it runs, so a central
// adopted at runtime needs this called explicitly — otherwise its channels
// keep event groups that can never record a trigger, which looks exactly
// like a fleet whose buttons nobody has pressed.
func (f *EventSourceFeed) StartCentral(u *central.Unit) func() {
	if f == nil || u == nil || u.EventBus == nil {
		return nil
	}
	return events.Subscribe(u.EventBus, func(e hmevent.DeviceTriggerEvent) {
		q := u.QueryFacade()
		if q == nil {
			return
		}
		channelAddr := e.DeviceAddress + ":" + strconv.Itoa(e.ChannelNo)
		attached := q.GetEventGroup(channelAddr, hmenum.Parameter(e.Parameter))
		if attached == nil {
			return
		}
		// Only concrete sources carry the fire lifecycle; any other
		// implementation of the interface has no timestamp to record.
		src, ok := attached.(*modevent.Source)
		if !ok {
			return
		}
		// Fire applies the device-error transition gate internally, so a
		// repeated identical error value stays a single reported fault
		// rather than a stream of them.
		src.Fire(e.Value.Unwrap())
	})
}

// Stop detaches every subscription. Idempotent.
func (f *EventSourceFeed) Stop() {
	if f == nil {
		return
	}
	for _, unsub := range f.unsubs {
		if unsub != nil {
			unsub()
		}
	}
	f.unsubs = nil
}
