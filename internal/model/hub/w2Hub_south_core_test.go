// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hub

import (
	"context"
	"reflect"
	"testing"
	"time"
)

// ─── model-hub-c010: one count accessor, no HA-template shapes ──────────

// w2HubBannedAggregateMethods names the accessors that must not exist on the
// message aggregates.
//
//   - Counter duplicated Count byte for byte (same RLock, same
//     len(a.messages)) and carried a claim about what "HA's MQTT
//     statistics-template expects". The published alarm-messages state topic
//     is a JSON list and the discovery body derives the count with
//     `{{ value_json | length }}`, so no template ever read a count from the
//     model. Two names for one datum is the drift shape: a change to the live
//     Count leaves the second copy behind with nothing to notice.
//   - AdditionalInformationIndexed returned an "alarm_1"/"message_1" indexed
//     dict for a json_attributes_template that does not exist — the published
//     attributes template is `{"messages": {{ value_json | tojson }} }`, the
//     raw list. Its doc also promised the dict "follows the same order as
//     List", which a Go map cannot carry.
var w2HubBannedAggregateMethods = []string{"Counter", "AdditionalInformationIndexed"}

// TestW2HubMessageAggregatesExposeNoHATemplateShapes pins that neither message
// aggregate re-declares the message count under a second name, and that
// neither carries an HA presentation shape the daemon never publishes.
func TestW2HubMessageAggregatesExposeNoHATemplateShapes(t *testing.T) {
	types := []reflect.Type{
		reflect.TypeOf(&AlarmMessages{}),
		reflect.TypeOf(&ServiceMessages{}),
	}
	for _, rt := range types {
		for _, banned := range w2HubBannedAggregateMethods {
			if _, ok := rt.MethodByName(banned); ok {
				t.Errorf("%s must not expose %s: the message count has one accessor (Count), "+
					"and the daemon publishes the raw message list — no indexed-dict or second "+
					"counter shape reaches any topic", rt.String(), banned)
			}
		}
	}
	// The live accessor must still be there, so the check above cannot pass
	// by the aggregate having lost its count entirely.
	for _, rt := range types {
		if _, ok := rt.MethodByName("Count"); !ok {
			t.Errorf("%s must expose Count", rt.String())
		}
	}
}

// ─── model-hub-c016: the model does not clamp the pairing window ────────

// w2HubInstallModeWriterSpy records what the model hands the writer.
type w2HubInstallModeWriterSpy struct {
	enabled  bool
	duration time.Duration
	calls    int
}

func (w *w2HubInstallModeWriterSpy) SetInstallMode(_ context.Context, _ string, enabled bool, duration time.Duration) error {
	w.enabled = enabled
	w.duration = duration
	w.calls++
	return nil
}

// TestW2HubInstallModePressSendsAnExplicitDuration pins that the pairing
// window this daemon opens is always a duration it put on the wire itself.
// The firmware defaults differ per surface — rfd's XML-RPC setInstallMode
// substitutes 60 s when the caller omits `seconds`
// (../OpenCCU-Base/src/rfd/XmlRpcMethods.cpp:608-609) — so a Press() that
// stopped passing a duration would silently inherit whichever default the
// contacted surface happens to carry.
func TestW2HubInstallModePressSendsAnExplicitDuration(t *testing.T) {
	spy := &w2HubInstallModeWriterSpy{}
	m := NewInstallMode("BidCos-RF", spy)
	if err := m.Press(context.Background()); err != nil {
		t.Fatalf("Press: %v", err)
	}
	if spy.calls != 1 {
		t.Fatalf("writer calls = %d, want 1", spy.calls)
	}
	if !spy.enabled {
		t.Error("Press must enable install mode")
	}
	if spy.duration != DefaultInstallModeDuration {
		t.Errorf("Press sent duration %v, want %v — the daemon must never fall back "+
			"to a firmware default, which is 60 s on rfd's XML-RPC setInstallMode and "+
			"is not the same number on every surface", spy.duration, DefaultInstallModeDuration)
	}
}

// TestW2HubInstallModeForwardsTheRequestedDurationUnclamped pins the half of
// the InstallModeWriter contract that says the model does not clamp: the
// duration reaches the writer exactly as the caller asked. The ceiling is the
// CCU's and it is transport-dependent — rfd truncates to
// INSTALL_MODE_MAX_TIME = 600 s (../OpenCCU-Base/src/rfd/RFManager.h:562,
// applied at RFManager.cpp:582-583 and :601-602), the HmIP legacy path does
// not bound at all — so a clamp introduced here would be wrong for at least
// one transport.
func TestW2HubInstallModeForwardsTheRequestedDurationUnclamped(t *testing.T) {
	for _, want := range []time.Duration{time.Second, 600 * time.Second, 3600 * time.Second} {
		spy := &w2HubInstallModeWriterSpy{}
		m := NewInstallMode("HmIP-RF", spy)
		if err := m.Enable(context.Background(), want); err != nil {
			t.Fatalf("Enable(%v): %v", want, err)
		}
		if spy.duration != want {
			t.Errorf("Enable(%v) sent %v to the writer — the model must forward the "+
				"requested window verbatim; the ceiling belongs to the CCU and differs "+
				"per transport", want, spy.duration)
		}
	}
}

// ─── model-hub-c013: the model's metric name is a debug identifier ──────

// TestW2HubMetricSensorNameFeedsOnlyTheDebugSignature pins the one consumer
// MetricSensorName actually has. The operator-visible name of these three
// metrics comes from the i18n catalogues via the MQTT discovery builder, not
// from here; MetricSensorName is a stable, locale-independent identifier and
// Signature() is what reads it.
func TestW2HubMetricSensorNameFeedsOnlyTheDebugSignature(t *testing.T) {
	m := NewMetrics()
	for _, kind := range []MetricKind{MetricSystemHealth, MetricConnectionLatMs, MetricLastEventAgeSecs} {
		s := NewMetricHubSensor("ccu-01", kind, m)
		want := "HUB_SENSOR/" + MetricSensorName(kind)
		if got := s.Signature(); got != want {
			t.Errorf("Signature() for %s = %q, want %q — MetricSensorName is the debug "+
				"identifier Signature is built from, not a display name", kind, got, want)
		}
	}
}
