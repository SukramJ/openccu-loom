// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package coordinators

// NOTE: fakeLinkClient is declared in link_test.go (same package).
// Tests here reuse it without redeclaring.

import (
	"context"
	"errors"
	"testing"
)

func TestLinkGetLinkInfoReturnsFirstLink(t *testing.T) {
	t.Parallel()
	want := DeviceLink{SenderAddress: "S:1", ReceiverAddress: "R:2", Name: "my-link"}
	fake := &fakeLinkClient{links: []DeviceLink{want}}
	lc := NewLinkCoordinator(func(_ string) (LinkClient, bool) { return fake, true })

	got, err := lc.GetLinkInfo(context.Background(), "S:1", "R:2")
	if err != nil {
		t.Fatalf("GetLinkInfo error: %v", err)
	}
	if got != want {
		t.Fatalf("got %+v want %+v", got, want)
	}
}

func TestLinkGetLinkInfoPropagatesClientError(t *testing.T) {
	t.Parallel()
	boom := errors.New("rpc error")
	fake := &fakeLinkClient{err: boom}
	lc := NewLinkCoordinator(func(_ string) (LinkClient, bool) { return fake, true })

	_, err := lc.GetLinkInfo(context.Background(), "S:1", "R:2")
	if !errors.Is(err, boom) {
		t.Fatalf("expected boom wrapped in error, got %v", err)
	}
}

func TestLinkSetLinkInfoForwardsToClient(t *testing.T) {
	t.Parallel()
	fake := &fakeLinkClient{}
	lc := NewLinkCoordinator(func(_ string) (LinkClient, bool) { return fake, true })

	if err := lc.SetLinkInfo(context.Background(), "S:1", "R:2", "name", "desc"); err != nil {
		t.Fatalf("SetLinkInfo error: %v", err)
	}
}

func TestLinkGetLinkableChannelsForwardsToClient(t *testing.T) {
	t.Parallel()
	want := []LinkableChannel{{Address: "X:1", DeviceModel: "HM-CC"}}
	fake := &fakeLinkClient{linkable: want}
	lc := NewLinkCoordinator(func(_ string) (LinkClient, bool) { return fake, true })

	got, err := lc.GetLinkableChannels(context.Background(), "X")
	if err != nil {
		t.Fatalf("GetLinkableChannels error: %v", err)
	}
	if len(got) != 1 || got[0].Address != "X:1" {
		t.Fatalf("unexpected result %+v", got)
	}
}

func TestLinkRemoveLinkForwardsToClient(t *testing.T) {
	t.Parallel()
	fake := &fakeLinkClient{}
	lc := NewLinkCoordinator(func(_ string) (LinkClient, bool) { return fake, true })

	if err := lc.RemoveLink(context.Background(), "S:1", "R:2"); err != nil {
		t.Fatalf("RemoveLink error: %v", err)
	}
	if fake.removeCalls.Load() != 1 {
		t.Fatalf("removeCalls=%d want 1", fake.removeCalls.Load())
	}
}

func TestLinkResolveReturnsErrWhenResolverNilOrMissing(t *testing.T) {
	t.Parallel()
	// nil resolver → ErrLinkClientMissing
	lc := NewLinkCoordinator(nil)
	err := lc.RemoveLink(context.Background(), "S:1", "R:2")
	if !errors.Is(err, ErrLinkClientMissing) {
		t.Fatalf("nil resolver: expected ErrLinkClientMissing, got %v", err)
	}

	// Resolver returns (nil, false) → ErrLinkClientMissing.
	lc.SetResolver(func(_ string) (LinkClient, bool) { return nil, false })
	_, err = lc.GetLinks(context.Background(), "DEVICE")
	if !errors.Is(err, ErrLinkClientMissing) {
		t.Fatalf("false resolver: expected ErrLinkClientMissing, got %v", err)
	}
}

func TestLinkGetLinksCountsIncrements(t *testing.T) {
	t.Parallel()
	fake := &fakeLinkClient{links: []DeviceLink{
		{SenderAddress: "A:1", ReceiverAddress: "B:2"},
	}}
	lc := NewLinkCoordinator(func(_ string) (LinkClient, bool) { return fake, true })

	_, err := lc.GetLinks(context.Background(), "DEV")
	if err != nil {
		t.Fatalf("GetLinks error: %v", err)
	}
	if fake.getCalls.Load() != 1 {
		t.Fatalf("getCalls=%d want 1", fake.getCalls.Load())
	}
}
