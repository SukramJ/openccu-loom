// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"log/slog"

	"github.com/SukramJ/openccu-loom/internal/alarm"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/internal/north/rest/ws"
	"github.com/SukramJ/openccu-loom/internal/security"
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
//
// centralHook attaches ALL of these subscribers to one central adopted at
// runtime and returns a single composite unwire. Every one of them subscribes
// by walking the registry exactly once, so without it an adopted CCU emitted
// no device.trigger / device lifecycle / optimistic-rollback / system-status
// frame on any plane — WebSocket, REST buffer or MQTT — until the daemon was
// restarted and the central became a boot-time one. It is deliberately ONE
// hook rather than one per subscriber: the previous shape covered exactly the
// hub-events subscriber and silently forgot the other five.
func wireSystemStatusSubscribers(
	reg *central.Registry,
	wsHub *ws.Hub,
	mqttWiring *mqtt.Wiring,
	mqttSup *mqttSupervisor,
	alarmSvc *alarm.Service,
	alarmSink *alarmMQTTSink,
	securitySvc *security.Service,
	locale, publicURL string,
	logger *slog.Logger,
) (sysStatusBuf *handlers.SystemStatusBuffer, centralHook func(u *central.Unit) (unwire func()), teardown func()) {
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

	// The security.* broadcast subscriber rides the Security & Safety bus,
	// which is daemon-level like the alarm bus. It is wired independently
	// of MQTT and of the alarm engine: the domain reports hazards and
	// faults with or without either, and a WebSocket consumer that had to
	// poll GET /security would learn about a smoke alarm on its own
	// schedule rather than on the event's.
	var wsSecurity *ws.SecuritySubscriber
	if securitySvc != nil {
		wsSecurity = ws.NewSecuritySubscriber(securitySvc.Bus(), wsHub)
		wsSecurity.Start()
	}

	var mqttSysStatus *mqtt.SystemStatusPublisher
	if mqttWiring != nil {
		mqttSysStatus = mqtt.NewSystemStatusPublisher(reg, mqttWiring, logger)
		mqttSysStatus.Start() //nolint:contextcheck // Start has no ctx parameter; it subscribes to the event bus internally
	}

	// One hook fanning out over every registry-snapshotting subscriber above.
	centralHook = func(u *central.Unit) func() {
		return composeUnwires(sysStatusBuf.SubscribeCentral(u), wsSysStatus.StartCentral(u),
			wsHubEvents.StartCentral(u), wsDeviceLifecycle.StartCentral(u),
			wsDeviceTrigger.StartCentral(u), wsOptimisticRollback.StartCentral(u),
			mqttSysStatus.StartCentral(u))
	}

	// The MQTT alarm publisher mirrors the daemon-level alarm engine onto
	// the HA alarm_control_panel plane. Nil-safe: only wired when both the
	// alarm service and MQTT wiring are present. It owns the FAILED_TO_ARM
	// event, so the command sink's master-arm failure hook points at it.
	var mqttAlarm *mqtt.AlarmMQTTPublisher
	if mqttWiring != nil && alarmSvc != nil {
		mqttAlarm = mqtt.NewAlarmMQTTPublisher(alarmSvc, mqttWiring, logger)
		mqttAlarm.Start() //nolint:contextcheck // Start has no ctx parameter; it subscribes to the event bus internally
		if alarmSink != nil {
			alarmSink.setArmFailureHook(mqttAlarm.PublishFailedToArm)
		}
		if mqttSup != nil {
			// Re-seed the retained alarm plane after every broker
			// (re)connect — a broker restart wipes the retained store
			// and a quiescent alarm system would never repopulate it.
			mqttSup.OnConnect(func(context.Context) { mqttAlarm.OnBrokerConnect() })
		}
	}

	// The Security & Safety plane mirrors the domain onto its own
	// daemon-level tree and its own device card. Independent of the alarm
	// service: the domain reports hazards and faults with or without one.
	var mqttSecurity *mqtt.SecurityMQTTPublisher
	if mqttWiring != nil && securitySvc != nil {
		mqttSecurity = mqtt.NewSecurityMQTTPublisher(securitySvc, mqttWiring, locale, publicURL, logger)
		mqttSecurity.Start(securitySvc.Bus()) //nolint:contextcheck // Start subscribes to the event bus internally
		if mqttSup != nil {
			// A broker restart wipes the retained store, and a quiet
			// installation would never repopulate it on its own.
			//nolint:contextcheck // the reconcile path runs on the publisher lifetime, detached from the connect ctx by design
			mqttSup.OnConnect(func(context.Context) { mqttSecurity.OnBrokerConnect() })
		}
	}

	teardown = func() {
		// LIFO: mirror the order the original inline defers would have run.
		if mqttSecurity != nil {
			mqttSecurity.Stop()
		}
		if mqttAlarm != nil {
			mqttAlarm.Stop()
		}
		if mqttSysStatus != nil {
			mqttSysStatus.Stop()
		}
		if wsAlarm != nil {
			wsAlarm.Stop()
		}
		if wsSecurity != nil {
			wsSecurity.Stop()
		}
		wsOptimisticRollback.Stop()
		wsDeviceTrigger.Stop()
		wsDeviceLifecycle.Stop()
		wsHubEvents.Stop()
		wsSysStatus.Stop()
		stopSysStatusBuf()
	}

	return sysStatusBuf, centralHook, teardown
}

// composeUnwires folds several per-central unwires into one, skipping the nil
// entries a subscriber returns when it had nothing to attach. The composite is
// what the orchestrator stores, so removing the central drops the whole set.
func composeUnwires(unwires ...func()) func() {
	return func() {
		for _, unwire := range unwires {
			if unwire != nil {
				unwire()
			}
		}
	}
}
