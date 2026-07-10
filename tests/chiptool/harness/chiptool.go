// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build chiptool

package harness

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// ChipToolKVSBaseEnv overrides the directory under which the
// harness creates per-controller chip-tool KVS directories. Useful
// when the snap is confined differently, or when running against a
// non-snap chip-tool build.
const ChipToolKVSBaseEnv = "OPENCCU_LOOM_CHIPTOOL_KVS_BASE"

// ChipDefaultPasscode is the chip-tool default Matter passcode the
// suite commissions with. Picked to match the canonical chip-tool
// fixture (also used by `make matter-smoke`) so the same daemon
// config file works for both flows.
const ChipDefaultPasscode = 20202021

// ChipDefaultDiscriminator is the chip-tool default 12-bit Matter
// discriminator (3840 == 0xF00). Same rationale as
// [ChipDefaultPasscode].
const ChipDefaultDiscriminator = 0xF00

// SharedFabricNodeID is the operational node ID the shared
// controller commissions the bridge with. Distinct from the
// bridge's own [Bridge.matterNodeID] so post-commissioning reads
// address the bridge (node ID = bridge), not the controller.
const SharedFabricNodeID = 0x1234

// PairTargetHost is the loopback address tests hand to chip-tool's
// `pairing already-discovered`. IPv6 deliberately: the CSA
// chip-cert-bins builds are ipv6only (INET_CONFIG_ENABLE_IPV4=0) and
// reject IPv4 literals during argument parsing with
// CHIP_ERROR_INVALID_ARGUMENT (0x2F), while every chip-tool build —
// snap or cert-bins — accepts the v6 loopback. The bridge binds
// dual-stack ([::], prefer_ipv4=false in the harness config), so ::1
// always reaches it.
const PairTargetHost = "::1"

// Controller is a chip-tool commissioner identity bound to an
// on-disk KVS directory. Each test that needs an isolated fabric
// owns one Controller; the shared post-commissioning controller
// lives on [Bridge.SharedCtl].
type Controller struct {
	// ChipBin is the resolved chip-tool binary path. Populated by
	// the harness; tests never set it directly.
	ChipBin string

	// StorageDir is the chip-tool KVS root. chip-tool persists the
	// fabric certificates and the operational node identity here.
	// Isolating the directory per controller keeps two controllers
	// on the same machine from trampling each other.
	StorageDir string

	// NodeID is the operational node ID the bridge knows this
	// controller as. The shared controller uses
	// [SharedFabricNodeID]; per-test controllers pick a distinct
	// value to avoid collision with the shared fabric.
	NodeID uint64
}

// NewController builds a fresh Controller backed by a KVS
// directory the snap (or local) chip-tool can write to. NodeID picks
// the operational node ID under which chip-tool commissions the
// bridge.
//
// The directory is created under (in priority order):
//
//  1. $OPENCCU_LOOM_CHIPTOOL_KVS_BASE if set,
//  2. $HOME/snap/chip-tool/common when chipBin is a snap path
//     (Ubuntu snap chip-tool is strict-confined and can only write
//     here),
//  3. t.TempDir() for non-snap chip-tool builds.
//
// Cleanup is registered against t in (2) and (3); the env-override
// case leaves files behind so a CI agent can salvage them.
func NewController(t *testing.T, chipBin string, nodeID uint64) *Controller {
	t.Helper()
	dir, cleanup, err := pickChipToolStorageDir(chipBin)
	if err != nil {
		t.Fatalf("chip-tool KVS dir: %v", err)
	}
	t.Cleanup(cleanup)
	return &Controller{
		ChipBin:    chipBin,
		StorageDir: dir,
		NodeID:     nodeID,
	}
}

// NewControllerShared is the [NewController] variant invoked from
// `TestMain` (no testing.T context). Returns the controller and a
// cleanup callback the caller MUST run before binary exit.
func NewControllerShared(chipBin string, nodeID uint64) (*Controller, func(), error) {
	dir, cleanup, err := pickChipToolStorageDir(chipBin)
	if err != nil {
		return nil, nil, err
	}
	return &Controller{
		ChipBin:    chipBin,
		StorageDir: dir,
		NodeID:     nodeID,
	}, cleanup, nil
}

// pickChipToolStorageDir resolves a write-accessible KVS directory
// for the given chip-tool binary. See [NewController] for the
// priority ladder. The cleanup callback removes the directory.
func pickChipToolStorageDir(chipBin string) (string, func(), error) {
	suffix := randomToken()

	if base := os.Getenv(ChipToolKVSBaseEnv); base != "" {
		dir := filepath.Join(base, "openccu-loom-chiptool-"+suffix)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", nil, fmt.Errorf("create chip-tool KVS dir under %s: %w", ChipToolKVSBaseEnv, err)
		}
		return dir, func() { _ = os.RemoveAll(dir) }, nil
	}

	if strings.HasPrefix(chipBin, "/snap/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", nil, fmt.Errorf("snap chip-tool needs $HOME for KVS; UserHomeDir: %w", err)
		}
		base := filepath.Join(home, "snap", "chip-tool", "common")
		if fi, err := os.Stat(base); err != nil || !fi.IsDir() {
			return "", nil, fmt.Errorf("snap chip-tool KVS base %s missing (run chip-tool once interactively to create it, or set %s)", base, ChipToolKVSBaseEnv)
		}
		dir := filepath.Join(base, "openccu-loom-chiptool-tests", suffix)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", nil, fmt.Errorf("create snap KVS dir: %w", err)
		}
		return dir, func() { _ = os.RemoveAll(dir) }, nil
	}

	dir, err := os.MkdirTemp("", "openccu-loom-chiptool-kvs-")
	if err != nil {
		return "", nil, err
	}
	return dir, func() { _ = os.RemoveAll(dir) }, nil
}

// randomToken returns 16 hex characters from crypto/rand. Used to
// disambiguate concurrent test KVS directories within the same
// snap-common root.
func randomToken() string {
	var raw [8]byte
	_, _ = rand.Read(raw[:])
	return hex.EncodeToString(raw[:])
}

// Run executes chip-tool with the given args under the controller's
// KVS directory and a 90 s wall-clock timeout. Returns the captured
// stdout+stderr (merged), and an error that wraps the exit status when
// chip-tool reports failure. Tests inspect the returned string for
// attribute values, event reports, or status markers via the [parser]
// helpers in the same package.
//
// The ceiling is generous because the heaviest caller — a full
// commissioning (PASE + CASE + attestation + the post-commission
// cluster reads) — can legitimately run tens of seconds on a loaded
// arm64 CI runner amid mDNS resolution and MRP retransmits. A tighter
// 30 s budget killed chip-tool mid-commission before it could either
// finish or surface a real protocol error, turning a slow-but-correct
// run into a timeout failure. Successful fast commands still return
// immediately, so the higher ceiling only affects the failure/hang
// path; callers that deliberately want a short fail-fast ceiling use
// [RunWithTimeout] directly.
func (c *Controller) Run(ctx context.Context, t *testing.T, args ...string) (string, error) {
	t.Helper()
	return c.RunWithTimeout(ctx, t, 90*time.Second, args...)
}

// RunWithTimeout is the [Run] variant that lets the caller pin a
// non-default timeout. Subscribe-Init-only ("priming")
// invocations want a shorter ceiling so a hung session bubbles
// up in seconds, not minutes; pairing reads on a busy host want
// longer.
func (c *Controller) RunWithTimeout(parent context.Context, t *testing.T, timeout time.Duration, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	full := append([]string{}, args...)
	full = append(full, "--storage-directory", c.StorageDir)

	cmd := exec.CommandContext(ctx, c.ChipBin, full...)
	cmd.Env = append(
		os.Environ(),
		// Force coloured TTY off; the parser greps ANSI-stripped lines
		// and the CSV/grep markers chip-tool emits are stable across
		// versions only when the terminal escape codes are suppressed.
		"TERM=dumb",
		"NO_COLOR=1",
	)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	err := cmd.Run()
	out := stripANSI(buf.String())
	if err != nil {
		// Surface a clean diagnostic before bubbling: the raw exit
		// error doesn't include the command, and chip-tool's stdout
		// is the only place that explains why a pair failed.
		if ctx.Err() == context.DeadlineExceeded {
			return out, fmt.Errorf("chip-tool timeout after %s: %w\n--- output ---\n%s", timeout, err, out)
		}
		return out, fmt.Errorf("chip-tool %s: %w\n--- output ---\n%s", strings.Join(args, " "), err, out)
	}
	return out, nil
}

// Pair commissions the bridge over PASE via the chip-tool
// `pairing already-discovered` flow. Returns the captured output
// so tests can also assert on the success marker.
//
//   - addr  → bridge IP (use [PairTargetHost]; ipv6only chip-tool
//     builds reject IPv4 literals)
//   - port  → bridge UDP port (read out of /api/v1/matter/status)
//
// The flow used here matches docs/contributor/matter-smoke.md §3
// (and the v9 capability report's T2): PASE-only,
// `--bypass-attestation-verifier true` because the dev DAC is
// ephemeral.
func (c *Controller) Pair(ctx context.Context, t *testing.T, addr string, port int) (string, error) {
	t.Helper()
	return c.Run(
		ctx, t,
		"pairing", "already-discovered",
		fmt.Sprintf("0x%X", c.NodeID),
		fmt.Sprintf("%d", ChipDefaultPasscode),
		addr, fmt.Sprintf("%d", port),
		"--bypass-attestation-verifier", "true",
		"--pase-only", "true",
	)
}

// PairFull is the [Pair] variant that exercises the full
// PASE→AddNOC→CASE Sigma1 flow (no `--pase-only true`). Used for
// the post-commissioning shared fabric the suite reads from.
func (c *Controller) PairFull(ctx context.Context, t *testing.T, addr string, port int) (string, error) {
	t.Helper()
	return c.Run(
		ctx, t,
		"pairing", "already-discovered",
		fmt.Sprintf("0x%X", c.NodeID),
		fmt.Sprintf("%d", ChipDefaultPasscode),
		addr, fmt.Sprintf("%d", port),
		"--bypass-attestation-verifier", "true",
	)
}

// PairFullWithPasscode is the [PairFull] variant that uses a caller-
// supplied passcode instead of [ChipDefaultPasscode]. Required when
// commissioning into a window opened via REST
// `/api/v1/matter/commissioning/window`, which generates a fresh
// passcode the test must consume from the response body.
func (c *Controller) PairFullWithPasscode(ctx context.Context, t *testing.T, addr string, port int, passcode uint32) (string, error) {
	t.Helper()
	return c.Run(
		ctx, t,
		"pairing", "already-discovered",
		fmt.Sprintf("0x%X", c.NodeID),
		fmt.Sprintf("%d", passcode),
		addr, fmt.Sprintf("%d", port),
		"--bypass-attestation-verifier", "true",
	)
}

// PairFullVerifyAttestation is the [PairFull] variant that exercises
// chip-tool's Device Attestation Verifier. The `--bypass-attestation-
// verifier` flag is omitted, so chip-tool walks the DAC → PAI → PAA
// chain and validates the CD against its compiled-in trust store.
// The bridge must present a chain rooted in a PAA chip-tool already
// trusts (openccu-loom embeds the official CSA Test PAA by default;
// see internal/north/matter/secure/attestation/testpaa.go).
func (c *Controller) PairFullVerifyAttestation(ctx context.Context, t *testing.T, addr string, port int) (string, error) {
	t.Helper()
	return c.Run(
		ctx, t,
		"pairing", "already-discovered",
		fmt.Sprintf("0x%X", c.NodeID),
		fmt.Sprintf("%d", ChipDefaultPasscode),
		addr, fmt.Sprintf("%d", port),
	)
}

// Unpair removes the chip-tool side of the fabric. The bridge-side
// removal is exercised separately via DELETE /api/v1/matter/fabrics/{id}.
func (c *Controller) Unpair(ctx context.Context, t *testing.T) (string, error) {
	t.Helper()
	return c.Run(ctx, t, "pairing", "unpair", fmt.Sprintf("0x%X", c.NodeID))
}

// ReadAttr executes a `read <attribute>` against the given cluster
// and endpoint on the commissioned bridge. `cluster` is the
// chip-tool cluster slug (e.g. "basicinformation"), `attr` is the
// chip-tool attribute slug (e.g. "vendor-id").
func (c *Controller) ReadAttr(ctx context.Context, t *testing.T, cluster, attr string, endpointID uint16) (string, error) {
	t.Helper()
	return c.Run(
		ctx, t,
		cluster, "read", attr,
		fmt.Sprintf("0x%X", c.NodeID),
		fmt.Sprintf("%d", endpointID),
	)
}

// ReadEvent executes a `read-event <event>` against the given
// cluster + endpoint. chip-tool returns either the latest matching
// EventDataIB or an empty EventReports list when the event has not
// fired since the daemon started.
func (c *Controller) ReadEvent(ctx context.Context, t *testing.T, cluster, evt string, endpointID uint16) (string, error) {
	t.Helper()
	return c.Run(
		ctx, t,
		cluster, "read-event", evt,
		fmt.Sprintf("0x%X", c.NodeID),
		fmt.Sprintf("%d", endpointID),
	)
}

// Invoke executes a cluster command. `args` is the chip-tool
// argument list following the command name (most invocations have
// none — e.g. "onoff on").
func (c *Controller) Invoke(ctx context.Context, t *testing.T, cluster, cmd string, endpointID uint16, args ...string) (string, error) {
	t.Helper()
	full := []string{cluster, cmd}
	full = append(full, args...)
	full = append(
		full,
		fmt.Sprintf("0x%X", c.NodeID),
		fmt.Sprintf("%d", endpointID),
	)
	return c.Run(ctx, t, full...)
}

// Subscribe runs a one-shot Subscribe and tears down once chip-tool
// has received the priming Report plus at least one steady-state
// ReportData. minIntervalSec and maxIntervalSec map to chip-tool's
// `--min-interval` / `--max-interval` flags.
func (c *Controller) Subscribe(ctx context.Context, t *testing.T, cluster, attr string, endpointID uint16, minIntervalSec, maxIntervalSec int) (string, error) {
	t.Helper()
	return c.RunWithTimeout(
		ctx, t, 25*time.Second,
		cluster, "subscribe", attr,
		fmt.Sprintf("%d", minIntervalSec),
		fmt.Sprintf("%d", maxIntervalSec),
		fmt.Sprintf("0x%X", c.NodeID),
		fmt.Sprintf("%d", endpointID),
	)
}

// SubscribeEvent is the event-variant of [Subscribe].
func (c *Controller) SubscribeEvent(ctx context.Context, t *testing.T, cluster, evt string, endpointID uint16, minIntervalSec, maxIntervalSec int) (string, error) {
	t.Helper()
	return c.RunWithTimeout(
		ctx, t, 25*time.Second,
		cluster, "subscribe-event", evt,
		fmt.Sprintf("%d", minIntervalSec),
		fmt.Sprintf("%d", maxIntervalSec),
		fmt.Sprintf("0x%X", c.NodeID),
		fmt.Sprintf("%d", endpointID),
	)
}

// Discover runs `chip-tool discover commissionables` for the
// duration timeout. Used only by the opt-in mDNS test gated on
// [EnableMDNSDiscoveryEnv]. Returns the captured output; callers
// grep for the bridge's discriminator/vendor markers.
func (c *Controller) Discover(ctx context.Context, t *testing.T, timeout time.Duration) (string, error) {
	t.Helper()
	return c.RunWithTimeout(
		ctx, t, timeout,
		"discover", "commissionables",
	)
}

// stripANSI returns s with ECMA-48 colour escape sequences removed.
// chip-tool emits `\x1b[1;3?m … \x1b[0m` around log lines; tests
// grep on the stripped text so a CI runner that surprises us by
// reading TERM differently does not break the match.
func stripANSI(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1B && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7E) {
				j++
			}
			if j < len(s) {
				i = j
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// WriteAttr executes a `write <attribute> <value>` against the given
// cluster and endpoint on the commissioned bridge. Value precedes the
// node-id/endpoint tail, mirroring chip-tool's own argv order and the
// same node-id/endpoint tail convention [Controller.ReadAttr] and
// [Controller.Invoke] already use.
func (c *Controller) WriteAttr(ctx context.Context, t *testing.T, cluster, attr, value string, endpointID uint16) (string, error) {
	t.Helper()
	return c.Run(
		ctx, t,
		cluster, "write", attr, value,
		fmt.Sprintf("0x%X", c.NodeID),
		fmt.Sprintf("%d", endpointID),
	)
}

// ReadAttrRaw is a thin passthrough to [Controller.Run] for
// chip-tool invocations that need an argv shape [Controller.ReadAttr]
// does not model — extra flags, wildcard attribute selectors, or any
// command that doesn't fit the single-attribute calling convention.
// Callers assemble the full argument list themselves; ReadAttrRaw
// only supplies the shared timeout + KVS-directory plumbing
// [Controller.Run] already provides.
func (c *Controller) ReadAttrRaw(ctx context.Context, t *testing.T, args ...string) (string, error) {
	t.Helper()
	return c.Run(ctx, t, args...)
}

// SubscribeAndAwait runs a Subscribe against cluster/attr and returns
// as soon as the accumulated, ANSI-stripped output satisfies
// want(out) — or errors out once timeout elapses. Unlike
// [Controller.Subscribe], which waits for chip-tool's own exit before
// returning, this variant scans stdout incrementally so a
// RECEIVE-direction test reacts the moment the daemon actually pushes
// the report a simulated device event triggered, instead of racing a
// fixed sleep window against it.
//
// The subprocess is torn down (context cancellation → chip-tool
// killed) as soon as want() matches or the timeout fires; chip-tool's
// subscribe commands otherwise run until interrupted.
func (c *Controller) SubscribeAndAwait(
	ctx context.Context, t *testing.T,
	cluster, attr string, endpointID uint16,
	minIntervalSec, maxIntervalSec int,
	want func(out string) bool,
	timeout time.Duration,
) (string, error) {
	t.Helper()
	return c.subscribeAndAwait(
		ctx, t, want, timeout,
		cluster, "subscribe", attr,
		fmt.Sprintf("%d", minIntervalSec),
		fmt.Sprintf("%d", maxIntervalSec),
		fmt.Sprintf("0x%X", c.NodeID),
		fmt.Sprintf("%d", endpointID),
	)
}

// SubscribeEventAndAwait is the event-report variant of
// [Controller.SubscribeAndAwait], used for event-driven clusters such
// as GenericSwitch — a button press has no attribute to poll, chip-
// tool only surfaces it through `subscribe-event`.
func (c *Controller) SubscribeEventAndAwait(
	ctx context.Context, t *testing.T,
	cluster, evt string, endpointID uint16,
	minIntervalSec, maxIntervalSec int,
	want func(out string) bool,
	timeout time.Duration,
) (string, error) {
	t.Helper()
	return c.subscribeAndAwait(
		ctx, t, want, timeout,
		cluster, "subscribe-event", evt,
		fmt.Sprintf("%d", minIntervalSec),
		fmt.Sprintf("%d", maxIntervalSec),
		fmt.Sprintf("0x%X", c.NodeID),
		fmt.Sprintf("%d", endpointID),
	)
}

// subscribeAndAwait is the shared implementation behind
// [Controller.SubscribeAndAwait] and [Controller.SubscribeEventAndAwait].
// It runs chip-tool with a pipe-connected, merged stdout+stderr
// stream (mirroring [Controller.Run]'s TERM=dumb / NO_COLOR=1 /
// --storage-directory conventions), scans it line-by-line, and
// returns the moment the ANSI-stripped accumulated output satisfies
// want(). The subprocess is cancelled — not just abandoned — as soon
// as a match lands or the timeout expires, so a caller looping over
// many send/receive cases does not leak one long-lived chip-tool
// process per case.
func (c *Controller) subscribeAndAwait(
	parent context.Context, t *testing.T,
	want func(out string) bool, timeout time.Duration,
	args ...string,
) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	full := append([]string{}, args...)
	full = append(full, "--storage-directory", c.StorageDir)

	cmd := exec.CommandContext(ctx, c.ChipBin, full...)
	cmd.Env = append(os.Environ(), "TERM=dumb", "NO_COLOR=1")

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("stdout pipe: %w", err)
	}
	cmd.Stderr = cmd.Stdout // merge stderr into the same scanned stream

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start chip-tool: %w", err)
	}

	var (
		mu  sync.Mutex
		buf bytes.Buffer
	)
	matched := make(chan struct{})
	scanDone := make(chan struct{})
	var matchOnce sync.Once

	go func() {
		defer close(scanDone)
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			mu.Lock()
			buf.WriteString(scanner.Text())
			buf.WriteByte('\n')
			snapshot := stripANSI(buf.String())
			mu.Unlock()
			if want(snapshot) {
				matchOnce.Do(func() { close(matched) })
				return
			}
		}
	}()

	var runErr error
	select {
	case <-matched:
	case <-scanDone:
		runErr = errors.New("chip-tool exited before the expected report arrived")
	case <-ctx.Done():
		runErr = fmt.Errorf("timeout after %s waiting for expected report", timeout)
	}

	// Stop the subprocess before Wait — in the matched case chip-tool
	// is still running the live subscription and would otherwise
	// block Wait forever.
	cancel()
	_ = cmd.Wait()
	<-scanDone

	mu.Lock()
	out := stripANSI(buf.String())
	mu.Unlock()

	if runErr != nil {
		return out, fmt.Errorf("%w\n--- output ---\n%s", runErr, out)
	}
	return out, nil
}

// SubscribeEventInteractiveAndAwait subscribes to an event inside a chip-tool
// INTERACTIVE session, which — unlike the single-command subscribe-event, a
// one-shot Subscribe-Init that exits before an event fired afterwards arrives —
// stays alive across a device-originated event fired AFTER the subscription
// establishes. It sends the subscribe-event command over stdin, waits for the
// "Subscription established" marker, invokes fire() exactly once to drive the
// event, then scans the live report stream until want() matches or timeout.
//
// This is the RECEIVE primitive for momentary/event-only clusters (GenericSwitch
// button presses), which have no persisted attribute the read-reflection
// AwaitProactiveReport path could observe.
func (c *Controller) SubscribeEventInteractiveAndAwait(
	ctx context.Context, t *testing.T,
	cluster, evt string, endpointID uint16,
	minIntervalSec, maxIntervalSec int,
	fire func() error,
	want func(out string) bool,
	timeout time.Duration,
) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, c.ChipBin, "interactive", "start", "--storage-directory", c.StorageDir)
	cmd.Env = append(os.Environ(), "TERM=dumb", "NO_COLOR=1")

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return "", fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("stdout pipe: %w", err)
	}
	cmd.Stderr = cmd.Stdout // merge stderr into the same scanned stream

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start chip-tool interactive: %w", err)
	}

	// Issue the subscription into the live REPL session.
	if _, err := fmt.Fprintf(stdin, "%s subscribe-event %s %d %d 0x%X %d\n",
		cluster, evt, minIntervalSec, maxIntervalSec, c.NodeID, endpointID); err != nil {
		cancel()
		_ = cmd.Wait()
		return "", fmt.Errorf("write subscribe-event command: %w", err)
	}

	var (
		mu  sync.Mutex
		buf bytes.Buffer
	)
	matched := make(chan struct{})
	scanDone := make(chan struct{})
	var matchOnce, fireOnce sync.Once

	go func() {
		defer close(scanDone)
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			mu.Lock()
			buf.WriteString(scanner.Text())
			buf.WriteByte('\n')
			snapshot := stripANSI(buf.String())
			mu.Unlock()
			// Once the subscription is live, fire the device-originated event
			// exactly once. A transient event's Subscribe-Init report is empty,
			// so a match can only come from the change we drive here.
			if strings.Contains(snapshot, "Subscription established") {
				fireOnce.Do(func() {
					if err := fire(); err != nil {
						t.Logf("SubscribeEventInteractiveAndAwait: fire failed: %v", err)
					}
				})
			}
			if want(snapshot) {
				matchOnce.Do(func() { close(matched) })
				return
			}
		}
	}()

	var runErr error
	select {
	case <-matched:
	case <-scanDone:
		runErr = errors.New("chip-tool interactive exited before the expected event arrived")
	case <-ctx.Done():
		runErr = fmt.Errorf("timeout after %s waiting for expected event", timeout)
	}

	// Best-effort graceful quit, then force-stop the session.
	_, _ = fmt.Fprint(stdin, "quit\n")
	cancel()
	_ = cmd.Wait()
	<-scanDone

	mu.Lock()
	out := stripANSI(buf.String())
	mu.Unlock()

	if runErr != nil {
		return out, fmt.Errorf("%w\n--- output ---\n%s", runErr, out)
	}
	return out, nil
}
