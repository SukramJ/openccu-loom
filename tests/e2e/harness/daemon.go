// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build e2e

package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// AuthMode selects which authentication backend the harness wires
// into the daemon. The daemon supports them all in production; the
// harness lets each test pick the one it cares about.
type AuthMode int

const (
	// AuthBasic enables HTTP Basic against a single in-memory user.
	AuthBasic AuthMode = iota
	// AuthSession enables form-based login + cookie sessions.
	AuthSession
	// AuthToken enables API-token auth with one admin token.
	AuthToken
	// AuthOIDC wires the daemon's OIDC client to the harness's
	// MockOP. Implies that MockOP is started.
	AuthOIDC
)

// Default credentials baked into every harness daemon.
const (
	AdminUser  = "admin"
	AdminPass  = "harness-admin-pw"
	AdminToken = "harness-admin-token" //nolint:gosec // test-only literal
)

// Options configures the harness. All fields are optional; zero
// values pick safe defaults (godevccu with DefaultDevices, MQTT off,
// AuthSession with admin/admin).
type Options struct {
	// Devices overrides the godevccu fleet. Empty = DefaultDevices.
	Devices []string

	// AuthMode selects the authentication backend.
	AuthMode AuthMode

	// EnableMQTT brings up the embedded MQTT broker and wires the
	// MQTT bridge. Off by default — most tests do not need it.
	EnableMQTT bool

	// EnableMatter is reserved for v1.1; no-op today.
	EnableMatter bool

	// StartupDeadline overrides the default 60 s wait for the
	// /api/v1/health endpoint to start returning 200. When zero, the
	// default applies and OPENCCU_LOOM_E2E_STARTUP_DEADLINE (a Go
	// duration) can override it without a rebuild.
	StartupDeadline time.Duration

	// CheckConnectionInterval overrides the central's background
	// check_connection job cadence. Zero uses the compiled-in default
	// (30 s). A short value (e.g. 5 s) makes degraded-state detection
	// faster in tests that exercise CCU disconnects.
	CheckConnectionInterval time.Duration

	// StartCCUNotReady boots godevccu in its "still warming up" state
	// (JSON-RPC 503, /ise/checkrega.cgi != "OK") so the daemon's
	// readiness-gated southbound bring-up waits before loading devices.
	// Flip it live with h.CCU().V().SetReady(true). The daemon's
	// north-bound surface still comes up immediately, so Start's
	// /api/v1/health wait is unaffected.
	StartCCUNotReady bool

	// PublicURL sets north.rest.public_url — the externally-reachable
	// address an operator configures behind a reverse proxy. Empty is the
	// default deployment, where the daemon reports no Config-UI URL.
	PublicURL string
}

// Harness is the test-owned facade over a running daemon sub-process.
//
// The exposed surface is deliberately minimal: each accessor returns
// a client targeting one external interface. Internal types (the
// CentralRegistry, the EventBus, the SQLite store) are intentionally
// unreachable from tests — E2E is black-box.
type Harness struct {
	t *testing.T

	dataDir string
	cfgPath string

	cmd       *exec.Cmd
	cmdDone   chan struct{} // closed when cmd.Wait returns
	cmdErr    error         // populated by the wait goroutine before close
	stdoutBuf *syncBuffer
	stderrBuf *syncBuffer

	rest *RESTClient
	mqtt MQTTBroker
	op   MockOP
	ccu  *MockCCU

	// Effective listener addresses, populated before Start returns.
	restAddr   string // 127.0.0.1:<port>
	mqttBroker string // tcp://127.0.0.1:<port>, "" if MQTT disabled
	opIssuer   string // http://127.0.0.1:<port>, "" unless AuthOIDC

	stopOnce sync.Once
}

// Start brings up godevccu, the embedded MQTT broker (if enabled),
// the mock OIDC OP (if AuthOIDC), and the openccu-loom daemon, all
// on loopback ephemeral ports. It returns once /api/v1/health
// reports 200.
//
// A t.Cleanup is registered to stop everything in reverse order.
// Test code must not call Stop directly except when exercising
// lifecycle behaviour explicitly.
func Start(t *testing.T, opts Options) *Harness {
	t.Helper()

	h := &Harness{
		t:         t,
		stdoutBuf: newSyncBuffer(),
		stderrBuf: newSyncBuffer(),
	}
	t.Cleanup(h.Stop)

	binPath := locateDaemonBinary(t)

	h.ccu = startMockCCU(t, opts.Devices, opts.StartCCUNotReady)

	if opts.EnableMQTT {
		h.mqtt = startMQTTBroker(t)
		if h.mqtt != nil {
			h.mqttBroker = h.mqtt.URL()
		}
	}
	if opts.AuthMode == AuthOIDC {
		h.op = startMockOP(t)
		if h.op != nil {
			h.opIssuer = h.op.IssuerURL()
		}
	}

	// Every daemon-side port is left to the OS: the daemon binds it and
	// reports what it got, rather than being handed a number a probe
	// listener held open a moment ago.
	//
	// Pre-allocation had a window between the probe's Close and the
	// daemon's Listen roughly a second wide — process spawn, migrations,
	// bring-up — and under a parallel CI run something else on the
	// machine took the port inside it often enough to redden unrelated
	// PRs with "address already in use". No amount of bookkeeping closes
	// that window; not unbinding does.
	//
	// The callback ports need no resolution here at all. Nothing in the
	// harness reads them back, and dynamic callback ports are a
	// supported production mode: the daemon re-advertises the effective
	// port to the CCU on every init() and reconnect. The REST port is
	// the one exception — the test client has to address it — and the
	// daemon logs it.

	h.dataDir = t.TempDir()
	h.cfgPath = filepath.Join(h.dataDir, "config.yaml")
	cfgYAML := buildConfigYAML(configInputs{
		DataDir: h.dataDir,
		// ":0" and port 0 are the daemon's documented dynamic-port
		// modes. The UI shares the REST listener (ADR 0044), so its
		// address is configured but never bound.
		RESTListen: ":0",
		UIListen:   ":0",
		// A wide window, walked until something binds. `port: 0` would
		// mean the default 8120 here, not "let the OS choose".
		CallbackPortRange:       "20000-60000",
		BinPort:                 0,
		AuthMode:                opts.AuthMode,
		MQTTBroker:              h.mqttBroker,
		OIDCIssuer:              h.opIssuer,
		CCUHost:                 "127.0.0.1",
		CCUXMLRPC:               h.ccu.v.XMLRPCAddr().(*net.TCPAddr).Port,
		CCUJSONRPC:              jsonrpcPort(h.ccu),
		CheckConnectionInterval: opts.CheckConnectionInterval,
		PublicURL:               opts.PublicURL,
	})
	if err := os.WriteFile(h.cfgPath, []byte(cfgYAML), 0o600); err != nil {
		t.Fatalf("write config.yaml: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	h.cmd = exec.CommandContext(ctx, binPath, "run", "--config", h.cfgPath)
	h.cmd.Stdout = h.stdoutBuf
	h.cmd.Stderr = h.stderrBuf
	if runtime.GOOS != "windows" {
		// New process group so SIGTERM addresses children too.
		h.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	if err := h.cmd.Start(); err != nil {
		cancel()
		t.Fatalf("start openccu-loom: %v", err)
	}
	h.cmdDone = make(chan struct{})
	go func() {
		h.cmdErr = h.cmd.Wait()
		close(h.cmdDone)
		cancel()
	}()

	// Resolve the REST address from the daemon's own report before
	// anything tries to reach it.
	restAddr, err := awaitRESTAddr(h, opts.StartupDeadline)
	if err != nil {
		t.Logf("daemon stdout:\n%s", h.stdoutBuf.String())
		t.Logf("daemon stderr:\n%s", h.stderrBuf.String())
		t.Fatalf("resolving the daemon's REST address: %v", err)
	}
	h.restAddr = restAddr

	deadline := opts.StartupDeadline
	if deadline == 0 {
		// Default generously: every e2e test spawns its own daemon +
		// godevccu, and under parallel CI load the fleet-loading startup
		// can exceed a tight budget (a loaded runner was observed taking
		// >30s). 60s is still a meaningful "genuinely stuck" bound, not a
		// real performance assertion. OPENCCU_LOOM_E2E_STARTUP_DEADLINE
		// (a Go duration, e.g. "90s") overrides it without a rebuild.
		deadline = 60 * time.Second
		if v := os.Getenv("OPENCCU_LOOM_E2E_STARTUP_DEADLINE"); v != "" {
			if d, err := time.ParseDuration(v); err == nil && d > 0 {
				deadline = d
			}
		}
	}
	if err := waitForHealth(h.t, h.restAddr, deadline, h.cmdDone); err != nil {
		t.Logf("daemon stdout:\n%s", h.stdoutBuf.String())
		t.Logf("daemon stderr:\n%s", h.stderrBuf.String())
		t.Fatalf("daemon did not become healthy within %s: %v", deadline, err)
	}
	return h
}

// Stop signals the daemon, waits for the process to exit, and
// dumps captured output on test failure. Idempotent.
func (h *Harness) Stop() {
	if h == nil {
		return
	}
	h.stopOnce.Do(func() {
		if h.cmd != nil && h.cmd.Process != nil {
			_ = h.cmd.Process.Signal(syscall.SIGTERM)
			select {
			case <-h.cmdDone:
			case <-time.After(5 * time.Second):
				_ = h.cmd.Process.Kill()
				<-h.cmdDone
			}
		}
		if h.t != nil && h.t.Failed() {
			h.t.Logf("daemon stdout:\n%s", h.stdoutBuf.String())
			h.t.Logf("daemon stderr:\n%s", h.stderrBuf.String())
		}
	})
}

// REST returns a client targeting the daemon's REST listener.
func (h *Harness) REST() *RESTClient {
	if h.rest == nil {
		h.rest = newRESTClient("http://" + h.restAddr)
	}
	return h.rest
}

// MQTT returns the embedded broker. Nil if Options.EnableMQTT was
// false.
func (h *Harness) MQTT() MQTTBroker { return h.mqtt }

// OP returns the mock OIDC OP. Nil unless AuthMode == AuthOIDC.
func (h *Harness) OP() MockOP { return h.op }

// CCU returns the godevccu mock — exposed for tests that need to
// inject events directly (e.g. WS-push smoke).
func (h *Harness) CCU() *MockCCU { return h.ccu }

// RESTBase returns the daemon's REST base URL, e.g.
// "http://127.0.0.1:53122".
func (h *Harness) RESTBase() string { return "http://" + h.restAddr }

// UIBase returns the base URL of the server-rendered bootstrap surface
// (login / setup / about / health / OIDC). Since 0.14.0 it is folded onto the
// REST listener (ADR 0044) — there is no separate UI listener anymore — so
// this returns the REST base.
func (h *Harness) UIBase() string { return "http://" + h.restAddr }

// locateDaemonBinary resolves the path to ./bin/openccu-loom relative
// to the repo root. Tests fail with a clear message if the binary
// has not been built yet.
func locateDaemonBinary(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("OPENCCU_LOOM_E2E_BINARY"); p != "" {
		return p
	}
	// repo root = three levels up from this file (tests/e2e/harness/).
	_, thisFile, _, _ := runtime.Caller(0)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", ".."))
	bin := filepath.Join(repoRoot, "bin", "openccu-loom")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("openccu-loom binary not found at %s: %v\n"+
			"run `make build` before `make e2e`, or set OPENCCU_LOOM_E2E_BINARY",
			bin, err)
	}
	return bin
}

// waitForHealth polls GET /api/v1/health until it returns 200 OK or
// the deadline elapses. It also bails out if the daemon process
// exits, so a misconfigured daemon fails fast instead of timing out.
// `exited` is the harness's cmdDone channel and must be closed (not
// sent on) so multiple watchers can observe the same signal.
func waitForHealth(t *testing.T, restAddr string, deadline time.Duration, exited <-chan struct{}) error {
	t.Helper()
	url := "http://" + restAddr + "/api/v1/health"
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	expire := time.NewTimer(deadline)
	defer expire.Stop()

	hc := &http.Client{Timeout: 1 * time.Second}
	for {
		req, _ := http.NewRequest(http.MethodGet, url, nil)
		// Health is mounted unauthenticated; no auth header needed.
		resp, err := hc.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case <-tick.C:
			continue
		case <-expire.C:
			return errors.New("startup deadline elapsed")
		case <-exited:
			return errors.New("daemon exited before becoming healthy")
		}
	}
}

// syncBuffer is a goroutine-safe wrapper around bytes.Buffer used to
// capture sub-process stdout / stderr. exec.Cmd writes to it from
// its own goroutines; t.Logf reads from the test goroutine.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func newSyncBuffer() *syncBuffer { return &syncBuffer{} }

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// awaitRESTAddr reads the daemon's own report of the address its REST
// server bound, from the structured log it writes at start-up.
//
// The daemon is the only thing that knows: it was configured with ":0",
// and the OS chose. Waiting for the line also makes the failure legible
// — a daemon that dies before it binds produces "no rest.listen line"
// with its output attached, rather than a test that hangs against an
// address nothing is listening on.
func awaitRESTAddr(h *Harness, deadline time.Duration) (string, error) {
	if deadline <= 0 {
		deadline = 60 * time.Second
	}
	const poll = 20 * time.Millisecond
	until := time.Now().Add(deadline)
	for {
		if addr := restAddrFrom(h.stdoutBuf.String()); addr != "" {
			return addr, nil
		}
		select {
		case <-h.cmdDone:
			// One last look: the line may have arrived in the same
			// breath as the exit.
			if addr := restAddrFrom(h.stdoutBuf.String()); addr != "" {
				return addr, nil
			}
			return "", errors.New("daemon exited before reporting its REST address")
		default:
		}
		if time.Now().After(until) {
			return "", fmt.Errorf("no rest.listen line within %s", deadline)
		}
		time.Sleep(poll)
	}
}

// restAddrFrom scans structured log output for the REST listener's
// bound address, returning "" until it appears.
//
// The daemon logs `{"msg":"rest.listen","addr":"127.0.0.1:45123",…}`.
// A wildcard bind reports "[::]:45123" or "0.0.0.0:45123"; the test
// client talks to loopback, so only the port is taken from the line.
func restAddrFrom(out string) string {
	for line := range strings.SplitSeq(out, "\n") {
		if !strings.Contains(line, `"rest.listen"`) {
			continue
		}
		var rec struct {
			Msg  string `json:"msg"`
			Addr string `json:"addr"`
		}
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		if rec.Msg != "rest.listen" || rec.Addr == "" {
			continue
		}
		_, port, err := net.SplitHostPort(rec.Addr)
		if err != nil || port == "" || port == "0" {
			continue
		}
		return net.JoinHostPort("127.0.0.1", port)
	}
	return ""
}
