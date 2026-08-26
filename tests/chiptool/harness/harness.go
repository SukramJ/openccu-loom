// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build chiptool

package harness

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

// EnableMDNSDiscoveryEnv is the env var that opt-in enables the
// mDNS-discovery test cluster. Off by default — multicast advertis-
// ing tends to contaminate the host's `_matter._tcp.local` namespace
// and is flaky in CI. Local developer runs that want T1-style cover-
// age set `OPENCCU_LOOM_CHIPTOOL_MDNS=1`.
const EnableMDNSDiscoveryEnv = "OPENCCU_LOOM_CHIPTOOL_MDNS"

// AdminUser / AdminPass are the in-memory REST credentials baked
// into the harness's daemon config. Tests that hit the REST surface
// (matter/status, matter/fabrics, matter/commissioning/window) read
// these via [Bridge.AuthHeader].
const (
	AdminUser = "chiptool-admin"
	AdminPass = "chiptool-admin-pw" //nolint:gosec // test-only literal
)

// BridgeNodeID / BridgeFabricID are the operational node + fabric
// identifiers the bridge exposes when CASE is wired. Distinct from
// [SharedFabricNodeID] (the chip-tool controller's node ID) so the
// bridge and the controller stay addressable separately.
const (
	BridgeNodeID   uint64 = 0xCAFE
	BridgeFabricID uint64 = 0xBEEF
)

// Bridge is the harness handle for the running openccu-loom daemon
// plus its godevccu southbound. Tests share one Bridge across the
// suite — see [tests/chiptool/main_test.go] for the lifecycle.
type Bridge struct {
	t *testing.T

	chipBin string

	cmd     *exec.Cmd
	cmdDone chan struct{}
	cmdErr  error
	stdout  *syncBuf
	stderr  *syncBuf

	dataDir string
	cfgPath string

	restAddr     string // 127.0.0.1:<port>
	matterAddr   string // 127.0.0.1:<port>
	matterPort   int
	callbackPort int
	binPort      int

	// MDNS records the advertise mode the bridge was brought up
	// under — exposed so the opt-in mDNS test can sanity-check that
	// the env knob was honoured.
	MDNS string

	// CCU is the southbound godevccu simulator. Tests reach for it
	// to inject events directly (e.g. value changes that fan out
	// through the Matter subscription).
	CCU *MockCCU

	// SharedCtl is the shared chip-tool controller commissioned in
	// TestMain. Read-only tests grab it from here; isolated tests
	// build their own via [NewController].
	SharedCtl *Controller

	stopOnce sync.Once
}

// Options configures the bridge. All zero-value fields pick sane
// defaults.
type Options struct {
	// Devices overrides the godevccu fleet. Empty = [DefaultDevices].
	Devices []string

	// EnableMDNS picks the matter mDNS advertiser. When true we use
	// `zeroconf`; default and chip-tool's opt-in are routed through
	// the env var, not this struct field directly.
	EnableMDNS bool

	// CASEEnabled flips on the CASE responder so post-PASE
	// pairing-attempt-with-`--pase-only false` succeeds. Required for
	// the shared fabric the suite reads from.
	CASEEnabled bool

	// DevRotateUniqueIDs surfaces the `north.matter.dev_rotate_unique_ids`
	// knob. Needed by lifecycle_test.go's bootid-rotation case.
	DevRotateUniqueIDs bool

	// ExposeSecondaryChannels surfaces the
	// `north.matter.expose_secondary_channels` expert knob. Off (default)
	// keeps one Matter endpoint per physical device; on additionally exposes
	// a custom entity's secondary actor channels and its group-STATE channel.
	ExposeSecondaryChannels bool

	// StartupDeadline overrides the default 30 s wait for the
	// /api/v1/health endpoint to start returning 200.
	StartupDeadline time.Duration
}

// StartShared is the [Start] variant whose lifecycle is governed by
// `TestMain` rather than a single test. Returns the bridge + a
// cleanup callback the caller MUST run before `os.Exit(m.Run())`.
// Used to spin up the suite-wide bridge once and tear it down only
// when the whole test binary exits.
//
// The daemon binary is resolved against the same env vars [Start]
// honours ([DaemonBinaryEnv]); when neither the env nor
// ./bin/openccu-loom is usable the function returns an error rather
// than calling t.Fatalf (no testing.T is available here).
func StartShared(chipBin string, opts Options) (*Bridge, func(), error) {
	return startCommon(nil, chipBin, opts)
}

// Start brings up godevccu and the openccu-loom daemon, configured
// for Matter against an in-process simulator. Returns the Bridge
// handle. A t.Cleanup is registered to stop everything.
func Start(t *testing.T, chipBin string, opts Options) *Bridge {
	t.Helper()
	b, cleanup, err := startCommon(t, chipBin, opts)
	if err != nil {
		t.Fatalf("bridge bring-up: %v", err)
	}
	t.Cleanup(cleanup)
	return b
}

// startCommon is the shared bring-up path used by both Start (with
// a t.Cleanup hook) and StartShared (with a returned callback). When
// t is non-nil the function fast-fails via t.Fatalf; when nil it
// surfaces errors via the returned error. Either way the bridge
// handle and a cleanup function are produced.
func startCommon(t *testing.T, chipBin string, opts Options) (*Bridge, func(), error) {
	b := &Bridge{
		t:       t,
		chipBin: chipBin,
		stdout:  newSyncBuf(),
		stderr:  newSyncBuf(),
	}

	daemonBin, err := resolveDaemonBinary(t)
	if err != nil {
		return nil, nil, err
	}

	ccu, ccuStop, err := startMockCCUShared(opts.Devices)
	if err != nil {
		return nil, nil, fmt.Errorf("godevccu: %w", err)
	}
	b.CCU = ccu

	// Track per-resource cleanups so an error mid-bring-up can roll
	// back any partial progress.
	cleanups := []func(){ccuStop}
	rollback := func() {
		for i := len(cleanups) - 1; i >= 0; i-- {
			cleanups[i]()
		}
	}

	// Pre-allocate every loopback port we hand the daemon. The TCP
	// dance is the standard "Listen→Addr→Close" trick (small TOCTOU
	// window). The matter UDP port uses the UDP-equivalent helper.
	restPort, err := pickFreeTCPPortNoT()
	if err != nil {
		rollback()
		return nil, nil, fmt.Errorf("pick REST port: %w", err)
	}
	if b.callbackPort, err = pickFreeTCPPortNoT(); err != nil {
		rollback()
		return nil, nil, fmt.Errorf("pick callback port: %w", err)
	}
	if b.binPort, err = pickFreeTCPPortNoT(); err != nil {
		rollback()
		return nil, nil, fmt.Errorf("pick bin port: %w", err)
	}
	if b.matterPort, err = pickFreeUDPPortNoT(); err != nil {
		rollback()
		return nil, nil, fmt.Errorf("pick matter UDP port: %w", err)
	}
	b.restAddr = fmt.Sprintf("127.0.0.1:%d", restPort)
	// Matter binds to [::]:<port> (dual-stack). chip-tool's
	// post-CASE operational mDNS resolution rejects non-link-local
	// addresses, so the bridge must advertise on a real interface
	// with fe80:: rather than just loopback; binding dual-stack and
	// letting the kernel pick interfaces lets `grandcat/zeroconf`
	// publish the host's actual fe80:: addresses.
	b.matterAddr = fmt.Sprintf("[::]:%d", b.matterPort)

	// zeroconf advertisement is REQUIRED for chip-tool's post-CASE
	// operational discovery — without it the controller commissions
	// successfully but then deadlocks in FindOperationalForStayActive
	// trying to resolve the bridge's `_matter._tcp` record.
	mdns := "zeroconf"
	b.MDNS = mdns
	// EnableMDNS / EnableMDNSDiscoveryEnv are retained as no-op API
	// hooks; flipping them does not change behaviour now that the
	// suite always advertises.
	_ = opts.EnableMDNS

	dataDir, err := makeDataDir(t)
	if err != nil {
		rollback()
		return nil, nil, fmt.Errorf("data dir: %w", err)
	}
	cleanups = append(cleanups, func() { _ = os.RemoveAll(dataDir) })
	b.dataDir = dataDir
	b.cfgPath = filepath.Join(b.dataDir, "config.yaml")

	cfgYAML := buildChipToolConfigYAML(chipToolConfigInputs{
		DataDir:                 b.dataDir,
		RESTListen:              fmt.Sprintf(":%d", restPort),
		CallbackPort:            b.callbackPort,
		BinPort:                 b.binPort,
		MatterListen:            b.matterAddr,
		MatterMDNS:              mdns,
		MatterPasscode:          ChipDefaultPasscode,
		MatterDiscriminator:     ChipDefaultDiscriminator,
		CASEEnabled:             opts.CASEEnabled,
		BridgeNodeID:            BridgeNodeID,
		BridgeFabricID:          BridgeFabricID,
		DevRotateUniqueIDs:      opts.DevRotateUniqueIDs,
		ExposeSecondaryChannels: opts.ExposeSecondaryChannels,
		CCUHost:                 "127.0.0.1",
		CCUXMLRPC:               b.CCU.XMLRPCPort(),
		CCUJSONRPC:              b.CCU.JSONRPCPort(),
	})
	if err := os.WriteFile(b.cfgPath, []byte(cfgYAML), 0o600); err != nil {
		rollback()
		return nil, nil, fmt.Errorf("write config.yaml: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	b.cmd = exec.CommandContext(ctx, daemonBin, "run", "--config", b.cfgPath)
	b.cmd.Stdout = b.stdout
	b.cmd.Stderr = b.stderr
	if runtime.GOOS != "windows" {
		b.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	if err := b.cmd.Start(); err != nil {
		cancel()
		rollback()
		return nil, nil, fmt.Errorf("start openccu-loom: %w", err)
	}
	b.cmdDone = make(chan struct{})
	go func() {
		b.cmdErr = b.cmd.Wait()
		close(b.cmdDone)
		cancel()
	}()
	cleanups = append(cleanups, b.Stop)

	deadline := opts.StartupDeadline
	if deadline == 0 {
		deadline = 30 * time.Second
	}
	if err := b.waitForHealth(deadline); err != nil {
		if t != nil {
			t.Logf("daemon stdout:\n%s", b.stdout.String())
			t.Logf("daemon stderr:\n%s", b.stderr.String())
		}
		rollback()
		return nil, nil, fmt.Errorf("daemon health timeout (%s): %w", deadline, err)
	}
	if err := b.waitForMatterListening(15 * time.Second); err != nil {
		if t != nil {
			t.Logf("daemon stdout:\n%s", b.stdout.String())
			t.Logf("daemon stderr:\n%s", b.stderr.String())
		}
		rollback()
		return nil, nil, fmt.Errorf("matter listener timeout: %w", err)
	}

	return b, rollback, nil
}

// resolveDaemonBinary mirrors [RequireDaemonBinary] but returns an
// error instead of calling t.Fatalf, so [StartShared] (no
// testing.T) can use it. When t is non-nil and the binary is
// missing, falls through to t.Fatalf for compatibility.
func resolveDaemonBinary(t *testing.T) (string, error) {
	if p := os.Getenv(DaemonBinaryEnv); p != "" {
		if _, err := os.Stat(p); err != nil {
			return "", fmt.Errorf("%s=%q: %w", DaemonBinaryEnv, p, err)
		}
		return p, nil
	}
	_, thisFile, _, _ := runtime.Caller(0)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", ".."))
	bin := filepath.Join(repoRoot, "bin", "openccu-loom")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	if _, err := os.Stat(bin); err != nil {
		return "", fmt.Errorf("openccu-loom binary not found at %s: %w (run `make build` or `make chiptool-test`; or set %s)", bin, err, DaemonBinaryEnv)
	}
	return bin, nil
}

// makeDataDir creates the daemon's data directory. When a testing.T
// is available it uses t.TempDir (per-test cleanup); otherwise it
// creates an os.MkdirTemp the caller cleans up via the returned
// cleanup callback in startCommon.
func makeDataDir(t *testing.T) (string, error) {
	if t != nil {
		return t.TempDir(), nil
	}
	return os.MkdirTemp("", "openccu-loom-chiptool-")
}

// Stop signals the daemon, waits for the process to exit, and
// dumps captured output on test failure. Idempotent.
func (b *Bridge) Stop() {
	if b == nil {
		return
	}
	b.stopOnce.Do(func() {
		if b.cmd != nil && b.cmd.Process != nil {
			_ = b.cmd.Process.Signal(syscall.SIGTERM)
			select {
			case <-b.cmdDone:
			case <-time.After(5 * time.Second):
				_ = b.cmd.Process.Kill()
				<-b.cmdDone
			}
		}
		if b.t != nil && b.t.Failed() {
			b.t.Logf("daemon stdout:\n%s", b.stdout.String())
			b.t.Logf("daemon stderr:\n%s", b.stderr.String())
		}
	})
}

// Restart stops the daemon, waits for it to exit, and starts it
// again with the same config. The matter UDP port is reused; the
// pre-allocated socket is long gone but the daemon's bind has the
// SO_REUSEADDR-equivalent the listener configures. Used by
// lifecycle_test.go for bootid-rotation + CASE-pickup cases.
func (b *Bridge) Restart(t *testing.T) {
	t.Helper()
	// Tear down only the process, NOT the cleanup hook (the hook
	// still needs to run after the restarted daemon dies at the end
	// of the suite).
	if b.cmd != nil && b.cmd.Process != nil {
		_ = b.cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-b.cmdDone:
		case <-time.After(5 * time.Second):
			_ = b.cmd.Process.Kill()
			<-b.cmdDone
		}
	}

	daemonBin := RequireDaemonBinary(t)
	ctx, cancel := context.WithCancel(context.Background())
	b.stdout = newSyncBuf()
	b.stderr = newSyncBuf()
	b.cmd = exec.CommandContext(ctx, daemonBin, "run", "--config", b.cfgPath)
	b.cmd.Stdout = b.stdout
	b.cmd.Stderr = b.stderr
	if runtime.GOOS != "windows" {
		b.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	if err := b.cmd.Start(); err != nil {
		cancel()
		t.Fatalf("restart openccu-loom: %v", err)
	}
	b.cmdDone = make(chan struct{})
	b.stopOnce = sync.Once{}
	go func() {
		b.cmdErr = b.cmd.Wait()
		close(b.cmdDone)
		cancel()
	}()
	if err := b.waitForHealth(30 * time.Second); err != nil {
		t.Logf("daemon stdout (restart):\n%s", b.stdout.String())
		t.Fatalf("restarted daemon did not become healthy: %v", err)
	}
	if err := b.waitForMatterListening(15 * time.Second); err != nil {
		t.Fatalf("restarted matter bridge not listening: %v", err)
	}
}

// MatterAddr returns the bridge's UDP listen address as
// `127.0.0.1:<port>`.
func (b *Bridge) MatterAddr() string { return b.matterAddr }

// MatterPort returns just the UDP port.
func (b *Bridge) MatterPort() int { return b.matterPort }

// RESTBase returns the REST base URL, e.g. "http://127.0.0.1:53122".
func (b *Bridge) RESTBase() string { return "http://" + b.restAddr }

// AuthHeader returns the HTTP Basic Authorization header value the
// harness signs REST calls with. Exposed so a test can build a
// custom http.Request when the canonical helpers don't fit.
func (b *Bridge) AuthHeader() string {
	creds := base64.StdEncoding.EncodeToString([]byte(AdminUser + ":" + AdminPass))
	return "Basic " + creds
}

// MatterStatus queries GET /api/v1/matter/status and returns the
// parsed body. The harness uses it during bring-up; tests use it
// to assert on listening/advertising state.
type MatterStatusResponse struct {
	Enabled        bool   `json:"enabled"`
	Listening      bool   `json:"listening"`
	ListenAddr     string `json:"listen_addr"`
	EndpointCount  int    `json:"endpoint_count"`
	FabricCount    int    `json:"fabric_count"`
	EnabledCount   int    `json:"enabled_count"`
	Advertising    bool   `json:"advertising"`
	WindowOpen     bool   `json:"commissioning_window_open"`
	WindowDuration uint16 `json:"commissioning_window_duration_seconds,omitempty"`
}

// MatterStatus is the typed REST helper. Returns the parsed body
// on success.
func (b *Bridge) MatterStatus(t *testing.T) MatterStatusResponse {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, b.RESTBase()+"/api/v1/matter/status", nil)
	if err != nil {
		t.Fatalf("build matter/status request: %v", err)
	}
	req.Header.Set("Authorization", b.AuthHeader())
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("matter/status: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("matter/status: status=%d body=%s", resp.StatusCode, raw)
	}
	var body MatterStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode matter/status: %v", err)
	}
	return body
}

// RESTPost POSTs the given body to the daemon and returns the
// response body. Used by the commissioning-window test.
func (b *Bridge) RESTPost(t *testing.T, path string, payload any) ([]byte, int) {
	t.Helper()
	var buf bytes.Buffer
	if payload != nil {
		if err := json.NewEncoder(&buf).Encode(payload); err != nil {
			t.Fatalf("encode payload: %v", err)
		}
	}
	req, err := http.NewRequest(http.MethodPost, b.RESTBase()+path, &buf)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Authorization", b.AuthHeader())
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return body, resp.StatusCode
}

// EnableAllExposures asks the daemon for every mappable Matter
// candidate (`GET /api/v1/matter/exposable`) and bulk-flips them to
// `enabled=true` via the POST /api/v1/matter/exposable/bulk handler.
// The bridge reassembles the topology after the upsert, so the
// suite's read/subscribe tests find non-empty bridged endpoints to
// talk to.
//
// Returns the number of rows it flipped on. Returns an error when
// the GET or POST fails — the caller decides whether to fatal.
func (b *Bridge) EnableAllExposures(ctx context.Context) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		b.RESTBase()+"/api/v1/matter/exposable", nil)
	if err != nil {
		return 0, fmt.Errorf("build list request: %w", err)
	}
	req.Header.Set("Authorization", b.AuthHeader())
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return 0, fmt.Errorf("GET /matter/exposable: %w", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("GET /matter/exposable: status=%d body=%s", resp.StatusCode, body)
	}

	var list struct {
		Items []struct {
			CentralName   string `json:"central_name"`
			DeviceAddress string `json:"device_address"`
			ChannelNo     int    `json:"channel_no"`
			DPKind        string `json:"dp_kind"`
			DPKey         string `json:"dp_key"`
			Enabled       bool   `json:"enabled"`
			Mappable      string `json:"mappable"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return 0, fmt.Errorf("parse exposable list: %w", err)
	}

	type updateItem struct {
		CentralName   string `json:"central_name"`
		DeviceAddress string `json:"device_address"`
		ChannelNo     int    `json:"channel_no"`
		DPKind        string `json:"dp_kind"`
		DPKey         string `json:"dp_key"`
		Enabled       bool   `json:"enabled"`
	}
	var items []updateItem
	for _, c := range list.Items {
		if c.Enabled {
			continue
		}
		// Flip on mappable AND partially_mappable. `unmappable` rows
		// have no Matter-side cluster mapping; flipping them would
		// just produce 400 on the next reassemble.
		if c.Mappable == "unmappable" {
			continue
		}
		items = append(items, updateItem{
			CentralName:   c.CentralName,
			DeviceAddress: c.DeviceAddress,
			ChannelNo:     c.ChannelNo,
			DPKind:        c.DPKind,
			DPKey:         c.DPKey,
			Enabled:       true,
		})
	}
	if len(items) == 0 {
		return 0, nil
	}

	payload, err := json.Marshal(map[string]any{"items": items})
	if err != nil {
		return 0, fmt.Errorf("marshal bulk: %w", err)
	}
	postReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		b.RESTBase()+"/api/v1/matter/exposable/bulk", bytes.NewReader(payload))
	if err != nil {
		return 0, fmt.Errorf("build bulk request: %w", err)
	}
	postReq.Header.Set("Authorization", b.AuthHeader())
	postReq.Header.Set("Content-Type", "application/json")
	postResp, err := (&http.Client{Timeout: 10 * time.Second}).Do(postReq)
	if err != nil {
		return 0, fmt.Errorf("POST bulk: %w", err)
	}
	defer postResp.Body.Close()
	if postResp.StatusCode < 200 || postResp.StatusCode >= 300 {
		raw, _ := io.ReadAll(postResp.Body)
		return 0, fmt.Errorf("POST bulk: status=%d body=%s", postResp.StatusCode, raw)
	}
	return len(items), nil
}

// RESTGet GETs a JSON body and decodes it into `into`. Used by the
// matter/exposable + matter/fabrics tests.
func (b *Bridge) RESTGet(t *testing.T, path string, into any) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, b.RESTBase()+path, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Authorization", b.AuthHeader())
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	if into != nil && resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(into); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
	}
	return resp.StatusCode
}

// Stdout returns a snapshot of the daemon's captured stdout buffer.
// Tests use it to look for log markers (e.g. PASE handshake
// completion) when chip-tool's own output is too coarse.
func (b *Bridge) Stdout() string { return b.stdout.String() }

// Stderr returns a snapshot of the daemon's captured stderr buffer.
// Panics and runtime faults land here rather than in the slog
// stream, so failure diagnostics should dump both.
func (b *Bridge) Stderr() string { return b.stderr.String() }

// waitForHealth polls GET /api/v1/health until 200 or deadline.
func (b *Bridge) waitForHealth(deadline time.Duration) error {
	url := b.RESTBase() + "/api/v1/health"
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	expire := time.NewTimer(deadline)
	defer expire.Stop()
	hc := &http.Client{Timeout: 1 * time.Second}
	for {
		req, _ := http.NewRequest(http.MethodGet, url, nil)
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
		case <-b.cmdDone:
			return errors.New("daemon exited before becoming healthy")
		}
	}
}

// waitForMatterListening polls /api/v1/matter/status until
// listening=true or deadline. The matter UDP bind happens after
// health flips green so a tight bring-up sequence still works.
func (b *Bridge) waitForMatterListening(deadline time.Duration) error {
	url := b.RESTBase() + "/api/v1/matter/status"
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	expire := time.NewTimer(deadline)
	defer expire.Stop()
	hc := &http.Client{Timeout: 1 * time.Second}
	for {
		req, _ := http.NewRequest(http.MethodGet, url, nil)
		req.Header.Set("Authorization", b.AuthHeader())
		resp, err := hc.Do(req)
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK && strings.Contains(string(body), `"listening":true`) {
				return nil
			}
		}
		select {
		case <-tick.C:
			continue
		case <-expire.C:
			return errors.New("matter listener not up")
		case <-b.cmdDone:
			return errors.New("daemon exited before matter bridge came up")
		}
	}
}

// chipToolConfigInputs collects every value the chiptool harness
// needs to write a complete daemon config.
type chipToolConfigInputs struct {
	DataDir                 string
	RESTListen              string
	CallbackPort            int
	BinPort                 int
	MatterListen            string
	MatterMDNS              string
	MatterPasscode          uint32
	MatterDiscriminator     uint16
	CASEEnabled             bool
	BridgeNodeID            uint64
	BridgeFabricID          uint64
	DevRotateUniqueIDs      bool
	ExposeSecondaryChannels bool
	CCUHost                 string
	CCUXMLRPC               int
	CCUJSONRPC              int
}

// buildChipToolConfigYAML returns a complete openccu-loom config
// wired for the chip-tool suite: REST on a free loopback port (used
// for matter/status + matter/commissioning/window only), Matter on
// a pre-allocated UDP port, godevccu as the single southbound CCU.
//
// The YAML is hand-written rather than marshalled from the
// production config types so a refactor on the production side
// cannot silently change harness behaviour. The schema is documented
// in `example.config.yaml`.
func buildChipToolConfigYAML(in chipToolConfigInputs) string {
	caseNode := uint64(0)
	caseFabric := uint64(0)
	if in.CASEEnabled {
		caseNode = in.BridgeNodeID
		caseFabric = in.BridgeFabricID
	}
	rotate := "false"
	if in.DevRotateUniqueIDs {
		rotate = "true"
	}
	exposeSecondary := "false"
	if in.ExposeSecondaryChannels {
		exposeSecondary = "true"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# generated by tests/chiptool/harness — DO NOT COMMIT\n")
	fmt.Fprintf(&b, "locale: en\n")
	fmt.Fprintf(&b, "data_dir: %q\n", in.DataDir)
	// Debug level: the secure-channel RX paths (PASE/CASE dispatch,
	// reply-send failures, replay drops) log at debug, and the buffer
	// is only ever dumped on a bring-up or commissioning failure.
	fmt.Fprintf(&b, "logging:\n  level: debug\n  format: text\n")
	fmt.Fprintf(&b, "callback:\n  host: 127.0.0.1\n  port: %d\n  bin_port: %d\n", in.CallbackPort, in.BinPort)

	fmt.Fprintf(&b, "north:\n")
	// REST: needed for matter/status + matter/commissioning/window.
	// Auth is HTTP-Basic with the harness's admin credentials.
	fmt.Fprintf(&b, "  rest:\n")
	fmt.Fprintf(&b, "    enabled: true\n")
	fmt.Fprintf(&b, "    listen: %q\n", in.RESTListen)
	fmt.Fprintf(&b, "    auth:\n")
	fmt.Fprintf(&b, "      basic:\n        enabled: true\n")
	fmt.Fprintf(&b, "      session_enabled: false\n")
	fmt.Fprintf(&b, "      users:\n        %s: %q\n", AdminUser, AdminPass)
	fmt.Fprintf(&b, "      tokens: {}\n")
	fmt.Fprintf(&b, "      oidc:\n        enabled: false\n")
	fmt.Fprintf(&b, "  ui:\n    enabled: false\n")
	fmt.Fprintf(&b, "  mqtt:\n    enabled: false\n")

	fmt.Fprintf(&b, "  matter:\n")
	fmt.Fprintf(&b, "    enabled: true\n")
	fmt.Fprintf(&b, "    listen: %q\n", in.MatterListen)
	// IPv6 dual-stack is required for chip-tool to honour the
	// advertised addresses — see [Start] for the full rationale.
	fmt.Fprintf(&b, "    prefer_ipv4: false\n")
	fmt.Fprintf(&b, "    vendor_id: 0xFFF1\n")
	fmt.Fprintf(&b, "    product_id: 0x8001\n")
	fmt.Fprintf(&b, "    node_label: openccu-loom-chiptool\n")
	fmt.Fprintf(&b, "    discriminator: 0x%X\n", in.MatterDiscriminator)
	fmt.Fprintf(&b, "    mdns_advertise: %s\n", in.MatterMDNS)
	fmt.Fprintf(&b, "    commissioning:\n")
	fmt.Fprintf(&b, "      passcode: %d\n", in.MatterPasscode)
	// 16-byte dev salt — base64 of "SPAKE2P_SALT123!" (deterministic).
	fmt.Fprintf(&b, "      salt: \"U1BBS0UyUF9TQUxUMTIzIQ==\"\n")
	fmt.Fprintf(&b, "      iterations: 1000\n")
	fmt.Fprintf(&b, "      concurrent_pairings: false\n")
	fmt.Fprintf(&b, "      ephemeral_window: false\n")
	fmt.Fprintf(&b, "    case:\n")
	fmt.Fprintf(&b, "      node_id: %d\n", caseNode)
	fmt.Fprintf(&b, "      fabric_id: %d\n", caseFabric)
	fmt.Fprintf(&b, "    dev_rotate_unique_ids: %s\n", rotate)
	fmt.Fprintf(&b, "    expose_secondary_channels: %s\n", exposeSecondary)

	fmt.Fprintf(&b, "centrals:\n")
	fmt.Fprintf(&b, "  - name: ccu-chiptool\n")
	fmt.Fprintf(&b, "    host: %s\n", in.CCUHost)
	fmt.Fprintf(&b, "    port: %d\n", in.CCUXMLRPC)
	if in.CCUJSONRPC > 0 {
		fmt.Fprintf(&b, "    json_rpc_port: %d\n", in.CCUJSONRPC)
	}
	fmt.Fprintf(&b, "    username: Admin\n")
	fmt.Fprintf(&b, "    password: \"\"\n")
	fmt.Fprintf(&b, "    interfaces:\n      - HmIP-RF\n")

	return b.String()
}

// syncBuf is a goroutine-safe buffer used for sub-process stdout /
// stderr capture. Mirrors the e2e harness's syncBuffer.
type syncBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func newSyncBuf() *syncBuf { return &syncBuf{} }

func (b *syncBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
