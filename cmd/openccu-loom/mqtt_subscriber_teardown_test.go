// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	clientpkg "github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
)

// buildTestMQTTSubscribers wires the daemon's subscriber builder against a
// no-op client and returns the teardown the supervisor would call, plus the
// backend the command path writes through.
func buildTestMQTTSubscribers(t *testing.T, client mqtt.Client) (teardown func(), ops *recordingBackendOps, noop *mqtt.NoopClient, err error) {
	t.Helper()
	const centralName = "ccu-test"
	ctx := context.Background()

	unit, uErr := central.New(central.Config{Name: centralName})
	if uErr != nil {
		t.Fatalf("central.New: %v", uErr)
	}
	reg := central.NewRegistry()
	if rErr := reg.Register(unit); rErr != nil {
		t.Fatalf("registry.Register: %v", rErr)
	}
	ops = &recordingBackendOps{testBackendOps: &testBackendOps{}}
	writer := clientpkg.NewValueWriter()
	writer.Register(centralName, "HmIP-RF", ops)

	build := makeMQTTSubscriberBuilder(ctx, reg, writer, nil, nil, nil, nil, nil, supervisorLogger())
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{
		Base: "openccu-loom", CentralName: centralName, RawEnabled: true,
	}, client)
	teardown, err = build(ctx, client, bridge)
	noop, _ = client.(*mqtt.NoopClient)
	return teardown, ops, noop, err
}

// TestMQTTSubscriberTeardownStopsTheCommandDispatcher pins that the teardown
// the supervisor stores for a stack generation really retires that
// generation's subscribers.
//
// The birth-sync and command subscribers each own dispatcher worker goroutines
// started in their constructor and stopped only by Close. A teardown that only
// drops the add-on subscription leaves them running for the process lifetime,
// so every MQTT reload adds another generation of workers — and a command
// delivered on a retired generation still executes against the CCU.
func TestMQTTSubscriberTeardownStopsTheCommandDispatcher(t *testing.T) {
	t.Parallel()
	teardown, ops, noop, err := buildTestMQTTSubscribers(t, mqtt.NewNoopClient())
	if err != nil {
		t.Fatalf("subscriber builder: %v", err)
	}

	const (
		filter = "openccu-loom/+/+/+/+/+/+/set"
		topic  = "openccu-loom/ccu-test/HmIP-RF/0001ABCD/4/values/STATE/set"
	)
	if !noop.DeliverInbound(filter, topic, []byte("true")) {
		t.Fatal("the daemon does not subscribe to its own declared command topic")
	}
	waitForBackendCalls(t, ops, 1)

	teardown()

	// The subscription itself is torn down with the client on a real swap, so
	// a late delivery on the retired generation must find a closed dispatcher
	// rather than a live worker pool.
	noop.DeliverInbound(filter, topic, []byte("false"))
	time.Sleep(50 * time.Millisecond)
	if got := ops.calls(); got != 1 {
		t.Fatalf("the retired command subscriber executed %d writes, want 1 (its dispatcher was never closed)", got)
	}
}

// rejectingSubscribeClient is a no-op client whose command-topic subscribes
// fail, the way a broker ACL that denies the `…/set` wildcard does.
type rejectingSubscribeClient struct {
	*mqtt.NoopClient
}

func (c *rejectingSubscribeClient) Subscribe(ctx context.Context, filter string, qos mqtt.QoS,
	handler mqtt.MessageHandler, opts ...mqtt.SubscribeOption,
) (mqtt.SubscribeResult, error) {
	if len(filter) >= 4 && filter[len(filter)-4:] == "/set" {
		return mqtt.SubscribeResult{}, errors.New("not authorized")
	}
	return c.NoopClient.Subscribe(ctx, filter, qos, handler, opts...)
}

// TestMQTTSubscriberBuildFailureStopsTheDispatchersItStarted pins the error
// path: a build that fails half-way still owns the worker goroutines its
// constructors started, and it is the only code that can still reach them —
// the supervisor treats the error as non-fatal and records no teardown.
func TestMQTTSubscriberBuildFailureStopsTheDispatchersItStarted(t *testing.T) {
	baseline := goroutineCount()

	client := &rejectingSubscribeClient{NoopClient: mqtt.NewNoopClient()}
	teardown, _, _, err := buildTestMQTTSubscribers(t, client)
	if err == nil {
		if teardown != nil {
			teardown()
		}
		t.Fatal("expected the command subscriber build to fail against a broker that rejects the filter")
	}

	// 1 birth-sync worker + 8 command workers must be gone again.
	deadline := time.Now().Add(3 * time.Second)
	for goroutineCount() > baseline+2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := goroutineCount(); got > baseline+2 {
		t.Fatalf("goroutines after a failed subscriber build: %d, baseline %d — the dispatchers of the "+
			"half-built generation are still running and nothing holds a handle to stop them", got, baseline)
	}
}

// goroutineCount reads the process-wide goroutine count after giving the
// runtime a moment to reap finished ones.
func goroutineCount() int {
	runtime.Gosched()
	return runtime.NumGoroutine()
}

// waitForBackendCalls blocks until the command dispatcher has run want writes
// through the backend. Commands are dispatched on a worker goroutine, so the
// assertion cannot read the recorder in the next statement.
func waitForBackendCalls(t *testing.T, ops *recordingBackendOps, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for ops.calls() < want && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if got := ops.calls(); got < want {
		t.Fatalf("backend writes = %d, want %d", got, want)
	}
}

// compile-time proof that the rejecting client is still a full mqtt.Client.
var _ mqtt.Client = (*rejectingSubscribeClient)(nil)
