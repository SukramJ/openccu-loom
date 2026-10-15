//go:build chiptool

// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package chiptool

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/tests/chiptool/harness"
)

// TestSendReceive_FixedColor drives the palette-quantised ColorControl of
// a fixed-colour light (HmIP-BSL, ADR 0012's mapping table) end to end:
// a hue chip-tool writes becomes one COLOR slot on the CCU, and the slot
// the CCU confirms is what the attributes settle on. The BSL runs on its
// own bridge like the HmIP-LSC: the shared fleet keeps its endpoint count
// moderate on purpose (harness.DefaultDevices).
func TestSendReceive_FixedColor(t *testing.T) {
	chipBin := harness.RequireChipTool(t)
	b := harness.Start(t, chipBin, harness.Options{
		CASEEnabled: true,
		Devices:     []string{"HmIP-BSL"},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	expCtx, expCancel := context.WithTimeout(ctx, 15*time.Second)
	if _, err := b.EnableAllExposures(expCtx); err != nil {
		t.Fatalf("enable exposures: %v", err)
	}
	expCancel()
	time.Sleep(1500 * time.Millisecond)

	ctl := harness.NewController(t, chipBin, 0x5303)
	out, err := ctl.PairFull(ctx, t, harness.PairTargetHost, b.MatterPort())
	if err != nil {
		t.Fatalf("pairing: %v\n%s", err, out)
	}
	if !harness.PairingSuccess(out) {
		t.Fatalf("pairing did not report success:\n%s", out)
	}
	b.SharedCtl = ctl

	slOut, err := ctl.ReadAttr(ctx, t, "descriptor", "server-list", 0xFFFF)
	if err != nil {
		t.Fatalf("wildcard server-list: %v", err)
	}
	perEP := harness.ServerListIDsPerEndpoint(slOut)
	eps := make([]uint16, 0, len(perEP))
	for e := range perEP {
		eps = append(eps, e)
	}
	sort.Slice(eps, func(i, j int) bool { return eps[i] < eps[j] })
	var ep uint16
	found := false
	for _, e := range eps {
		if harness.HasCluster(perEP[e], 0x0300) {
			ep = e
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("no ColorControl (0x0300) endpoint materialised for HmIP-BSL; server lists: %v", perEP)
	}

	// The BSL bridges two fixed-colour channels (8 and 12); the endpoint
	// inspector names the channel each endpoint serves, so the CCU-side
	// checks read the right one.
	var inspector struct {
		Endpoints []struct {
			EndpointID     uint16 `json:"endpoint_id"`
			ChannelAddress string `json:"channel_address"`
		} `json:"endpoints"`
	}
	if status := b.RESTGet(t, "/api/v1/matter/endpoints", &inspector); status != 200 {
		t.Fatalf("GET /api/v1/matter/endpoints: status %d", status)
	}
	var address string
	for _, e := range inspector.Endpoints {
		if e.EndpointID == ep {
			address = e.ChannelAddress
		}
	}
	if address == "" {
		t.Fatalf("the endpoint inspector names no channel for endpoint %d: %+v", ep, inspector.Endpoints)
	}

	// The device must be on for a plain MoveTo* to execute (ExecuteIfOff
	// is not set): the OnOff server's Sink writes LEVEL on the CCU.
	if _, err := b.SharedCtl.Invoke(ctx, t, "onoff", "on", ep); err != nil {
		t.Fatalf("invoke onoff on: %v", err)
	}
	// Wait for the on write to land on the CCU (LEVEL > 0): the gate a
	// plain MoveTo* checks reads the light's confirmed state.
	deadlineOn := time.Now().Add(5 * time.Second)
	for {
		if lv, ok := b.CCU.GetDPValue(address, "LEVEL"); ok && valueNear(lv, 1.0, 0.99) && !valueNear(lv, 0, 0.001) {
			break
		}
		if time.Now().After(deadlineOn) {
			lv, _ := b.CCU.GetDPValue(address, "LEVEL")
			t.Fatalf("CCU LEVEL after onoff on = %v, want > 0", lv)
		}
		time.Sleep(250 * time.Millisecond)
	}

	// readHue reads CurrentHue, retrying briefly: the slot's reference
	// colour lands on the server through the CCU's confirmed COLOR, which
	// arrives on the event path after the write returned.
	readHue := func(t *testing.T, want int64) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		var got int64 = -1
		for time.Now().Before(deadline) {
			read, err := b.SharedCtl.ReadAttr(ctx, t, "colorcontrol", "current-hue", ep)
			if err != nil {
				t.Fatalf("read current-hue: %v", err)
			}
			if v, ok := harness.FindAttrUint(read, "CurrentHue"); ok {
				got = v
				if v == want {
					return
				}
			}
			time.Sleep(250 * time.Millisecond)
		}
		t.Fatalf("CurrentHue = %d, want the slot's reference %d", got, want)
	}

	// 100° is Matter hue 71 and lies in the GREEN band; the server settles
	// on GREEN's reference colour, 120° = Matter hue 85. The saturation
	// travels with the first command: the server starts on the white point
	// (the CCU reported no colour yet), and a MoveToHue alone would keep
	// saturation 0, which is WHITE whatever the hue.
	t.Run("send/move-to-hue-and-saturation-snaps-to-green", func(t *testing.T) {
		if _, err := b.SharedCtl.Invoke(ctx, t, "colorcontrol", "move-to-hue-and-saturation", ep,
			"71", "254", "0", "1", "1"); err != nil {
			t.Fatalf("invoke move-to-hue-and-saturation: %v", err)
		}
		assertCCUColor(t, b, address, "GREEN")
		readHue(t, 85)
	})

	// With the saturation held, a MoveToHue alone snaps by hue: 20° is
	// in RED's wrap-around band.
	t.Run("send/move-to-hue-snaps-to-red", func(t *testing.T) {
		if _, err := b.SharedCtl.Invoke(ctx, t, "colorcontrol", "move-to-hue", ep,
			"14", "0", "0", "1", "1"); err != nil {
			t.Fatalf("invoke move-to-hue: %v", err)
		}
		assertCCUColor(t, b, address, "RED")
		readHue(t, 0)
	})

	// 240° = Matter hue 169 lies in the BLUE band exactly on its
	// reference, so the attributes keep the commanded value.
	t.Run("send/move-to-hue-and-saturation-blue", func(t *testing.T) {
		if _, err := b.SharedCtl.Invoke(ctx, t, "colorcontrol", "move-to-hue-and-saturation", ep,
			"169", "254", "0", "1", "1"); err != nil {
			t.Fatalf("invoke move-to-hue-and-saturation: %v", err)
		}
		assertCCUColor(t, b, address, "BLUE")
		readHue(t, 169)
	})

	// A desaturated colour is the WHITE slot whatever its hue.
	t.Run("send/move-to-saturation-zero-is-white", func(t *testing.T) {
		if _, err := b.SharedCtl.Invoke(ctx, t, "colorcontrol", "move-to-saturation", ep,
			"0", "0", "1", "1"); err != nil {
			t.Fatalf("invoke move-to-saturation: %v", err)
		}
		assertCCUColor(t, b, address, "WHITE")
		read, err := b.SharedCtl.ReadAttr(ctx, t, "colorcontrol", "current-saturation", ep)
		if err != nil {
			t.Fatalf("read current-saturation: %v", err)
		}
		if v, ok := harness.FindAttrUint(read, "CurrentSaturation"); !ok || v != 0 {
			t.Fatalf("CurrentSaturation after WHITE = %v (ok=%v), want 0\n%s", v, ok, read)
		}
	})

	// The receive path: a slot the CCU reports on its own (a wall button, a
	// CCU program) reaches the subscribed attributes as its reference colour.
	t.Run("receive/ccu-color-reaches-current-hue", func(t *testing.T) {
		if err := b.CCU.SetDPValue(address, "COLOR", fixedColorSlot(t, "RED")); err != nil {
			t.Fatalf("inject COLOR=RED: %v", err)
		}
		readHue(t, 0)
		if err := b.CCU.SetDPValue(address, "COLOR", fixedColorSlot(t, "YELLOW")); err != nil {
			t.Fatalf("inject COLOR=YELLOW: %v", err)
		}
		readHue(t, 42) // 60° = Matter hue 42
	})
}

// assertCCUColor checks the CCU-side COLOR of the channel against a slot
// label; the simulator reports an ENUM as its index or its label.
func assertCCUColor(t *testing.T, b *harness.Bridge, address, want string) {
	t.Helper()
	got, ok := b.CCU.GetDPValue(address, "COLOR")
	if !ok {
		t.Fatalf("COLOR absent on CCU")
	}
	if s, isString := got.(string); isString {
		if s != want {
			t.Fatalf("CCU COLOR = %q, want %q", s, want)
		}
		return
	}
	if !valueNear(got, float64(fixedColorSlot(t, want)), 0.1) {
		t.Fatalf("CCU COLOR = %v, want %s (slot %d)\ndaemon stderr tail:\n%s", got, want, fixedColorSlot(t, want), stderrTail(b.Stderr(), 25))
	}
}

// fixedColorSlot is the index of a COLOR label in the HmIP-BSL's value
// list (BLACK, BLUE, GREEN, TURQUOISE, RED, PURPLE, YELLOW, WHITE), the
// raw value the CCU reports and accepts for that colour.
func fixedColorSlot(t *testing.T, label string) int {
	t.Helper()
	list := []string{"BLACK", "BLUE", "GREEN", "TURQUOISE", "RED", "PURPLE", "YELLOW", "WHITE"}
	for i, n := range list {
		if n == label {
			return i
		}
	}
	t.Fatalf("%q is not a BSL colour", label)
	return -1
}

// stderrTail returns the last n lines of the daemon's captured stderr.
func stderrTail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
