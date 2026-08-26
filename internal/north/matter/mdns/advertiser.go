// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mdns

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// Errors.
var (
	// ErrServiceNotFound is returned by Update / Withdraw when the
	// instance name was never published.
	ErrServiceNotFound = errors.New("mdns: service not found")
)

// Advertiser is the runtime surface a bridge component holds onto.
// Implementations vary in how they push records onto the wire — the
// production [MulticastAdvertiser] sends multicast UDP responses on
// 5353; tests use [Noop].
type Advertiser interface {
	// Publish announces (or re-announces) svc. Replaces an existing
	// record with the same InstanceName + ServiceType.
	Publish(ctx context.Context, svc Service) error
	// Withdraw retracts the record. Implementations send a
	// "goodbye" packet (TTL=0) before deleting their local copy
	// when the underlying transport supports it.
	Withdraw(ctx context.Context, instanceName, serviceType string) error
	// Active lists the currently published service records.
	Active() []Service
	// Close tears down the advertiser. Idempotent.
	Close() error
}

// Noop is an advertiser that just records published services in
// memory. Used by tests and by the daemon during the boot phase
// before the network stack is up.
type Noop struct {
	mu    sync.RWMutex
	items map[string]Service
}

// NewNoop returns an empty no-op advertiser.
func NewNoop() *Noop {
	return &Noop{items: make(map[string]Service)}
}

// Publish implements [Advertiser].
func (n *Noop) Publish(_ context.Context, svc Service) error {
	if err := svc.Validate(); err != nil {
		return err
	}
	n.mu.Lock()
	n.items[noopKey(svc.InstanceName, svc.ServiceType)] = svc
	n.mu.Unlock()
	return nil
}

// Withdraw implements [Advertiser].
func (n *Noop) Withdraw(_ context.Context, instanceName, serviceType string) error {
	key := noopKey(instanceName, serviceType)
	n.mu.Lock()
	defer n.mu.Unlock()
	if _, ok := n.items[key]; !ok {
		return fmt.Errorf("%w: %s/%s", ErrServiceNotFound, instanceName, serviceType)
	}
	delete(n.items, key)
	return nil
}

// Active implements [Advertiser].
func (n *Noop) Active() []Service {
	n.mu.RLock()
	defer n.mu.RUnlock()
	out := make([]Service, 0, len(n.items))
	for k := range n.items {
		out = append(out, n.items[k])
	}
	return out
}

// Close implements [Advertiser]. No-op for the no-op advertiser; the
// in-memory map drops out of scope with the receiver.
func (n *Noop) Close() error { return nil }

// noopKey builds the map key from instance + service type.
func noopKey(instance, serviceType string) string {
	return instance + "|" + serviceType
}
