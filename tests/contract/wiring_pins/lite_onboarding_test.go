// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package wiring_pins

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/godevccu/pkg/litefake"

	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/transport/occulited"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/tests/contract"
)

// TestOnboardingRoutesAreMounted pins that the daemon hands the onboarding
// domain to the REST router and to the first-run setup service: without
// either line the probe and pairing routes, or the wizard's pairing id,
// silently disappear.
func TestOnboardingRoutesAreMounted(t *testing.T) {
	contract.MustFindStructLiteralField(t, "cmd/openccu-loom/daemon_rest_mount.go", "rest.Deps", "Onboarding")
	contract.MustFindStructLiteralField(t, "cmd/openccu-loom/daemon_rest_mount.go", "handlers.SetupService", "Onboarding")
}

// persisted records the system types an `auto` central resolves to.
type persisted struct {
	mu    sync.Mutex
	types map[string]hmenum.SystemType
}

func (p *persisted) record(_ context.Context, name string, st hmenum.SystemType) {
	p.mu.Lock()
	p.types[name] = st
	p.mu.Unlock()
}

func (p *persisted) get(name string) hmenum.SystemType {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.types[name]
}

func hostPort(t *testing.T, raw string) (host string, port int) {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	host, portStr, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	port, err = strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("port: %v", err)
	}
	return host, port
}

func wireAuto(t *testing.T, name, host string, port int, token string, p *persisted) *central.Unit {
	t.Helper()
	cfg := &config.Config{Centrals: []config.CentralConfig{{
		Name: name, Host: host, JSONRPCPort: port, SystemType: hmenum.SystemTypeAuto, APIToken: token,
		Interfaces: []config.InterfaceSpec{{Name: "BidCos-RF"}, {Name: "HmIP-RF"}},
	}}}
	reg := central.NewRegistry()
	unit, err := central.New(central.Config{Name: name})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	if err := reg.Register(unit); err != nil {
		t.Fatalf("Register: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	mgr, err := adapter.WireCentrals(ctx, cfg, reg,
		adapter.WireDeps{Writer: client.NewValueWriter(), PersistSystemType: p.record}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("WireCentrals: %v", err)
	}
	t.Cleanup(mgr.Teardown)
	return unit
}

// TestAutoSystemTypeResolvesAndPersists pins `system_type: auto` through
// the composition entry point: a box whose API is still starting is
// already identified as openccu-lite, the type is persisted, and the
// central then comes up as a lite central once the box is ready; a system
// whose ReGa check answers OK is identified as a CCU.
func TestAutoSystemTypeResolvesAndPersists(t *testing.T) {
	p := &persisted{types: map[string]hmenum.SystemType{}}

	fake := startFake(t, litefake.Options{StartNotReady: true})
	host, port := hostPort(t, fake.URL())
	unit := wireAuto(t, "box", host, port, litefake.DefaultToken, p)
	waitFor(t, 10*time.Second, "the lite type to be persisted", func() bool {
		return p.get("box") == hmenum.SystemTypeOpenCCULite
	})
	fake.SetReady(true)
	waitLiteReady(t, unit)
	if len(unit.ModelRegistry.List()) == 0 {
		t.Error("the resolved lite central loaded no devices")
	}

	ccu := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ise/checkrega.cgi" {
			_, _ = w.Write([]byte("OK"))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(ccu.Close)
	host, port = hostPort(t, ccu.URL)
	wireAuto(t, "ccu", host, port, "", p)
	waitFor(t, 10*time.Second, "the ccu type to be persisted", func() bool {
		return p.get("ccu") == hmenum.SystemTypeCCU
	})
}

// capturingCentrals records the rows a central write stores.
type capturingCentrals struct {
	mu   sync.Mutex
	rows map[string]sqlite.CentralRow
}

func (c *capturingCentrals) Put(_ context.Context, row sqlite.CentralRow) error {
	c.mu.Lock()
	c.rows[row.Name] = row
	c.mu.Unlock()
	return nil
}

func (c *capturingCentrals) Get(_ context.Context, name string) (sqlite.CentralRow, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.rows[name]
	if !ok {
		return sqlite.CentralRow{}, sqlite.ErrCentralNotFound
	}
	return r, nil
}

func (c *capturingCentrals) Delete(context.Context, string) error { return nil }

func (c *capturingCentrals) List(context.Context) ([]sqlite.CentralRow, error) { return nil, nil }

// TestPairingTokenNeverLeavesTheDaemon pins the pairing flow end to end
// over the REST handlers: a pairing is started and approved on the box, a
// central is created naming the pairing, and it stores a token the box
// accepts — while no answer the client received in the whole flow carries
// a token.
func TestPairingTokenNeverLeavesTheDaemon(t *testing.T) {
	fake := startFake(t, litefake.Options{})
	host, port := hostPort(t, fake.URL())
	onboarding := adapter.NewLiteOnboarding("loom-test", nil, "en", slog.New(slog.DiscardHandler))
	store := &capturingCentrals{rows: map[string]sqlite.CentralRow{}}
	mux := chi.NewRouter()
	mux.Post("/centrals/pairing", handlers.StartCentralPairing(onboarding))
	mux.Get("/centrals/pairing/{id}", handlers.CentralPairingStatus(onboarding))
	mux.Post("/centrals", handlers.CreateCentral(store, nil, onboarding))

	var bodies []string
	call := func(method, path, body string, want int) []byte {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req = req.WithContext(auth.ContextWithIdentity(req.Context(), auth.Identity{Subject: "admin", Role: auth.RoleAdmin}))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		bodies = append(bodies, w.Body.String())
		if w.Code != want {
			t.Fatalf("%s %s: status %d, body %s", method, path, w.Code, w.Body)
		}
		return w.Body.Bytes()
	}

	var started hmapi.CentralPairingStarted
	_ = json.Unmarshal(call(http.MethodPost, "/centrals/pairing",
		`{"host":"`+host+`","port":`+strconv.Itoa(port)+`}`, http.StatusCreated), &started)
	if started.PairingID == "" || len(started.Code) != 6 {
		t.Fatalf("pairing started = %+v", started)
	}
	// The first poll reveals the client's nonce; only then can the box's
	// administrator approve.
	call(http.MethodGet, "/centrals/pairing/"+started.PairingID, "", http.StatusOK)
	pending := fake.Pairings()
	if len(pending) != 1 || pending[0].Code != started.Code {
		t.Fatalf("the box holds %+v; want one request showing code %s", pending, started.Code)
	}
	if _, err := fake.ApprovePairing(pending[0].ID, started.Code); err != nil {
		t.Fatalf("ApprovePairing: %v", err)
	}
	// A short poll right after the first would be told to slow down; the
	// long poll is exempt and answers as soon as the box approved.
	var status hmapi.CentralPairingStatus
	_ = json.Unmarshal(call(http.MethodGet, "/centrals/pairing/"+started.PairingID+"?wait=5", "", http.StatusOK), &status)
	if status.State != "approved" {
		t.Fatalf("status = %+v, want approved", status)
	}
	call(http.MethodPost, "/centrals", `{"name":"box","host":"`+host+`","json_rpc_port":`+strconv.Itoa(port)+
		`,"system_type":"openccu-lite","enabled":true,"interfaces":[{"name":"HmIP-RF"}],"pairing_id":"`+started.PairingID+`"}`,
		http.StatusCreated)

	for _, b := range bodies {
		if strings.Contains(b, "olt_") {
			t.Errorf("a response carried a token: %s", b)
		}
	}
	row := store.rows["box"]
	if !strings.HasPrefix(row.APITokenPlain, "olt_") {
		t.Fatalf("the stored central has no token: %+v", row)
	}
	c, err := occulited.New(occulited.Config{BaseURL: fake.URL(), Token: row.APITokenPlain})
	if err != nil {
		t.Fatalf("occulited.New: %v", err)
	}
	state, err := c.AuthState(context.Background())
	if err != nil || !state.Authenticated {
		t.Errorf("the stored token is not accepted by the box: %+v, %v", state, err)
	}
	if _, _, err := onboarding.PairingToken(started.PairingID); err == nil {
		t.Error("the pairing session still holds the token after the central took it")
	}
}
