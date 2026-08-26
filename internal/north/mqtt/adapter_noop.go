// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"sync"
)

// NoopClient is a no-dependency [Client] implementation that records
// every call. It is the default wired into the daemon when no MQTT
// broker URL is configured; tests and the `/mqtt` UI use it to
// exercise the bridge surface without a real broker.
type NoopClient struct {
	mu          sync.RWMutex
	published   []Publication
	subscribers map[string]MessageHandler
}

// Publication records a single Publish call.
type Publication struct {
	Topic   string
	Payload []byte
	QoS     QoS
	Retain  bool
}

// NewNoopClient constructs an empty client.
func NewNoopClient() *NoopClient {
	return &NoopClient{subscribers: make(map[string]MessageHandler)}
}

// Publish implements [Publisher].
func (c *NoopClient) Publish(_ context.Context, topic string, payload []byte, qos QoS, retain bool, _ ...PublishOption) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.published = append(c.published, Publication{Topic: topic, Payload: append([]byte(nil), payload...), QoS: qos, Retain: retain})
	return nil
}

// Subscribe implements [Subscriber].
func (c *NoopClient) Subscribe(_ context.Context, filter string, _ QoS, handler MessageHandler, _ ...SubscribeOption) (SubscribeResult, error) {
	c.mu.Lock()
	c.subscribers[filter] = handler
	c.mu.Unlock()
	return SubscribeResult{}, nil
}

// Unsubscribe implements [Subscriber].
func (c *NoopClient) Unsubscribe(_ context.Context, filter string) error {
	c.mu.Lock()
	delete(c.subscribers, filter)
	c.mu.Unlock()
	return nil
}

// SubscribedFilters returns the filter strings currently registered, in no
// particular order. Tests use it to read a subscriber's actual registered
// wildcard shapes off a real [NoopClient] it was started with, instead of
// restating them by hand.
func (c *NoopClient) SubscribedFilters() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]string, 0, len(c.subscribers))
	for f := range c.subscribers {
		out = append(out, f)
	}
	return out
}

// Published returns a copy of every recorded publication.
func (c *NoopClient) Published() []Publication {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]Publication, len(c.published))
	copy(out, c.published)
	return out
}

// DeliverInbound pushes a non-retained message to the subscriber
// registered for filter (exact match). Useful in tests that want to
// exercise the bridge's command-topic handling. For retained replay
// tests use [NoopClient.DeliverInboundRetained].
func (c *NoopClient) DeliverInbound(filter, topic string, payload []byte) bool {
	return c.deliverInbound(filter, topic, payload, false)
}

// DeliverInboundRetained is the retained-flag variant — tests use it
// to assert that command handlers drop replays from the broker.
func (c *NoopClient) DeliverInboundRetained(filter, topic string, payload []byte) bool {
	return c.deliverInbound(filter, topic, payload, true)
}

func (c *NoopClient) deliverInbound(filter, topic string, payload []byte, retained bool) bool {
	c.mu.RLock()
	handler, ok := c.subscribers[filter]
	c.mu.RUnlock()
	if !ok || handler == nil {
		return false
	}
	handler(&Message{Topic: topic, Payload: payload, Retain: retained})
	return true
}
