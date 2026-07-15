// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"log/slog"

	"github.com/SukramJ/openccu-loom/internal/alarm"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/internal/north/rest/ws"
)

// wireSystemStatusSubscribers stands up the north-bound forwarders for
// SystemStatusChangedEvent and the related lifecycle / trigger /
// optimistic-rollback event streams. All WS subscribers are started
// unconditionally so their ring buffers accumulate events regardless of
// whether a particular north-bound plane is active; the MQTT publisher is
// started only when MQTT wiring is present.
//
// The returned [handlers.SystemStatusBuffer] feeds the REST SystemStatus
// surface and is consumed later in the composition root. The returned
// teardown runs every subscriber's Stop in the same LIFO order the inline
// defers would have executed.
func wireSystemStatusSubscribers(
	reg *central.Registry,
	wsHub *ws.Hub,
	mqttWiring *mqtt.Wiring,
	alarmSvc *alarm.Service,
	logger *slog.Logger,
) (sysStatusBuf *handlers.SystemStatusBuffer, teardown func()) {
	sysStatusBuf = handlers.NewSystemStatusBuffer(100)
	stopSysStatusBuf := sysStatusBuf.Subscribe(reg)

	wsSysStatus := ws.NewSystemStatusSubscriber(reg, wsHub)
	wsSysStatus.Start()

	wsHubEvents := ws.NewHubEventsSubscriber(reg, wsHub)
	wsHubEvents.Start()

	wsDeviceLifecycle := ws.NewDeviceLifecycleSubscriber(reg, wsHub)
	wsDeviceLifecycle.Start()

	wsDeviceTrigger := ws.NewDeviceTriggerSubscriber(reg, wsHub)
	wsDeviceTrigger.Start()

	wsOptimisticRollback := ws.NewOptimisticRollbackSubscriber(reg, wsHub)
	wsOptimisticRollback.Start()

	// The alarm_panel.* broadcast subscriber rides the daemon-level alarm
	// event bus (areas are daemon-level, not per-central), so it binds to
	// alarmSvc.Bus() rather than fanning across the registry. Nil when the
	// alarm service is disabled.
	var wsAlarm *ws.AlarmPanelSubscriber
	if alarmSvc != nil {
		wsAlarm = ws.NewAlarmPanelSubscriber(alarmSvc.Bus(), wsHub)
		wsAlarm.Start()
	}

	var mqttSysStatus *mqtt.SystemStatusPublisher
	if mqttWiring != nil {
		mqttSysStatus = mqtt.NewSystemStatusPublisher(reg, mqttWiring, logger)
		mqttSysStatus.Start() //nolint:contextcheck // Start has no ctx parameter; it subscribes to the event bus internally
	}

	teardown = func() {
		// LIFO: mirror the order the original inline defers would have run.
		if mqttSysStatus != nil {
			mqttSysStatus.Stop()
		}
		if wsAlarm != nil {
			wsAlarm.Stop()
		}
		wsOptimisticRollback.Stop()
		wsDeviceTrigger.Stop()
		wsDeviceLifecycle.Stop()
		wsHubEvents.Stop()
		wsSysStatus.Stop()
		stopSysStatusBuf()
	}

	return sysStatusBuf, teardown
}
