// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package occulited_test

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client/transport/occulited"
	"github.com/SukramJ/openccu-loom/tests/harness/litefake"
)

const (
	waitMsg       = 5 * time.Second
	switchChannel = "VCU0000321:1" // HM-LC-Sw1-Pl on BidCos-RF
	bidcosRF      = "BidCos-RF"
	hmipRF        = "HmIP-RF"
)

func startFake(t *testing.T, opts litefake.Options) *litefake.Fake {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	f, err := litefake.Start(ctx, opts)
	if err != nil {
		t.Fatalf("litefake.Start: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func newClient(t *testing.T, baseURL, token string) *occulited.Client {
	t.Helper()
	c, err := occulited.New(occulited.Config{BaseURL: baseURL, Token: token})
	if err != nil {
		t.Fatalf("occulited.New: %v", err)
	}
	return c
}

// fastBackoff keeps reconnects inside a test's patience.
func fastBackoff() occulited.Backoff {
	return occulited.Backoff{
		Initial: 20 * time.Millisecond, Max: 100 * time.Millisecond, Jitter: 0.01,
		Unauthorized: 50 * time.Millisecond, TooManyStreams: 50 * time.Millisecond, Starting: 50 * time.Millisecond,
	}
}

// nextMsg returns the next message that is not a comment or open.
func nextMsg(t *testing.T, s *occulited.Stream) occulited.Message {
	t.Helper()
	deadline := time.After(waitMsg)
	for {
		select {
		case m, ok := <-s.Messages():
			if !ok {
				t.Fatal("stream closed")
			}
			if m.Kind == occulited.KindComment || m.Kind == occulited.KindOpen {
				continue
			}
			return m
		case <-deadline:
			t.Fatal("no message in time")
		}
	}
}

// waitKind skips messages until one of kind arrives.
func waitKind(t *testing.T, s *occulited.Stream, kind occulited.Kind) occulited.Message {
	t.Helper()
	deadline := time.After(waitMsg)
	for {
		select {
		case m, ok := <-s.Messages():
			if !ok {
				t.Fatal("stream closed")
			}
			if m.Kind == kind {
				return m
			}
		case <-deadline:
			t.Fatalf("no %s message in time", kind)
		}
	}
}

func fireSwitch(t *testing.T, f *litefake.Fake, on bool) {
	t.Helper()
	if err := f.V().InterfaceRPC(bidcosRF).SimulateDeviceEvent(switchChannel, "STATE", on); err != nil {
		t.Fatalf("SimulateDeviceEvent: %v", err)
	}
}
