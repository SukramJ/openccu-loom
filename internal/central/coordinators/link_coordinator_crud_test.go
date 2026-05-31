// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// link_coordinator_crud_test.go covers LinkCoordinator CRUD operations:
// AddLink, RemoveLink, GetLinks, SetLinkInfo, GetLinkInfo, and the
// deviceAddressOf helper.
package coordinators

import (
	"context"
	"errors"
	"testing"
)

// sentinel error to simulate wire-level failures.
var errFakeLinkFail = errors.New("fake link error")

// ---------------------------------------------------------------------------
// AddLink — CRUD
// ---------------------------------------------------------------------------

func TestLinkParity_AddLink_DelegatesAndReturnsNoError(t *testing.T) {
	t.Parallel()
	client := &fakeLinkClient{}
	coord := NewLinkCoordinator(func(addr string) (LinkClient, bool) {
		if addr == "VCU0000001" {
			return client, true
		}
		return nil, false
	})

	err := coord.AddLink(context.Background(), "VCU0000001:1", "VCU0000002:1", "My Link", "test desc")
	if err != nil {
		t.Fatalf("AddLink error: %v", err)
	}
	if n := client.addCalls.Load(); n != 1 {
		t.Fatalf("AddLink delegation call count = %d, want 1", n)
	}
}

func TestLinkParity_AddLink_DefaultNameGenerated(t *testing.T) {
	t.Parallel()
	var capturedName string
	coord := NewLinkCoordinator(func(string) (LinkClient, bool) {
		return &captureNameLinkClient{nameCapture: &capturedName}, true
	})

	err := coord.AddLink(context.Background(), "VCU0000001:1", "VCU0000002:1", "", "created by HA")
	if err != nil {
		t.Fatalf("AddLink error: %v", err)
	}
	if capturedName != "VCU0000001:1 -> VCU0000002:1" {
		t.Fatalf("default name = %q, want %q", capturedName, "VCU0000001:1 -> VCU0000002:1")
	}
}

func TestLinkParity_AddLink_UnknownDeviceReturnsError(t *testing.T) {
	t.Parallel()
	coord := NewLinkCoordinator(func(string) (LinkClient, bool) { return nil, false })
	err := coord.AddLink(context.Background(), "UNKNOWN:1", "VCU0000002:1", "", "")
	if !errors.Is(err, ErrLinkClientMissing) {
		t.Fatalf("err = %v, want ErrLinkClientMissing", err)
	}
}

func TestLinkParity_AddLink_ClientErrorPropagates(t *testing.T) {
	t.Parallel()
	client := &fakeLinkClient{err: errFakeLinkFail}
	coord := NewLinkCoordinator(func(string) (LinkClient, bool) { return client, true })
	err := coord.AddLink(context.Background(), "VCU0000001:1", "VCU0000002:1", "", "")
	if !errors.Is(err, errFakeLinkFail) {
		t.Fatalf("err = %v, want errFakeLinkFail", err)
	}
}

// ---------------------------------------------------------------------------
// RemoveLink — CRUD
// ---------------------------------------------------------------------------

func TestLinkParity_RemoveLink_DelegatesAndReturnsNoError(t *testing.T) {
	t.Parallel()
	client := &fakeLinkClient{}
	coord := NewLinkCoordinator(func(addr string) (LinkClient, bool) {
		if addr == "VCU0000001" {
			return client, true
		}
		return nil, false
	})

	err := coord.RemoveLink(context.Background(), "VCU0000001:1", "VCU0000002:1")
	if err != nil {
		t.Fatalf("RemoveLink error: %v", err)
	}
	if n := client.removeCalls.Load(); n != 1 {
		t.Fatalf("RemoveLink delegation call count = %d, want 1", n)
	}
}

func TestLinkParity_RemoveLink_UnknownDeviceReturnsError(t *testing.T) {
	t.Parallel()
	coord := NewLinkCoordinator(func(string) (LinkClient, bool) { return nil, false })
	err := coord.RemoveLink(context.Background(), "UNKNOWN:1", "VCU0000002:1")
	if !errors.Is(err, ErrLinkClientMissing) {
		t.Fatalf("err = %v, want ErrLinkClientMissing", err)
	}
}

// ---------------------------------------------------------------------------
// GetLinks — sorting + deduplication semantics
// ---------------------------------------------------------------------------

func TestLinkParity_GetLinks_ReturnsSortedLinks(t *testing.T) {
	t.Parallel()
	client := &fakeLinkClient{links: []DeviceLink{
		{SenderAddress: "VCU0000001:2", ReceiverAddress: "VCU0000002:1"},
		{SenderAddress: "VCU0000001:1", ReceiverAddress: "VCU0000002:1"},
		{SenderAddress: "VCU0000001:1", ReceiverAddress: "VCU0000002:0"},
	}}
	coord := NewLinkCoordinator(func(string) (LinkClient, bool) { return client, true })

	links, err := coord.GetLinks(context.Background(), "VCU0000001")
	if err != nil {
		t.Fatalf("GetLinks error: %v", err)
	}
	if len(links) != 3 {
		t.Fatalf("len = %d, want 3", len(links))
	}
	// After sort: (VCU0000001:1, VCU0000002:0), (VCU0000001:1, VCU0000002:1), (VCU0000001:2, VCU0000002:1)
	if links[0].SenderAddress != "VCU0000001:1" || links[0].ReceiverAddress != "VCU0000002:0" {
		t.Fatalf("first link = %+v; want (VCU0000001:1, VCU0000002:0)", links[0])
	}
	if links[2].SenderAddress != "VCU0000001:2" {
		t.Fatalf("third link sender = %q; want VCU0000001:2", links[2].SenderAddress)
	}
}

func TestLinkParity_GetLinks_ClientErrorPropagates(t *testing.T) {
	t.Parallel()
	client := &fakeLinkClient{err: errFakeLinkFail}
	coord := NewLinkCoordinator(func(string) (LinkClient, bool) { return client, true })
	_, err := coord.GetLinks(context.Background(), "VCU0000001")
	if !errors.Is(err, errFakeLinkFail) {
		t.Fatalf("err = %v, want errFakeLinkFail", err)
	}
}

// ---------------------------------------------------------------------------
// SetLinkInfo / GetLinkInfo
// ---------------------------------------------------------------------------

func TestLinkParity_SetLinkInfo_DelegatesAndReturnsNoError(t *testing.T) {
	t.Parallel()
	client := &fakeLinkClient{}
	coord := NewLinkCoordinator(func(string) (LinkClient, bool) { return client, true })

	err := coord.SetLinkInfo(context.Background(), "VCU0000001:1", "VCU0000002:1", "Updated Name", "updated desc")
	if err != nil {
		t.Fatalf("SetLinkInfo error: %v", err)
	}
}

func TestLinkParity_GetLinkInfo_ReturnsStoredLink(t *testing.T) {
	t.Parallel()
	expected := DeviceLink{
		SenderAddress:   "VCU0000001:1",
		ReceiverAddress: "VCU0000002:1",
		Name:            "My Link",
	}
	client := &fakeLinkClient{links: []DeviceLink{expected}}
	coord := NewLinkCoordinator(func(string) (LinkClient, bool) { return client, true })

	link, err := coord.GetLinkInfo(context.Background(), "VCU0000001:1", "VCU0000002:1")
	if err != nil {
		t.Fatalf("GetLinkInfo error: %v", err)
	}
	if link.SenderAddress != expected.SenderAddress {
		t.Fatalf("sender = %q, want %q", link.SenderAddress, expected.SenderAddress)
	}
	if link.Name != expected.Name {
		t.Fatalf("name = %q, want %q", link.Name, expected.Name)
	}
}

// ---------------------------------------------------------------------------
// SetResolver — hot-swap
// ---------------------------------------------------------------------------

func TestLinkParity_NilResolverIsHandled(t *testing.T) {
	t.Parallel()
	coord := NewLinkCoordinator(nil)
	err := coord.AddLink(context.Background(), "VCU0000001:1", "VCU0000002:1", "", "")
	if !errors.Is(err, ErrLinkClientMissing) {
		t.Fatalf("nil resolver must yield ErrLinkClientMissing, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// deviceAddressOf helper — extra parity cases
// ---------------------------------------------------------------------------

func TestLinkParity_DeviceAddressOf_MultipleSeparators(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want string
	}{
		{"VCU0000001:1", "VCU0000001"},
		{"VCU0000001", "VCU0000001"},
		{"a:b:c", "a:b"},
		{":1", ""},
	}
	for _, tc := range cases {
		got := deviceAddressOf(tc.in)
		if got != tc.want {
			t.Errorf("deviceAddressOf(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// captureNameLinkClient records the name passed to AddLink.
type captureNameLinkClient struct {
	nameCapture *string
}

func (c *captureNameLinkClient) AddLink(_ context.Context, _, _, name, _ string) error {
	*c.nameCapture = name
	return nil
}
func (c *captureNameLinkClient) RemoveLink(_ context.Context, _, _ string) error { return nil }
func (c *captureNameLinkClient) GetLinks(_ context.Context, _ string) ([]DeviceLink, error) {
	return nil, nil
}

func (c *captureNameLinkClient) GetLinkableChannels(_ context.Context, _ string) ([]LinkableChannel, error) {
	return nil, nil
}

func (c *captureNameLinkClient) SetLinkInfo(_ context.Context, _, _, _, _ string) error { return nil }

func (c *captureNameLinkClient) GetLinkInfo(_ context.Context, _, _ string) (DeviceLink, error) {
	return DeviceLink{}, nil
}
