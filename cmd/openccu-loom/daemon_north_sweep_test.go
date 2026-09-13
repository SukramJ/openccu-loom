// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"log/slog"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
)

// northSweepTestConfig is a minimal enabled-with-a-broker north config. The
// broker address is unroutable on purpose: every assertion in this file is
// about what the composition root BUILDS, and nothing here may depend on a
// broker being there.
func northSweepTestConfig() *config.Config {
	cfg := &config.Config{}
	cfg.North.MQTT.Enabled = true
	cfg.North.MQTT.BrokerURL = "tcp://127.0.0.1:1"
	cfg.North.MQTT.TopicBase = "openccu-loom"
	cfg.North.MQTT.ClientID = "loom"
	return cfg
}

// TestTheBridgeRoutesItsSweepsThroughTheSweepConnection is the assertion
// TestSweepsDoNotRideTheCommandClient was missing.
//
// That test proves a dedicated sweep connection is BUILT and that it is a
// different object from the command plane's client. It never asks whether the
// bridge uses it — and the bridge is the only thing that decides. Both steps
// between the two are invisible from it: the composition root can resolve the
// sweep subscriber to the command client, and it can simply not hand the
// bridge a sweep subscriber at all. Either one puts `<base>/#` back on the
// command connection for the length of every sweep window, where a broker
// delivers one copy of every inbound command per matching subscription and
// go-mqtt re-matches each copy against the whole local filter list: every
// command handler runs twice. A doubled `PRESS_SHORT`, a doubled program
// trigger, a doubled alarm arm, none of it in any log. That is the defect
// #812 exists to fix, measured against Mosquitto.
//
// The effect itself — one handler call per inbound command while a window is
// open — is pinned in-package by TestSweepsDoNotDoubleInboundCommands against
// a broker double that reproduces both multiplications. What this adds is
// that the bridge this daemon really assembles is wired the way that test
// wires its own.
func TestTheBridgeRoutesItsSweepsThroughTheSweepConnection(t *testing.T) {
	t.Parallel()
	cfg := northSweepTestConfig()

	stack := buildMQTT(cfg, slog.Default(), nil, nil, func() []string { return nil })
	if stack == nil {
		t.Fatal("buildMQTT returned nil with MQTT enabled")
	}
	if stack.sweep == nil {
		t.Fatal("no dedicated sweep connection was built")
	}
	bridge := stack.wiring.Bridge()
	if bridge == nil {
		t.Fatal("the stack carries no bridge")
	}

	got := bridge.SweepSubscriber()
	if got == mqtt.Subscriber(stack.client) {
		t.Fatal("the bridge resolves its retained-store sweeps to the command plane's client: " +
			"`<base>/#` is installed on the connection the command subscriber holds, so for the " +
			"length of every sweep window the broker delivers every inbound command twice and " +
			"every handler runs twice — a doubled button press, a doubled program trigger, a " +
			"doubled alarm arm, with nothing in any log")
	}
	if got != mqtt.Subscriber(stack.sweep) {
		t.Fatalf("the bridge sweeps on %T, not on the dedicated sweep connection the daemon "+
			"built for exactly that purpose", got)
	}
}

// TestTheSweepConnectionCarriesNoLastWill pins the contract stated in
// capitals on [mqtt.NewSweepSubscriber]: the sweep connection must not carry
// a will.
//
// It was a comment and nothing else. The will belongs to CONNECT, so it is
// set where the client is built, and the only test reading a configured will
// reads [northTCPConfig] — the MAIN connection's, where a will is not only
// allowed but required. Adding one to the sweep connection therefore changed
// nothing any test could see.
//
// IT READS THE ASSEMBLED CONFIG, NOT sweepTCPConfig'S RETURN. Calling the
// builder directly was correct only for as long as [buildMQTT] used that
// return verbatim: one `sweepTCP.Will = …` line inside buildMQTT restored the
// whole hazard below with this test still green, which is the same one-step
// gap its sibling TestTheBridgeRoutesItsSweepsThroughTheSweepConnection was
// written to close on the routing half. So it reads
// mqttStack.sweepTCP — the struct the sweep client is really constructed
// from — and compares it against the daemon's main CONNECT.
//
// What it would change in production: the sweep connection comes and goes
// with every window and is dropped outright when an UNSUBSCRIBE fails. A
// graceful Close discards a will, which is why normal operation would never
// show this — but a dropped socket or a broker kick would publish a retained
// `offline` to `<base>/bridge/status`, the topic every entity of every CCU
// lists as its availability source. The whole fleet greys out while the
// daemon runs on and keeps publishing, and nothing anywhere reports it.
func TestTheSweepConnectionCarriesNoLastWill(t *testing.T) {
	t.Parallel()
	cfg := northSweepTestConfig()
	logger := slog.Default()

	main := northTCPConfig(cfg, logger)
	if main.Will == nil {
		t.Fatal("the main connection carries no will, so this test cannot tell an absent will " +
			"from an absent policy")
	}

	stack := buildMQTT(cfg, logger, nil, nil, func() []string { return nil })
	if stack == nil {
		t.Fatal("buildMQTT returned nil with MQTT enabled")
	}
	if stack.sweep == nil {
		t.Fatal("no dedicated sweep connection was built, so there is no CONNECT to read")
	}
	sweep := stack.sweepTCP
	if sweep.BrokerURL == "" {
		t.Fatal("the assembled sweep CONNECT is zero-valued: buildMQTT no longer records the " +
			"config its sweep client is constructed from, so this test reads nothing")
	}
	if sweep.Will != nil {
		t.Errorf("the sweep connection carries a will on %q: it is torn down on every window and "+
			"dropped on a failed UNSUBSCRIBE, so an ungraceful drop publishes that payload and "+
			"every entity of every CCU goes unavailable while the daemon runs on",
			sweep.Will.Topic)
	}
	if sweep.ClientID == main.ClientID {
		t.Errorf("the sweep connection reuses the command plane's client id %q — MQTT allows one "+
			"session per identifier (MQTT-3.1.4-2), so the two take each other over in a loop",
			sweep.ClientID)
	}
	if sweep.BrokerURL != main.BrokerURL {
		t.Errorf("the sweep connection dials %q, the daemon's broker is %q", sweep.BrokerURL, main.BrokerURL)
	}
}

// countingSweepClient is a sweep connection that records whether it was
// disconnected, so a teardown can be told from a connection left standing.
type countingSweepClient struct {
	mu          sync.Mutex
	connects    int
	disconnects int
}

func (c *countingSweepClient) Connect(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.connects++
	return nil
}

func (c *countingSweepClient) Disconnect(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.disconnects++
	return nil
}

func (c *countingSweepClient) counts() (connects, disconnects int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connects, c.disconnects
}

func (c *countingSweepClient) Publish(context.Context, string, []byte, mqtt.QoS, bool, ...mqtt.PublishOption) error {
	return nil
}

func (c *countingSweepClient) Subscribe(
	context.Context, string, mqtt.QoS, mqtt.MessageHandler, ...mqtt.SubscribeOption,
) (mqtt.SubscribeResult, error) {
	return mqtt.SubscribeResult{}, nil
}

func (c *countingSweepClient) Unsubscribe(context.Context, string) error { return nil }

// TestTeardownClosesTheSweepConnection pins the one line of the swap teardown
// that the lifecycle does not cover for it.
//
// Every other part of a generation is reachable from `sw.lifecycle`, which
// disconnects the socket the bridge publishes on. The sweep connection is its
// own socket and its own session, so a generation retired without closing it
// leaves a second broker session standing for the rest of the process — and,
// if that generation's last sweep failed to UNSUBSCRIBE, a broad `<base>/#`
// standing with it, overlapping the NEW generation's command filters. Config
// reloads are the path that produces those generations, and each one adds a
// session.
func TestTeardownClosesTheSweepConnection(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	sc := &countingSweepClient{}
	sweep := mqtt.NewSweepSubscriber(func() (mqtt.Client, mqtt.Connector) { return sc, sc }, slog.Default())
	// A sweep window: nothing to close until the connection exists.
	if _, err := sweep.Subscribe(ctx, "openccu-loom/#", mqtt.QoS0, func(*mqtt.Message) {}); err != nil {
		t.Fatalf("open the sweep window: %v", err)
	}
	if connects, _ := sc.counts(); connects != 1 {
		t.Fatalf("the sweep connected %d times, want 1 — the fixture has no live connection to close", connects)
	}

	sup := &mqttSupervisor{logger: slog.Default()}
	sup.teardown(ctx, &mqttSwap{sweep: sweep})

	if _, disconnects := sc.counts(); disconnects != 1 {
		t.Fatal("the retired generation's sweep connection was never disconnected: its session " +
			"stays open against the broker for the rest of the process, and a stranded " +
			"`<base>/#` on it keeps overlapping the new generation's command filters")
	}
}
