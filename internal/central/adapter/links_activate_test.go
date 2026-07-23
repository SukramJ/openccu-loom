// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

type activateCapturingBackend struct {
	fakeOperations
	receiver  string
	sender    string
	longPress bool
	called    bool
}

func (b *activateCapturingBackend) ActivateLinkParamset(_ context.Context, receiver, sender string, longPress bool) error {
	b.called = true
	b.receiver = receiver
	b.sender = sender
	b.longPress = longPress
	return nil
}

// TestLinksDomain_ActivateLink asserts the domain resolves via the RECEIVER
// device, forwards the args, and records a link-activate audit entry.
func TestLinksDomain_ActivateLink(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-al"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	dev := device.New(device.Config{
		InterfaceID: "HmIP-RF", Interface: hmenum.InterfaceHmIPRF,
		Address: "RCV", Model: "HmIP-PS",
	})
	dev.AddChannel("RCV:3", 3, "SWITCH", hmenum.ParamsetKeyValues)
	c.ModelRegistry.Put(dev)

	be := &activateCapturingBackend{fakeOperations: fakeOperations{kind: backends.KindCCU}}
	w := client.NewValueWriter()
	w.Register("ccu-al", "HmIP-RF", be)
	spy := &spyAudit{}
	d := NewLinksDomain(reg, w, nil).SetAuditRecorder(spy)

	if err := d.ActivateLink(context.Background(), "RCV:3", "SND:1", true); err != nil {
		t.Fatalf("ActivateLink: %v", err)
	}
	if !be.called || be.receiver != "RCV:3" || be.sender != "SND:1" || !be.longPress {
		t.Fatalf("backend not called with the right args: %+v", be)
	}
	if len(spy.entries) != 1 {
		t.Fatalf("expected 1 audit entry, got %d", len(spy.entries))
	}
	e := spy.entries[0]
	if e.Action != audit.ActionLinkActivate || e.Note != "long" || e.Peer != "SND:1" || e.DeviceAddress != "RCV" {
		t.Errorf("audit entry wrong: %+v", e)
	}
}

func TestLinksDomain_ActivateLink_UnknownReceiver(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-al2"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	d := NewLinksDomain(reg, client.NewValueWriter(), nil)
	if err := d.ActivateLink(context.Background(), "NOPE:1", "SND:1", false); err == nil {
		t.Error("expected an error for an unknown receiver device")
	}
}
