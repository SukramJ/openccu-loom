// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/SukramJ/openccu-loom/internal/wiring"

	"github.com/SukramJ/openccu-loom/internal/addonupdate"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/north/rest/ws"
)

// wireAddonUpdate constructs the CCU add-on self-updater (ADR 0057)
// when the platform capability check passes (add-on build AND an
// executable firmware installer) and returns nil otherwise. Every
// downstream surface (REST Deps.AddonUpdate, the WS broadcast, the
// MQTT `update` entity) treats a nil Updater as "not supported on this
// platform" rather than re-probing the capability itself.
//
// ctx is the daemon-lifetime context: it backs [addonupdate.Deps.Context],
// which [addonupdate.Updater.InstallAsync]'s detached download/verify/
// stage goroutine runs on, so an in-flight download is cancelled on
// daemon shutdown while the final installer hand-off (which
// deliberately ignores context cancellation — see
// [addonupdate.DefaultRunner]) is unaffected.
func wireAddonUpdate(ctx context.Context, logger *slog.Logger) *addonupdate.Updater {
	probe := addonupdate.NewCapabilityProbe()
	if !probe.Supported() {
		return nil
	}
	return addonupdate.NewUpdater(addonupdate.Deps{
		Capability: probe,
		Logger:     logger,
		Context:    ctx,
	})
}

// startAddonUpdatePeriodicCheck launches the boot-delayed, jittered
// recurring release check (ADR 0057 §4) and returns its stop func.
// A nil updater yields a no-op stopper.
func startAddonUpdatePeriodicCheck(ctx context.Context, updater *addonupdate.Updater, cfg *config.Config, logger *slog.Logger) func() {
	if updater == nil {
		return func() {}
	}
	// The toggle silences the background checking (boot-delayed and
	// recurring alike); the manual check/install verbs stay available.
	if !cfg.AddonUpdate.PeriodicCheckEnabled() {
		return func() {}
	}
	pc := &addonupdate.PeriodicChecker{
		Updater:  updater,
		Interval: cfg.AddonUpdate.CheckInterval,
		Logger:   logger,
	}
	pc.Start(ctx)
	return pc.Stop
}

// wireAddonUpdateWS registers the WS broadcast on every self-updater
// state transition — daemon-lifetime, independent of any MQTT broker
// (re)connect. Returns an unsubscribe func; a no-op when updater or
// hub is nil.
func wireAddonUpdateWS(m *wiring.Manifest, updater *addonupdate.Updater, hub *ws.Hub) (unsubscribe func()) {
	if updater == nil || hub == nil {
		return func() {}
	}
	// A plain listener registration on the updater, not an observer that
	// declares itself — which is why the exemption that used to cover
	// this function claimed a mechanism it does not have. Nothing else
	// carries the seam, so it is declared here.
	var unsub func()
	m.Attach(wiring.Seam{
		Name:         "ws.addon_update_status",
		Collaborator: "*ws.Hub, listening on addonupdate.Updater.OnChange",
		Phase:        wiring.PhaseOnce,
		Why:          "an add-on update's progress never reaches a WebSocket client. The SPA shows the state it had when the page loaded for the whole install, and because it arms its completion toast on the transition into installing, the operator is never told the update finished either",
	}, func() {
		unsub = updater.OnChange(func(st addonupdate.Status) {
			hub.PublishAddonUpdateStateChanged(addonUpdateWSPayload(st), time.Now())
		})
	})
	return unsub
}

// addonUpdateWSPayload converts the domain snapshot to the WS wire
// DTO — kept in lockstep with
// handlers.addonUpdateStatusResponse (internal/north/rest/handlers/addon_update.go).
func addonUpdateWSPayload(st addonupdate.Status) ws.AddonUpdateStatusPayload {
	payload := ws.AddonUpdateStatusPayload{
		Supported:       st.Supported,
		CurrentVersion:  st.CurrentVersion,
		LatestVersion:   st.LatestVersion,
		UpdateAvailable: st.UpdateAvailable,
		ReleaseURL:      st.ReleaseURL,
		State:           string(st.State),
		Error:           st.Error,
	}
	if !st.LastCheck.IsZero() {
		payload.LastCheck = st.LastCheck.UTC().Format(time.RFC3339)
	}
	return payload
}

// addonUpdateMQTTSink adapts *addonupdate.Updater onto
// mqtt.AddonUpdateSink (TriggerInstall vs. the domain's InstallAsync
// name). A concrete pointer type, not a bare interface value, so
// call sites can nil-check it before wiring — passing a typed-nil
// through an interface would make the command subscriber treat the
// add-on-update plane as wired. Mirrors alarmMQTTSink's shape.
type addonUpdateMQTTSink struct {
	u *addonupdate.Updater
}

// newAddonUpdateMQTTSink returns nil when u is nil, so callers can
// `if sink != nil { … }` exactly like [newAlarmMQTTSink].
func newAddonUpdateMQTTSink(u *addonupdate.Updater) *addonUpdateMQTTSink {
	if u == nil {
		return nil
	}
	return &addonUpdateMQTTSink{u: u}
}

func (s *addonUpdateMQTTSink) TriggerInstall(ctx context.Context) error {
	return s.u.InstallAsync(ctx)
}

// wsAddonUpdaterFrom narrows the updater onto the WS command contract.
// A nil updater (platform unsupported) yields a nil interface so the
// commands stay unregistered.
func wsAddonUpdaterFrom(u *addonupdate.Updater) ws.AddonUpdater {
	if u == nil {
		return nil
	}
	return u
}
