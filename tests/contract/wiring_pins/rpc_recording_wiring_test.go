// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package wiring_pins

import (
	"context"
	"log/slog"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"sukramj.dev/godevccu/pkg/litefake"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// startRecordingLiteCentral is [startLiteCentral] that also hands back the
// registry and the value writer the composition entry point was wired
// with, so a pin can drive the RPC recorder adapter and a write through the
// same objects a running daemon uses.
func startRecordingLiteCentral(t *testing.T, fake *litefake.Fake) (*central.Unit, *central.Registry, *client.ValueWriter) {
	t.Helper()
	u, err := url.Parse(fake.URL())
	if err != nil {
		t.Fatalf("parse fake URL: %v", err)
	}
	host, portStr, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatalf("split host: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}
	cfg := &config.Config{Centrals: []config.CentralConfig{{
		Name:        "box",
		Host:        host,
		JSONRPCPort: port,
		SystemType:  hmenum.SystemTypeOpenCCULite,
		APIToken:    litefake.DefaultToken,
		Interfaces:  []config.InterfaceSpec{{Name: "BidCos-RF"}, {Name: "HmIP-RF"}},
	}}}
	reg := central.NewRegistry()
	unit, err := central.New(central.Config{Name: "box"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	if err := reg.Register(unit); err != nil {
		t.Fatalf("Registry.Register: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	writer := client.NewValueWriter()
	mgr, err := adapter.WireCentrals(ctx, cfg, reg, adapter.WireDeps{Writer: writer}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("WireCentrals: %v", err)
	}
	t.Cleanup(mgr.Teardown)
	return unit, reg, writer
}

// recordedMethods lists the "<rpcType>/<method>" buckets the central's
// session recorder holds.
func recordedMethods(unit *central.Unit) []string {
	sessions := unit.Recorder.GetSessions()
	out := make([]string, 0, len(sessions))
	for bucket := range sessions {
		out = append(out, bucket)
	}
	sort.Strings(out)
	return out
}

// writableBool finds a writable BOOL parameter in a VALUES paramset the
// central loaded, so the write half of the pin targets a data point the box
// accepts.
func writableBool(unit *central.Unit) (wireID, address, parameter string, ok bool) {
	for _, dev := range unit.ModelRegistry.List() {
		iface := hmtypes.ParseWireInterfaceID(dev.InterfaceID)
		for _, ch := range dev.Channels() {
			ps, found := unit.ParamsetReg.Get(iface, ch.Address, hmenum.ParamsetKeyValues)
			if !found {
				continue
			}
			names := make([]string, 0, len(ps))
			for name := range ps {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				pd := ps[name]
				if pd.Type == hmenum.ParameterTypeBool && pd.Operations.IsWritable() {
					return dev.InterfaceID, ch.Address, name, true
				}
			}
		}
	}
	return "", "", "", false
}

// TestRPCRecordingCapturesSouthboundCallsOfARunningCentral pins the RPC
// recording — POST /diagnostics/rpc-recording — through the composition
// root: once the recorder adapter starts a session, the central's
// south-bound calls land in its trace, a read (listDevices during the
// bring-up) as well as a write through the daemon's value writer.
//
// The box boots not ready so the recording is armed before the bring-up's
// reads happen, the order a running daemon sees for every reconnect. Two
// negative controls bracket the effect: nothing is recorded while no
// session is active, before Start and after Stop.
func TestRPCRecordingCapturesSouthboundCallsOfARunningCentral(t *testing.T) {
	fake := startFake(t, litefake.Options{StartNotReady: true})
	unit, reg, writer := startRecordingLiteCentral(t, fake)
	if unit.Recorder == nil {
		t.Fatal("the central has no session recorder; the RPC recording has nothing to fill")
	}
	recorder := adapter.NewRPCRecorderAdapter(reg, "")

	// Negative control: the recorder is created inactive, so whatever the
	// central has sent so far (the readiness probing) leaves no trace.
	time.Sleep(300 * time.Millisecond)
	if got := recordedMethods(unit); len(got) != 0 {
		t.Fatalf("entries recorded before the recording was started: %v", got)
	}

	recorder.Start(nil, 0, false)
	t.Cleanup(func() { recorder.Stop(nil) })

	fake.SetReady(true)
	waitLiteReady(t, unit)

	if got := recordedMethods(unit); !containsSuffix(got, "/listDevices") {
		t.Fatalf("the bring-up's listDevices was not recorded; recorded buckets: %v", got)
	}

	wireID, address, parameter, ok := writableBool(unit)
	if !ok {
		t.Fatal("the fleet offers no writable BOOL VALUES parameter; the write half cannot be driven")
	}
	if err := writer.SetValue(
		context.Background(), "box", wireID, address,
		hmenum.Parameter(parameter), true, hmenum.CommandPriorityHigh,
	); err != nil {
		t.Fatalf("SetValue %s %s:%s through the value writer: %v", wireID, address, parameter, err)
	}
	if got := recordedMethods(unit); !containsSuffix(got, "/setValue") {
		t.Fatalf("the write was not recorded; recorded buckets: %v", got)
	}

	// Negative control: after Stop a further write — with a different
	// value, so it would form a new trace key — adds nothing.
	recorder.Stop(nil)
	before := recordedKeys(unit, "/setValue")
	if before == 0 {
		t.Fatal("no setValue key to compare against after Stop")
	}
	if err := writer.SetValue(
		context.Background(), "box", wireID, address,
		hmenum.Parameter(parameter), false, hmenum.CommandPriorityHigh,
	); err != nil {
		t.Fatalf("SetValue after Stop: %v", err)
	}
	if after := recordedKeys(unit, "/setValue"); after != before {
		t.Errorf("a write after Stop was recorded: setValue keys %d -> %d", before, after)
	}
}

// recordedKeys counts the distinct parameter sets recorded under every
// bucket whose name ends in suffix.
func recordedKeys(unit *central.Unit, suffix string) int {
	n := 0
	for bucket, params := range unit.Recorder.GetSessions() {
		if strings.HasSuffix(bucket, suffix) {
			n += len(params)
		}
	}
	return n
}

func containsSuffix(buckets []string, suffix string) bool {
	for _, b := range buckets {
		if strings.HasSuffix(b, suffix) {
			return true
		}
	}
	return false
}
