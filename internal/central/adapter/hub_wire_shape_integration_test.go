// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build integration

// hub_wire_shape_integration_test.go drives two hub-side paths against
// the CCU simulator in its realistic personality, from inside the
// package that owns them.
//
// Both decode into unexported types — the sysvar DTO and the JSON-RPC
// caller the backend is built on — so `tests/integration` cannot reach
// either. Testing them from outside would mean re-implementing the
// decode, which tests the re-implementation.

package adapter

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/godevccu/pkg/godevccu"

	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/client/rega"
	"github.com/SukramJ/openccu-loom/internal/client/transport/jsonrpc"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// startRealisticCCU boots a simulator answering with the CCU's own
// JSON-RPC payload shapes and object ids.
func startRealisticCCU(t *testing.T) *godevccu.VirtualCCU {
	t.Helper()

	v, err := godevccu.New(godevccu.Config{
		Mode:          godevccu.BackendModeCCU,
		Host:          "127.0.0.1",
		XMLRPCPort:    godevccu.EphemeralPort,
		JSONRPCPort:   godevccu.EphemeralPort,
		Username:      "Admin",
		Devices:       []string{"HmIP-BSM", "HmIP-BROLL"},
		SetupDefaults: true,
		Realism: godevccu.Realism{
			JSONSchema: true,
			RegaIDs:    true,
			Lifecycle:  true,
		},
	})
	if err != nil {
		t.Fatalf("godevccu.New: %v", err)
	}
	if err := v.Start(); err != nil {
		t.Fatalf("godevccu.Start: %v", err)
	}
	t.Cleanup(func() { _ = v.Stop() })
	return v
}

// loggedInClient returns a JSON-RPC client with a live session.
func loggedInClient(t *testing.T, v *godevccu.VirtualCCU) *jsonrpc.Client {
	t.Helper()

	c, err := jsonrpc.New(jsonrpc.Config{
		Endpoint: "http://" + v.JSONRPCAddr().String() + "/api/homematic.cgi",
		Username: "Admin",
	})
	if err != nil {
		t.Fatalf("jsonrpc.New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	if err := c.Login(ctx); err != nil {
		t.Fatalf("login: %v", err)
	}
	return c
}

// TestSysvarsDecodeFromTheCCUPayloadShape runs the production sysvar
// load against the payload a CCU actually returns.
//
// `SysVar.getAll` reports its values as *strings* and carries only the
// fields that apply to each type — a LOGIC variable has value names, a
// NUMBER has min and max, a LIST has a value list. The simulator used
// to answer with Go-native types and every field populated, which is
// the one shape that cannot expose a decode that assumes a bool or a
// float. A sysvar whose value fails to decode is not a loud failure: it
// spawns with the zero value, and an operator sees a switch that is off
// or a number that is nought.
func TestSysvarsDecodeFromTheCCUPayloadShape(t *testing.T) {
	v := startRealisticCCU(t)
	jc := loggedInClient(t, v)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var entries []sysvarEntry
	if err := jc.Call(ctx, "SysVar.getAll", nil, &entries); err != nil {
		t.Fatalf("SysVar.getAll into the production DTO: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("the fixture reports no system variables; the decode below would assert nothing")
	}

	for _, e := range entries {
		if e.ID == "" {
			t.Errorf("sysvar %q carries no id — the hub keys its model on it, and an empty id "+
				"collapses every variable onto one entry", e.Name)
		}
		if e.Name == "" {
			t.Errorf("sysvar id %q carries no name", e.ID)
		}
		if len(e.Value) == 0 {
			t.Errorf("sysvar %q (%s) carries no value; it spawns with the zero value and reads "+
				"as off or nought on every surface", e.Name, e.Type)
			continue
		}
		// The CCU stringifies values. What matters is that the raw
		// message survives to the coercion layer intact rather than
		// failing the decode outright.
		var probe any
		if err := json.Unmarshal(e.Value, &probe); err != nil {
			t.Errorf("sysvar %q value %s is not decodable JSON: %v", e.Name, e.Value, err)
		}
	}

	// RegaIDs assigns the numeric object ids a CCU uses. Without them a
	// client stores a textual address where an ise_id belongs.
	for _, e := range entries {
		if strings.TrimSpace(e.ID) != e.ID || e.ID == "0" {
			t.Errorf("sysvar %q has id %q, which is not a usable ReGa object id", e.Name, e.ID)
		}
	}
}

// TestRoomsReportNumericChannelIds pins the other half of the object
// ids: a room reports its channels as the numeric ReGa ids a CCU
// assigns, not as the addresses the simulator holds internally.
//
// The daemon stores what it is given and resolves room membership by
// it. Handed an address where an ise_id belongs, every room assignment
// an operator made on the CCU resolves to nothing — and a room that
// resolves to nothing is indistinguishable from one nobody has filled.
//
// The fixture's default rooms carry no channels at all, so the test
// assigns one itself; without that it would pass on an empty list and
// assert nothing.
func TestRoomsReportNumericChannelIds(t *testing.T) {
	requireSimulatorFix(t, "0.2.2", "assign object ids at start-up or report them as strings")

	v := startRealisticCCU(t)

	channel := firstChannelAddress(t, v)
	v.State().AddRoom("Test Room", "assigned by the test", []string{channel}, 0)

	jc := loggedInClient(t, v)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	rooms, err := jc.GetAllRoomsRaw(ctx)
	if err != nil {
		t.Fatalf("Room.getAll: %v", err)
	}

	var ids []string
	for _, r := range rooms {
		if r.Name == "Test Room" {
			ids = r.ChannelIDs
		}
	}
	if len(ids) == 0 {
		t.Fatalf("the room the test assigned a channel to reports none; rooms seen: %d", len(rooms))
	}
	for _, id := range ids {
		if _, err := strconv.Atoi(id); err != nil {
			t.Errorf("room channel id %q is not a numeric ReGa id (the address is %q) — the "+
				"daemon stores it where an ise_id belongs and every membership lookup misses",
				id, channel)
		}
	}
}

// firstChannelAddress returns a channel address the loaded fixture
// carries.
func firstChannelAddress(t *testing.T, v *godevccu.VirtualCCU) string {
	t.Helper()
	for _, d := range v.RPC().ListDevices() {
		addr, _ := d["ADDRESS"].(string)
		if strings.Contains(addr, ":") {
			return addr
		}
	}
	t.Fatal("fixture carries no channel address")
	return ""
}

// TestInstallModeReportsARemainingCountdown covers what the daemon
// reads back after opening a pairing window.
//
// SetInstallMode starts a countdown on the CCU and GetInstallMode
// reports the seconds left; the SPA renders that as the window's timer.
// While the simulator answered a constant 0, an open window was
// indistinguishable from a closed one on every surface that shows it,
// and the read path had nothing to be wrong about.
//
// The call goes through the production JSON-RPC caller and the real
// backend, because the daemon's install-mode path is JSON-RPC even
// though its sibling operations are XML-RPC — a test built on an
// invented caller would encode a guess at that dispatch.
func TestInstallModeReportsARemainingCountdown(t *testing.T) {
	requireSimulatorFix(t, "0.2.2", "run the pairing automaton on JSON-RPC")

	v := startRealisticCCU(t)
	jc := loggedInClient(t, v)

	runner, err := rega.NewRunner(rega.Config{Client: jc})
	if err != nil {
		t.Fatalf("rega.NewRunner: %v", err)
	}
	backend := backends.NewCcuBackendForInterface(hmenum.InterfaceHmIPRF,
		nil, &jsonrpcCaller{client: runner.Client()}, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if remaining, err := backend.GetInstallMode(ctx); err != nil {
		t.Fatalf("GetInstallMode before opening: %v", err)
	} else if remaining != 0 {
		t.Fatalf("install mode reports %ds remaining before anything opened it", remaining)
	}

	const duration = 120
	if err := backend.SetInstallMode(ctx, true, duration, 1, ""); err != nil {
		t.Fatalf("SetInstallMode: %v", err)
	}

	remaining, err := backend.GetInstallMode(ctx)
	if err != nil {
		t.Fatalf("GetInstallMode after opening: %v", err)
	}
	if remaining <= 0 || remaining > duration {
		t.Errorf("install mode reports %ds remaining after opening a %ds window; want a "+
			"countdown inside (0, %d] — a window reporting 0 while open reads as closed on "+
			"every surface", remaining, duration, duration)
	}
}

// requireSimulatorFix skips a test whose subject is a simulator
// behaviour that older versions do not have. The gate is a version
// comparison rather than a probe of the behaviour itself: probing would
// mean deciding "the feature is missing" from the same signal the test
// is about, and a test that skips itself on failure asserts nothing.
//
// It removes itself: once the dependency is at or past the version, the
// skip never fires again.
func requireSimulatorFix(t *testing.T, minVersion, what string) {
	t.Helper()
	if compareVersions(godevccu.Version, minVersion) >= 0 {
		return
	}
	t.Skipf("godevccu %s does not %s; needs %s or newer", godevccu.Version, what, minVersion)
}

// compareVersions orders two dotted numeric versions.
func compareVersions(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		var an, bn int
		if i < len(as) {
			an, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			bn, _ = strconv.Atoi(bs[i])
		}
		if an != bn {
			if an < bn {
				return -1
			}
			return 1
		}
	}
	return 0
}
