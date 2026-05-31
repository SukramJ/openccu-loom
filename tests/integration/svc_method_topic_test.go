// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build integration

package integration

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// climateSlot helper — BWTH001 is a HmIP wall thermostat, so its
// service-method topics live under the `climate` CDP-kind segment.
// The TopicBuilder uses this slot to compose the canonical ADR-0011
// per-method command topic
// `<base>/<central>/<iface>/<addr>/<chan>/custom/<kind>/set/<method>`.
func climateSlot(addr string, ch int) payload.TopicSlot {
	return payload.TopicSlot{
		Address:   addr,
		Channel:   ch,
		Bucket:    payload.BucketCustom,
		Parameter: "climate",
	}
}

// TestServiceMethodTopicEndToEnd is the ADR 0009 follow-up integration
// test: an MQTT publish on
// `<base>/<central>/<iface>/<addr>/<chan>/svc/set_mode/set` with a
// scalar payload `"heat"` lands as
// `Source.Invoke(ctx, "set_mode", {"mode": "heat"}, …)` on the
// channel's custom DP. End-to-end pipeline:
//
//	MQTT topic → CommandSubscriber → CDPInvocationSink.InvokeChannelService
//	 → channel.CustomDataPoint → Source.Invoke
//
// The test uses an in-process noop publisher and a recording sink
// fake — no broker required. The only "real" piece is the
// CommandSubscriber's topic parsing + dispatch logic.
func TestServiceMethodTopicEndToEnd(t *testing.T) {
	t.Parallel()

	pub := newInprocessPubSub()
	topics := mqtt.NewTopicBuilder("gh")
	sink := &recordingSink{}
	cdpSink := &recordingCDPSink{}
	sub := mqtt.NewCommandSubscriber(pub, topics, sink, nil).WithCDPSink(cdpSink)

	if err := sub.Start(context.Background()); err != nil {
		t.Fatalf("subscriber start: %v", err)
	}

	// Publish a scalar "heat" on the climate channel's set_mode topic.
	topic := topics.CustomDPServiceMethod("ccu", "HmIP-RF", climateSlot("BWTH001", 1), "set_mode")
	if err := pub.Publish(context.Background(), topic, []byte("heat"), mqtt.QoS1, false); err != nil {
		t.Fatalf("publish: %v", err)
	}

	// Wait for dispatch.
	pub.WaitDispatch()

	if cdpSink.calls.Load() != 1 {
		t.Fatalf("expected 1 InvokeChannelService call, got %d", cdpSink.calls.Load())
	}
	if cdpSink.lastMethod != "set_mode" {
		t.Errorf("method = %q, want set_mode", cdpSink.lastMethod)
	}
	if cdpSink.lastDevice != "BWTH001" {
		t.Errorf("device = %q, want BWTH001", cdpSink.lastDevice)
	}
	if cdpSink.lastChannel != 1 {
		t.Errorf("channel = %d, want 1", cdpSink.lastChannel)
	}
	mode, _ := cdpSink.lastParams["mode"].(string)
	if mode != "heat" {
		t.Errorf("params[mode] = %q, want heat", mode)
	}
}

// TestServiceMethodTopicJSONPayload verifies that JSON-object payloads
// pass through `params` verbatim instead of being wrapped under the
// scalar arg key.
func TestServiceMethodTopicJSONPayload(t *testing.T) {
	t.Parallel()

	pub := newInprocessPubSub()
	topics := mqtt.NewTopicBuilder("gh")
	cdpSink := &recordingCDPSink{}
	sub := mqtt.NewCommandSubscriber(pub, topics, &recordingSink{}, nil).WithCDPSink(cdpSink)

	if err := sub.Start(context.Background()); err != nil {
		t.Fatalf("subscriber start: %v", err)
	}

	topic := topics.CustomDPServiceMethod("ccu", "HmIP-RF", climateSlot("BWTH001", 1), "set_away_for_duration")
	body := []byte(`{"hours": 4, "away_temperature": 17.0}`)
	if err := pub.Publish(context.Background(), topic, body, mqtt.QoS1, false); err != nil {
		t.Fatalf("publish: %v", err)
	}
	pub.WaitDispatch()

	hours, _ := cdpSink.lastParams["hours"].(float64)
	if hours != 4 {
		t.Errorf("params[hours] = %v, want 4", hours)
	}
	awayTemp, _ := cdpSink.lastParams["away_temperature"].(float64)
	if awayTemp != 17.0 {
		t.Errorf("params[away_temperature] = %v, want 17.0", awayTemp)
	}
}

// --- in-process pub/sub helper ---

// inprocessPubSub is a minimal mqtt.Publisher + mqtt.Subscriber that
// loops Publish calls back through registered Subscribe callbacks.
// Wildcards are expanded to a regex-free prefix-match good enough for
// the topics this test publishes.
type inprocessPubSub struct {
	subs       map[string]mqtt.MessageHandler
	dispatched chan struct{}
}

func newInprocessPubSub() *inprocessPubSub {
	return &inprocessPubSub{
		subs:       make(map[string]mqtt.MessageHandler),
		dispatched: make(chan struct{}, 32),
	}
}

func (p *inprocessPubSub) Publish(_ context.Context, topic string, payload []byte, _ mqtt.QoS, _ bool) error {
	for filter, cb := range p.subs {
		if matchesFilter(filter, topic) {
			cb(topic, payload, false)
			p.dispatched <- struct{}{}
		}
	}
	return nil
}

func (p *inprocessPubSub) Subscribe(_ context.Context, filter string, _ mqtt.QoS, cb mqtt.MessageHandler) error {
	p.subs[filter] = cb
	return nil
}

func (p *inprocessPubSub) Unsubscribe(_ context.Context, filter string) error {
	delete(p.subs, filter)
	return nil
}

func (p *inprocessPubSub) WaitDispatch() {
	select {
	case <-p.dispatched:
	default:
	}
}

// matchesFilter is a tiny MQTT wildcard matcher: `+` matches one
// segment, no `#` support (not needed here).
func matchesFilter(filter, topic string) bool {
	fParts := strings.Split(filter, "/")
	tParts := strings.Split(topic, "/")
	if len(fParts) != len(tParts) {
		return false
	}
	for i, f := range fParts {
		if f == "+" {
			continue
		}
		if f != tParts[i] {
			return false
		}
	}
	return true
}

// --- recording sinks ---

type recordingSink struct{}

func (recordingSink) SetValue(context.Context, string, string, string, hmenum.Parameter, any, hmenum.CommandPriority) error {
	return errors.New("not used in service-method test")
}
func (recordingSink) SetSysvar(context.Context, string, string, any) error { return nil }
func (recordingSink) TriggerProgram(context.Context, string, string) error { return nil }

type recordingCDPSink struct {
	calls       atomic.Int32
	lastCentral string
	lastDevice  string
	lastMethod  string
	lastChannel int
	lastParams  map[string]any
	lastPrio    hmenum.CommandPriority
}

func (r *recordingCDPSink) InvokeCustomDP(_ context.Context, _, _, _, _ string, _ map[string]any, _ hmenum.CommandPriority) error {
	return nil
}

func (r *recordingCDPSink) InvokeChannelService(_ context.Context, central, _, device string, channel int, method string, params map[string]any, prio hmenum.CommandPriority) error {
	r.calls.Add(1)
	r.lastCentral = central
	r.lastDevice = device
	r.lastMethod = method
	r.lastChannel = channel
	r.lastParams = params
	r.lastPrio = prio
	return nil
}
