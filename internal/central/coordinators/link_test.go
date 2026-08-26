// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package coordinators

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
)

type fakeLinkClient struct {
	addCalls    atomic.Int32
	removeCalls atomic.Int32
	getCalls    atomic.Int32
	links       []DeviceLink
	linkable    []LinkableChannel
	err         error
}

func (f *fakeLinkClient) AddLink(_ context.Context, _, _, _, _ string) error {
	f.addCalls.Add(1)
	return f.err
}

func (f *fakeLinkClient) RemoveLink(_ context.Context, _, _ string) error {
	f.removeCalls.Add(1)
	return f.err
}

func (f *fakeLinkClient) GetLinks(_ context.Context, _ string) ([]DeviceLink, error) {
	f.getCalls.Add(1)
	return f.links, f.err
}

func (f *fakeLinkClient) GetLinkableChannels(_ context.Context, _ string) ([]LinkableChannel, error) {
	return f.linkable, f.err
}

func (f *fakeLinkClient) SetLinkInfo(_ context.Context, _, _, _, _ string) error { return f.err }
func (f *fakeLinkClient) GetLinkInfo(_ context.Context, _, _ string) (DeviceLink, error) {
	if len(f.links) > 0 {
		return f.links[0], f.err
	}
	return DeviceLink{}, f.err
}

func TestLinkCoordinatorResolvesPerDevice(t *testing.T) {
	c := &fakeLinkClient{}
	coord := NewLinkCoordinator(func(addr string) (LinkClient, bool) {
		if addr == "0001ABCD" {
			return c, true
		}
		return nil, false
	})
	if err := coord.AddLink(context.Background(), "0001ABCD:1", "0002EFGH:2", "", ""); err != nil {
		t.Fatalf("add: %v", err)
	}
	if c.addCalls.Load() != 1 {
		t.Fatalf("add calls=%d", c.addCalls.Load())
	}
}

func TestLinkCoordinatorUnknownDevice(t *testing.T) {
	coord := NewLinkCoordinator(func(string) (LinkClient, bool) { return nil, false })
	err := coord.AddLink(context.Background(), "0001ABCD:1", "0002:2", "", "")
	if !errors.Is(err, ErrLinkClientMissing) {
		t.Fatalf("err=%v", err)
	}
}

func TestLinkCoordinatorGetLinksSorted(t *testing.T) {
	client := &fakeLinkClient{links: []DeviceLink{
		{SenderAddress: "0001:2", ReceiverAddress: "0002:1"},
		{SenderAddress: "0001:1", ReceiverAddress: "0002:1"},
		{SenderAddress: "0001:1", ReceiverAddress: "0002:0"},
	}}
	coord := NewLinkCoordinator(func(string) (LinkClient, bool) { return client, true })
	links, err := coord.GetLinks(context.Background(), "0001")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if links[0].SenderAddress != "0001:1" || links[0].ReceiverAddress != "0002:0" {
		t.Fatalf("first=%+v", links[0])
	}
}

func TestLinkCoordinatorSetResolverSwappable(t *testing.T) {
	coord := NewLinkCoordinator(nil)
	if err := coord.AddLink(context.Background(), "0001:1", "0002:1", "", ""); !errors.Is(err, ErrLinkClientMissing) {
		t.Fatalf("err=%v", err)
	}
	client := &fakeLinkClient{}
	coord.SetResolver(func(string) (LinkClient, bool) { return client, true })
	if err := coord.AddLink(context.Background(), "0001:1", "0002:1", "", ""); err != nil {
		t.Fatalf("add after swap: %v", err)
	}
}

func TestDeviceAddressOf(t *testing.T) {
	cases := map[string]string{
		"0001ABCD:1":  "0001ABCD",
		"0001ABCD":    "0001ABCD",
		":1":          "",
		"foo:bar:baz": "foo:bar",
	}
	for in, want := range cases {
		if got := deviceAddressOf(in); got != want {
			t.Fatalf("deviceAddressOf(%q) = %q, want %q", in, got, want)
		}
	}
}
