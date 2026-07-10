// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build chiptool

package chiptool

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/tests/chiptool/harness"
)

// TestExposeSecondaryChannels_ValveFanOut is the positive counterpart to
// TestNegative_UnmappableDeviceClasses' irrigation-valve cases: with the
// north.matter.expose_secondary_channels expert flag ON, a valve custom
// entity's group-STATE transmitter (offset -1, ce_state) and its secondary
// actor channels (offsets +1/+2, ce_secondary) — folded away by default so the
// device is one endpoint — each surface as their own OnOff candidate AND
// materialise as bridged Matter endpoints. See
// docs/adr/0049-matter-one-endpoint-per-device.md.
//
// ELV-SH-WSM is not in [harness.DefaultDevices]; this test brings up its own
// isolated bridge with the flag on rather than the shared (flag-off) suite one.
func TestExposeSecondaryChannels_ValveFanOut(t *testing.T) {
	chipBin := harness.RequireChipTool(t)
	b := harness.Start(t, chipBin, harness.Options{
		CASEEnabled:             true,
		ExposeSecondaryChannels: true,
		Devices:                 []string{"ELV-SH-WSM"},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	const wsm = unmappableIrrigationAddr // VCU8976407

	// The four valve channels: primary (ch4) + the group-STATE transmitter
	// (ch3) + the two secondary actor channels (ch5/ch6). godevccu's ELV-SH-WSM
	// fixture pins these; the negative suite asserts only ch4 is exposed with
	// the flag off.
	wantChannels := []int{3, unmappableIrrigationPrimaryChannel, 5, 6}

	t.Run("candidates/all-valve-channels-exposed", func(t *testing.T) {
		var candidates struct {
			Items []negativeExposableRow `json:"items"`
		}
		if s := b.RESTGet(t, "/api/v1/matter/exposable", &candidates); s != 200 {
			t.Fatalf("GET /matter/exposable: status=%d", s)
		}
		got := map[int]bool{}
		for _, it := range candidates.Items {
			if it.DeviceAddress == wsm && it.DPKey == "STATE" && it.Mappable == "mappable" {
				got[it.ChannelNo] = true
			}
		}
		for _, ch := range wantChannels {
			if !got[ch] {
				t.Errorf("expose_secondary_channels on: WSM channel %d exposes no mappable STATE candidate — the flag must reveal the group-STATE (ch3) and secondary actor channels (ch5/ch6) alongside the primary (ch%d)", ch, unmappableIrrigationPrimaryChannel)
			}
		}
	})

	t.Run("endpoints/secondary-valve-channels-materialise", func(t *testing.T) {
		expCtx, expCancel := context.WithTimeout(ctx, 15*time.Second)
		if _, err := b.EnableAllExposures(expCtx); err != nil {
			t.Fatalf("enable exposures: %v", err)
		}
		expCancel()
		// Bridge reassembly is asynchronous; settle before commissioning against
		// a still-rebuilding topology (mirrors requireBridge's own bring-up).
		time.Sleep(1500 * time.Millisecond)

		ctl := harness.NewController(t, chipBin, 0x5101)
		out, err := ctl.PairFull(ctx, t, harness.PairTargetHost, b.MatterPort())
		if err != nil {
			t.Fatalf("commission: %v\n%s", err, out)
		}
		if !harness.PairingSuccess(out) {
			t.Fatalf("pairing did not report success:\n%s", out)
		}
		// [Bridge.ResolveCCUAddress] reads through b.SharedCtl; wire the
		// controller we just commissioned with.
		b.SharedCtl = ctl

		// Enumerate every bridged endpoint's ServerList in one wildcard read
		// (per-EP reads accumulate CASE sessions and time out on large fleets),
		// then count how many OnOff (0x0006) endpoints trace back to the WSM.
		slOut, err := ctl.ReadAttr(ctx, t, "descriptor", "server-list", 0xFFFF)
		if err != nil {
			t.Fatalf("wildcard server-list: %v", err)
		}
		perEP := harness.ServerListIDsPerEndpoint(slOut)
		wsmOnOff := 0
		for ep, clusters := range perEP {
			if !harness.HasCluster(clusters, 0x0006) {
				continue
			}
			addr, _, ok := b.ResolveCCUAddress(ctx, t, ep, 0x0006)
			if ok && strings.HasPrefix(addr, wsm+":") {
				wsmOnOff++
			}
		}
		// The default (flag off) projects exactly one WSM OnOff endpoint (the
		// primary); the flag must fan the valve out to more than one.
		if wsmOnOff < 2 {
			t.Errorf("expose_secondary_channels on: WSM materialised %d OnOff endpoint(s); expected the primary plus at least one secondary/status channel", wsmOnOff)
		}

		if _, err := ctl.Unpair(context.Background(), t); err != nil {
			t.Logf("unpair (best-effort): %v", err)
		}
	})
}
