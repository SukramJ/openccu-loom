// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// refreshObservable is the capability every [generic.DataPoint] offers:
// a hook that fires exactly when the data point's RefreshedAt moves — a
// CCU-confirmed observation, changed or not. Checked as an interface so
// the bridge never depends on the concrete generic type.
type refreshObservable interface {
	OnRefreshed(fn func(at time.Time)) func()
}

// deviceRefreshThrottleWindow is how long one [hmevent.DeviceRefreshedEvent]
// silences further ones for the same device. A single VALUES paramset
// read fills every parameter of a channel in one pass, and a periodic
// sweep does that for every channel of the device — without the throttle
// each of those observations would become its own bus event and its own
// WebSocket frame, carrying no information the first one did not.
const deviceRefreshThrottleWindow = 2 * time.Second

// deviceRefreshPublisher collapses the per-data-point refresh
// observations of one device into at most one bus event per throttle
// window. It publishes on the leading edge: the first observation after a
// quiet period goes out immediately and opens the window, everything
// inside the window is dropped. The event therefore carries the time the
// window opened, which can trail the device's true latest observation by
// up to one window — consumers that need the exact value read
// [device.Device.LastRefreshed] from the model.
type deviceRefreshPublisher struct {
	bus           *events.Bus
	centralName   string
	interfaceID   string
	deviceAddress string
	window        time.Duration

	mu             sync.Mutex
	windowOpenedAt time.Time
}

// observe records one data-point observation stamped at and publishes a
// [hmevent.DeviceRefreshedEvent] unless an earlier observation already
// opened a window that at still falls into.
func (p *deviceRefreshPublisher) observe(at time.Time) {
	p.mu.Lock()
	if !p.windowOpenedAt.IsZero() {
		if since := at.Sub(p.windowOpenedAt); since >= 0 && since < p.window {
			p.mu.Unlock()
			return
		}
	}
	p.windowOpenedAt = at
	p.mu.Unlock()
	p.bus.Publish(hmevent.DeviceRefreshedEvent{
		Base:          hmevent.NewBaseAt(at),
		CentralName:   p.centralName,
		InterfaceID:   p.interfaceID,
		DeviceAddress: p.deviceAddress,
		RefreshedAt:   at,
	})
}

// wireDeviceRefreshedToBus registers one refresh observer on every VALUES
// data point of dev that feeds a device-wide throttled
// [hmevent.DeviceRefreshedEvent] publisher. It is what lets a WebSocket
// client learn that a device's "last refreshed" time moved without
// polling — including for a periodic sweep whose readings did not change
// and so published no value-changed event.
//
// VALUES only, to match [device.Device.LastRefreshed]: the aggregate the
// event announces is computed over the same set, so an observer never sees
// the event without the REST value having moved too.
//
// No-op when bus or dev is nil. The returned unsubscribes are dropped on
// purpose, as in [bridgeDataPointRollbacksToBus]: the subscriptions live
// as long as the data-point objects, and a re-ingest after a device
// disappears and reappears builds fresh data points that this function is
// run over again.
func wireDeviceRefreshedToBus(bus *events.Bus, centralName string, dev *device.Device) {
	if bus == nil || dev == nil {
		return
	}
	pub := &deviceRefreshPublisher{
		bus:           bus,
		centralName:   centralName,
		interfaceID:   dev.InterfaceID,
		deviceAddress: dev.Address,
		window:        deviceRefreshThrottleWindow,
	}
	for _, ch := range dev.Channels() {
		for _, dp := range ch.DataPoints() {
			obs, ok := dp.(refreshObservable)
			if !ok {
				continue
			}
			obs.OnRefreshed(pub.observe)
		}
	}
}
