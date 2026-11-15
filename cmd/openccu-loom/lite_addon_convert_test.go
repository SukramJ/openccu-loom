// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/godevccu/pkg/litefake"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// putRecorder notes the enabled flag of every Put, in order: a disabled save
// is a removal to the live admin service, which deletes what is stored under
// the central's name, so the sequence shows whether a move keeps it.
type putRecorder struct {
	handlers.CentralAdminService
	mu   sync.Mutex
	puts []bool
}

func (p *putRecorder) Put(ctx context.Context, row sqlitestore.CentralRow) error {
	p.mu.Lock()
	p.puts = append(p.puts, row.Enabled)
	p.mu.Unlock()
	return p.CentralAdminService.Put(ctx, row)
}

func (p *putRecorder) sequence() []bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]bool(nil), p.puts...)
}

// migratedCCURow is the central a box brings along from OpenCCU: the CCU
// on the loopback, with its account, RPC ports and CUxD.
func migratedCCURow(name string) sqlitestore.CentralRow {
	return sqlitestore.CentralRow{
		Name: name, Host: "127.0.0.1", Enabled: true,
		Username: "Admin", PasswordPlain: "secret",
		Port: 2010, Ports: map[string]int{"HmIP-RF": 2010},
		PrimaryInterface: "CUxD",
		Interfaces:       []config.InterfaceSpec{{Name: "HmIP-RF", Port: 2010}, {Name: "CUxD"}},
	}
}

// TestLiteAddonOnboardingMovesTheMigratedCentralToLite pins what happens to
// the central a move from OpenCCU to openccu-lite leaves behind: the one CCU
// central pointing at the box itself becomes an openccu-lite central under
// its own name, credentialed by the add-on token file, with the box's
// interfaces and none of the CCU's fields — in one enabled save, which the
// live admin service turns into a restart; a disabled save first would be a
// removal and delete what is stored under the central's name.
func TestLiteAddonOnboardingMovesTheMigratedCentralToLite(t *testing.T) {
	store, fake := startOnboardFixture(t)
	ctx := t.Context()
	if err := store.Put(ctx, migratedCCURow("Otto")); err != nil {
		t.Fatalf("seed: %v", err)
	}
	admin := &putRecorder{CentralAdminService: store}

	maybeStartLiteAddonOnboarding(ctx, admin, nil, slog.New(slog.DiscardHandler))

	var row sqlitestore.CentralRow
	deadline := time.Now().Add(20 * time.Second)
	for {
		rows, err := store.List(ctx)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(rows) == 1 && rows[0].SystemType == "openccu-lite" && rows[0].Enabled {
			row = rows[0]
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the central was not moved to openccu-lite: %+v", rows)
		}
		time.Sleep(100 * time.Millisecond)
	}

	if row.Name != "Otto" {
		t.Errorf("name = %q, want the central's own name kept", row.Name)
	}
	if row.APITokenFile != os.Getenv(liteAddonTokenPathEnv) || row.APITokenPlain != "" {
		t.Errorf("token file = %q plain = %q, want the add-on token file only", row.APITokenFile, row.APITokenPlain)
	}
	if row.Username != "" || row.PasswordPlain != "" || row.Port != 0 || len(row.Ports) != 0 {
		t.Errorf("CCU fields survived: user %q password %q port %d ports %v", row.Username, row.PasswordPlain, row.Port, row.Ports)
	}
	u, err := url.Parse(fake.URL())
	if err != nil {
		t.Fatal(err)
	}
	if row.Host != u.Hostname() {
		t.Errorf("host = %q, want %q", row.Host, u.Hostname())
	}
	got := make([]string, 0, len(row.Interfaces))
	for _, in := range row.Interfaces {
		if in.Port != 0 {
			t.Errorf("interface %s keeps port %d", in.Name, in.Port)
		}
		got = append(got, in.Name)
	}
	if want := litefake.DefaultInterfaces(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("interfaces = %v, want the box's %v", got, want)
	}
	if row.PrimaryInterface != "" {
		t.Errorf("primary interface = %q, want it dropped with CUxD", row.PrimaryInterface)
	}
	if seq := admin.sequence(); len(seq) != 1 || !seq[0] {
		t.Errorf("Put sequence (enabled) = %v, want [true]", seq)
	}
}

// TestLiteAddonOnboardingLeavesOtherCentralsAsTheyAre pins the limits of the
// move: a CCU elsewhere on the network, two centrals that both point at the
// box, and a box that already has a central on the add-on token are not
// touched.
func TestLiteAddonOnboardingLeavesOtherCentralsAsTheyAre(t *testing.T) {
	cases := map[string]func(token string) []sqlitestore.CentralRow{
		"remote ccu": func(string) []sqlitestore.CentralRow {
			row := migratedCCURow("remote")
			row.Host = "192.0.2.9"
			return []sqlitestore.CentralRow{row}
		},
		"two local centrals": func(string) []sqlitestore.CentralRow {
			return []sqlitestore.CentralRow{migratedCCURow("one"), migratedCCURow("two")}
		},
		"box already a central": func(token string) []sqlitestore.CentralRow {
			return []sqlitestore.CentralRow{
				migratedCCURow("old"),
				{
					Name: "box", Host: "127.0.0.1", SystemType: "openccu-lite", APITokenFile: token, Enabled: true,
					Interfaces: []config.InterfaceSpec{{Name: "HmIP-RF"}},
				},
			}
		},
	}
	for name, seed := range cases {
		t.Run(name, func(t *testing.T) {
			store, _ := startOnboardFixture(t)
			ctx := t.Context()
			for _, row := range seed(os.Getenv(liteAddonTokenPathEnv)) {
				if err := store.Put(ctx, row); err != nil {
					t.Fatalf("seed: %v", err)
				}
			}
			admin := &putRecorder{CentralAdminService: store}

			maybeStartLiteAddonOnboarding(ctx, admin, nil, slog.New(slog.DiscardHandler))
			time.Sleep(2 * time.Second)

			if seq := admin.sequence(); len(seq) != 0 {
				t.Errorf("the onboarding wrote %d central(s), want none", len(seq))
			}
		})
	}
}

// TestLiteAddonOnboardingNamesACentralItCannotMove pins the health hint: a
// box without the add-on token cannot have its migrated central moved, and
// says so on /health instead of leaving it waiting for a CCU without a word.
func TestLiteAddonOnboardingNamesACentralItCannotMove(t *testing.T) {
	store, _ := startOnboardFixture(t)
	ctx := t.Context()
	if err := os.Remove(os.Getenv(liteAddonTokenPathEnv)); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(ctx, migratedCCURow("Otto")); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tracker := health.NewTracker()

	maybeStartLiteAddonOnboarding(ctx, store, tracker, slog.New(slog.DiscardHandler))

	deadline := time.Now().Add(10 * time.Second)
	for {
		if c, ok := tracker.Get(liteAddonCentralsHealthComponent); ok {
			if !strings.Contains(c.LastSample.Note, "Otto") {
				t.Errorf("note %q does not name the central", c.LastSample.Note)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no health component for the central that could not be moved")
		}
		time.Sleep(50 * time.Millisecond)
	}
	rows, err := store.List(ctx)
	if err != nil || len(rows) != 1 || rows[0].SystemType == "openccu-lite" {
		t.Errorf("rows = %+v err = %v, want the central untouched", rows, err)
	}
}

// TestHostIsThisMachine pins what counts as the box itself: the loopback,
// and an address one of the machine's interfaces carries — a central
// created with the box's LAN address is as local as one on 127.0.0.1 —
// and nothing else on the same network.
func TestHostIsThisMachine(t *testing.T) {
	t.Parallel()
	o := &liteAddonOnboarder{ifaceAddrs: func() ([]net.Addr, error) {
		return []net.Addr{
			&net.IPNet{IP: net.ParseIP("192.0.2.50"), Mask: net.CIDRMask(24, 32)},
			&net.IPNet{IP: net.ParseIP("2001:db8::50"), Mask: net.CIDRMask(64, 128)},
		}, nil
	}}
	for host, want := range map[string]bool{
		"127.0.0.1":    true,
		"::1":          true,
		"192.0.2.50":   true,
		"2001:db8::50": true,
		"192.0.2.51":   false,
		"2001:db8::51": false,
	} {
		if got := o.hostIsThisMachine(t.Context(), host); got != want {
			t.Errorf("hostIsThisMachine(%q) = %v, want %v", host, got, want)
		}
	}
}
