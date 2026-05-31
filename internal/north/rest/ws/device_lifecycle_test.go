// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ws

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func TestDeviceLifecycleTopicFormat(t *testing.T) {
	t.Parallel()
	got := DeviceLifecycleTopic("0001ABCDE")
	if want := "device.0001ABCDE.lifecycle"; got != want {
		t.Fatalf("DeviceLifecycleTopic = %q, want %q", got, want)
	}
}

func TestDeviceLifecycleSubscriberNilSafe(t *testing.T) {
	t.Parallel()
	s := NewDeviceLifecycleSubscriber(nil, nil)
	s.Start()
	s.Stop()
}

func TestDeviceCreatedPayloadShape(t *testing.T) {
	t.Parallel()
	p := DeviceCreatedPayload{
		Central:       "home",
		InterfaceID:   "HmIP-RF",
		DeviceAddress: "0001ABCDE",
		Model:         "HmIP-eTRV-2",
		Source:        hmenum.SourceOfDeviceCreationCache,
	}
	if p.DeviceAddress != "0001ABCDE" || p.Model != "HmIP-eTRV-2" {
		t.Fatalf("payload round-trip failed: %+v", p)
	}
}

func TestDeviceRemovedPayloadShape(t *testing.T) {
	t.Parallel()
	p := DeviceRemovedPayload{
		Central:       "home",
		InterfaceID:   "HmIP-RF",
		DeviceAddress: "0001ABCDE",
	}
	if p.Central != "home" || p.DeviceAddress != "0001ABCDE" {
		t.Fatalf("payload round-trip failed: %+v", p)
	}
}
