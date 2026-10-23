// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	matterbridge "github.com/SukramJ/go-fabric/bridge"
	"github.com/SukramJ/go-fabric/node"
	mattersetup "github.com/SukramJ/go-fabric/secure/setup"
	"github.com/SukramJ/go-fabric/secure/spake2"
	matterstore "github.com/SukramJ/go-fabric/store"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/health"
)

// TestMatterCommissioningCredentialsKeepAConfiguredPasscode: a valid
// configured passcode and salt reach the node verbatim and count as
// configured.
func TestMatterCommissioningCredentialsKeepAConfiguredPasscode(t *testing.T) {
	t.Parallel()
	cm := config.NorthMatterCommissioning{Passcode: 20202021, Salt: "openccu-loom-dev0", Iterations: 1000}
	passcode, salt, iterations, configured, err := matterCommissioningCredentials(cm, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("matterCommissioningCredentials: %v", err)
	}
	if passcode != 20202021 || string(salt) != "openccu-loom-dev0" || iterations != 1000 || !configured {
		t.Fatalf("got passcode=%d salt=%q iterations=%d configured=%v; want the configured values, configured", passcode, salt, iterations, configured)
	}
}

// TestMatterCommissioningCredentialsSubstituteWhatTheFacadeRefuses: the
// facade will not build without a valid passcode and a salt of spake2's
// size, the daemon runs without either — so a missing passcode becomes a
// random valid one that is NOT reported as configured, and a short salt
// becomes a random one of a valid size.
func TestMatterCommissioningCredentialsSubstituteWhatTheFacadeRefuses(t *testing.T) {
	t.Parallel()
	cm := config.NorthMatterCommissioning{Passcode: 0, Salt: "short", Iterations: 1000}
	passcode, salt, _, configured, err := matterCommissioningCredentials(cm, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("matterCommissioningCredentials: %v", err)
	}
	if configured {
		t.Fatal("passcode 0 was reported as configured")
	}
	if !mattersetup.IsValidSetupPIN(passcode) || passcode == 0 {
		t.Fatalf("substitute passcode %d is not a valid setup PIN", passcode)
	}
	if len(salt) < spake2.PBKDFMinSaltSize || len(salt) > spake2.PBKDFMaxSaltSize {
		t.Fatalf("substitute salt is %d bytes, want %d..%d", len(salt), spake2.PBKDFMinSaltSize, spake2.PBKDFMaxSaltSize)
	}
	// A trivially guessable passcode is invalid too, not merely unset.
	cm.Passcode = 11111111
	if _, _, _, configured, err = matterCommissioningCredentials(cm, slog.New(slog.DiscardHandler)); err != nil || configured {
		t.Fatalf("passcode 11111111: configured=%v err=%v; want not configured", configured, err)
	}
}

func TestMatterLocales(t *testing.T) {
	t.Parallel()
	if got := matterLocales("de"); len(got) != 2 || got[0] != "de-DE" || got[1] != "en-US" {
		t.Fatalf("matterLocales(de) = %v, want [de-DE en-US]", got)
	}
	if got := matterLocales("en"); len(got) != 1 || got[0] != "en-US" {
		t.Fatalf("matterLocales(en) = %v, want [en-US]", got)
	}
}

// TestMatterAttestationWithoutVendorMaterialIsTheFacadesTestChain: no
// configured DAC / PAI / CD leaves the facade to build the CSA test chain
// (a zero node.Attestation); the dev self-signed fallback is gone.
func TestMatterAttestationWithoutVendorMaterialIsTheFacadesTestChain(t *testing.T) {
	t.Parallel()
	got := matterAttestation(config.NorthMatter{VendorID: 0xFFF1, ProductID: 0x8000}, slog.New(slog.DiscardHandler))
	if got.Files != nil || got.Chain != nil || got.CertificationDeclaration != nil || got.BlockCommissioningOnError {
		t.Fatalf("matterAttestation without vendor material = %+v, want the zero value", got)
	}
}

// TestMatterFabricEventsRelayToThePublisherAttachedLater: the facade's
// observers are fixed at node.New; the WebSocket publisher is attached
// afterwards and still receives the fabric index.
func TestMatterFabricEventsRelayToThePublisherAttachedLater(t *testing.T) {
	t.Parallel()
	ev := &matterFabricEvents{}
	if err := ev.fabricAdded(node.FabricInfo{FabricIndex: 3}); err != nil {
		t.Fatalf("fabricAdded before attach: %v", err)
	}
	var added, removed []uint8
	ev.set(func(i uint8) { added = append(added, i) }, func(i uint8) { removed = append(removed, i) })
	_ = ev.fabricAdded(node.FabricInfo{FabricIndex: 4})
	_ = ev.fabricRemoved(node.FabricInfo{FabricIndex: 4})
	if len(added) != 1 || added[0] != 4 || len(removed) != 1 || removed[0] != 4 {
		t.Fatalf("added=%v removed=%v, want [4] [4]", added, removed)
	}
}

// TestStartMatterBridgeHandsTheFacadeTheAssemblersGroupManager: the
// endpoints' Groups servers (the assembler's manager) and the facade's
// root GroupKeyManagement plus the bridge's group routing (node.Config.Groups)
// must be one manager — two over the same store diverge in memory and an
// AddGroup on an endpoint is not routed until the next load.
func TestStartMatterBridgeHandsTheFacadeTheAssemblersGroupManager(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.MDNSAdvertise = "noop"
	cfg.North.Matter.Listen = ":0"
	cfg.DataDir = t.TempDir()
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-groups", Host: "127.0.0.1"}}
	reg := buildTestRegistry(t, "ccu-groups")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	bundle := startMatterBridge(ctx, cfg, reg, openTestLoomDB(t), health.NewTracker(), nil, slog.New(slog.DiscardHandler))
	if bundle == nil {
		t.Fatal("startMatterBridge returned nil")
	}
	t.Cleanup(bundle.stop)
	// The daemon supplies no manager, so the node builds the one the
	// assembler is handed; TestStartMatterBridgeMountsGroupsOnBridgedEndpoints
	// pins that an endpoint's AddGroup lands in it.
	if bundle.node.Groups() == nil {
		t.Fatal("the facade runs on no group manager")
	}
	if bundle.node.Aggregator() == nil {
		t.Fatal("the facade built no Aggregator although the daemon syncs its model into it")
	}
	if bundle.opMgr == nil || bundle.subMgr == nil || bundle.opMgr != bundle.node.Sessions() || bundle.subMgr != bundle.node.Subscriptions() {
		t.Fatal("the diagnostics managers on the bundle are not the facade's")
	}
}

// matterTestConfig is an enabled Matter configuration on an ephemeral
// port with the mDNS advertiser off, over one registered central.
func matterTestConfig(t *testing.T, central string) *config.Config {
	t.Helper()
	cfg := config.Default()
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.MDNSAdvertise = "noop"
	cfg.North.Matter.Listen = ":0"
	cfg.DataDir = t.TempDir()
	cfg.Centrals = []config.CentralConfig{{Name: central, Host: "127.0.0.1"}}
	return cfg
}

// basicInformationAttrUniqueID is the BasicInformation UniqueID attribute
// id (Matter core spec §11.1.5.13).
const basicInformationAttrUniqueID uint32 = 0x0012

// inFreshProcess re-runs the calling test alone in a child test process and
// reports whether the caller is that child. The UniqueID salt
// (bootid.EnableRotation, set by any start with dev_rotate_unique_ids) is a
// one-way process-wide switch, so a test that needs the pinned form cannot
// share a process with the dev-rotation starts elsewhere in this package.
// The parent fails when the child fails; the caller returns when this
// reports false.
func inFreshProcess(t *testing.T) bool {
	t.Helper()
	const envKey = "LOOM_FRESH_PROCESS_TEST"
	if os.Getenv(envKey) == t.Name() {
		return true
	}
	cmd := exec.Command(os.Args[0], "-test.run=^"+t.Name()+"$", "-test.count=1") //nolint:gosec // re-executes this very test binary
	cmd.Env = append(os.Environ(), envKey+"="+t.Name())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("fresh-process run of %s failed: %v\n%s", t.Name(), err, lastLines(string(out), 12))
	}
	return false
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// servedUniqueID reads the UniqueID the running root endpoint serves.
func servedUniqueID(t *testing.T, b *matterBridgeBundle) string {
	t.Helper()
	v, ok := b.node.Root().BasicInformation.MatterRead(basicInformationAttrUniqueID)
	if !ok {
		t.Fatal("root BasicInformation does not serve a UniqueID")
	}
	s, ok := v.(string)
	if !ok || s == "" {
		t.Fatalf("root BasicInformation UniqueID = %#v, want a non-empty string", v)
	}
	return s
}

// TestStartMatterBridgeKeepsServingWithABrokenAttestationChain: a
// configured chain that does not load leaves the bridge running for the
// controllers already paired — attestation is checked only while
// commissioning — but blocks new commissioning on the node itself: the
// uncommissioned node opens no window at Start, and opening one is refused
// with go-fabric's ErrCommissioningBlocked (the same gate refuses a paired
// controller's AdministratorCommissioning).
func TestStartMatterBridgeKeepsServingWithABrokenAttestationChain(t *testing.T) {
	t.Parallel()
	cfg := matterTestConfig(t, "ccu-attest-broken")
	cfg.North.Matter.Commissioning.Passcode = 20202021
	cfg.North.Matter.Attestation.DACPath = filepath.Join(t.TempDir(), "missing-dac.pem")
	reg := buildTestRegistry(t, "ccu-attest-broken")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	bundle := startMatterBridge(ctx, cfg, reg, openTestLoomDB(t), health.NewTracker(), nil, slog.New(slog.DiscardHandler))
	if bundle == nil {
		t.Fatal("startMatterBridge refused to start on an unloadable attestation chain, want it serving the paired controllers")
	}
	t.Cleanup(bundle.stop)
	if bundle.commissioningBlocked == nil {
		t.Fatal("bundle.commissioningBlocked is nil on an unloadable attestation chain, want the load error")
	}
	window := bundle.node.CommissioningWindow()
	if window.IsOpen() {
		t.Error("the uncommissioned node opened its own window, want commissioning blocked")
	}
	if err := window.OpenOwnWindow(ctx, 3*time.Minute); !errors.Is(err, matterbridge.ErrCommissioningBlocked) {
		t.Errorf("OpenOwnWindow err = %v, want ErrCommissioningBlocked", err)
	}
}

// TestStartMatterBridgeStartsWithoutAttestationConfig: the same setup with
// no attestation file configured starts on the facade's test chain.
func TestStartMatterBridgeStartsWithoutAttestationConfig(t *testing.T) {
	t.Parallel()
	cfg := matterTestConfig(t, "ccu-attest-none")
	reg := buildTestRegistry(t, "ccu-attest-none")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	bundle := startMatterBridge(ctx, cfg, reg, openTestLoomDB(t), health.NewTracker(), nil, slog.New(slog.DiscardHandler))
	if bundle == nil {
		t.Fatal("startMatterBridge returned nil without an attestation configuration")
	}
	t.Cleanup(bundle.stop)
	// The negative control of the blocked case: without a block the
	// uncommissioned node opens its own window at Start.
	if !bundle.node.CommissioningWindow().IsOpen() {
		t.Error("the uncommissioned node opened no window without an attestation configuration")
	}
}

// TestStartMatterBridgePinsUniqueIDAcrossARename: BasicInformation
// UniqueID has quality F, so a node_label edit between two boots over the
// same database must not re-identify the bridge.
func TestStartMatterBridgePinsUniqueIDAcrossARename(t *testing.T) {
	if !inFreshProcess(t) {
		return
	}
	db := openTestLoomDB(t)
	reg := buildTestRegistry(t, "ccu-uid-rename")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	logger := slog.New(slog.DiscardHandler)

	cfg := matterTestConfig(t, "ccu-uid-rename")
	cfg.North.Matter.NodeLabel = "Bridge One"
	first := startMatterBridge(ctx, cfg, reg, db, health.NewTracker(), nil, logger)
	if first == nil {
		t.Fatal("first startMatterBridge returned nil")
	}
	want := servedUniqueID(t, first)
	first.stop()

	cfg = matterTestConfig(t, "ccu-uid-rename")
	cfg.North.Matter.NodeLabel = "Bridge Two"
	// A daemon restart builds a fresh registry; the manifest of the first
	// one has already declared the readiness seam.
	second := startMatterBridge(ctx, cfg, buildTestRegistry(t, "ccu-uid-rename"), db, health.NewTracker(), nil, logger)
	if second == nil {
		t.Fatal("second startMatterBridge returned nil")
	}
	t.Cleanup(second.stop)
	if got := servedUniqueID(t, second); got != want {
		t.Fatalf("UniqueID after a node_label rename = %q, want the pinned %q", got, want)
	}
}

// TestStartMatterBridgeServesAUniqueIDTheDaemonPinnedBefore: a UniqueID
// stored under the facade's setting key by an earlier daemon release is
// served verbatim — the upgrade path keeps paired controllers' identity.
func TestStartMatterBridgeServesAUniqueIDTheDaemonPinnedBefore(t *testing.T) {
	if !inFreshProcess(t) {
		return
	}
	const pinned = "0123456789ABCDEF0123456789ABCDEF"
	db := openTestLoomDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := matterstore.New(db).SetSetting(ctx, matterstore.SettingUniqueID, pinned); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	cfg := matterTestConfig(t, "ccu-uid-pinned")
	reg := buildTestRegistry(t, "ccu-uid-pinned")
	bundle := startMatterBridge(ctx, cfg, reg, db, health.NewTracker(), nil, slog.New(slog.DiscardHandler))
	if bundle == nil {
		t.Fatal("startMatterBridge returned nil")
	}
	t.Cleanup(bundle.stop)
	if got := servedUniqueID(t, bundle); got != pinned {
		t.Fatalf("served UniqueID = %q, want the previously pinned %q", got, pinned)
	}
}

// fakeMdnsSource is a node's DNS-SD as the diagnostics read it.
type fakeMdnsSource struct {
	enabled  bool
	services []node.MDNSService
	findings []node.MDNSFinding
}

func (f fakeMdnsSource) MDNSEnabled() bool                { return f.enabled }
func (f fakeMdnsSource) MDNSServices() []node.MDNSService { return f.services }
func (f fakeMdnsSource) DiagnoseMDNS() []node.MDNSFinding { return f.findings }

// TestMatterMdnsReporterAdvertisingFollowsTheAdvertiser: the no-op
// advertiser is the explicit "stay quiet" opt-in and reports not
// advertising, without findings; a publishing advertiser reports
// advertising and its findings.
func TestMatterMdnsReporterAdvertisingFollowsTheAdvertiser(t *testing.T) {
	t.Parallel()
	finding := node.MDNSFinding{Code: "probe", Message: "m"}
	quiet := (matterMdnsReporter{node: fakeMdnsSource{findings: []node.MDNSFinding{finding}}}).MatterMdns()
	if quiet.Advertising || len(quiet.Findings) != 0 {
		t.Fatalf("noop advertiser: Advertising=%v findings=%d, want false and none", quiet.Advertising, len(quiet.Findings))
	}
	loud := (matterMdnsReporter{node: fakeMdnsSource{enabled: true, findings: []node.MDNSFinding{finding}}}).MatterMdns()
	if !loud.Advertising || len(loud.Findings) != 1 || loud.Findings[0].Code != "probe" {
		t.Fatalf("publishing advertiser: Advertising=%v findings=%+v, want true and the probe finding", loud.Advertising, loud.Findings)
	}
}

// TestMatterMdnsReporterReadsTheNodesAdvertiser pins the wiring: the
// reporter wired by wireMatterRuntime reads the node facade's advertiser,
// which mdns_advertise=noop leaves quiet.
func TestMatterMdnsReporterReadsTheNodesAdvertiser(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cfg := matterTestConfig(t, "ccu-mdns-report")
	wiring, closers, teardown := wireMatterRuntime(ctx, cfg, buildTestRegistry(t, "ccu-mdns-report"), openTestLoomDB(t), health.NewTracker(), nil, slog.New(slog.DiscardHandler), nil)
	t.Cleanup(func() {
		for _, c := range closers {
			c()
		}
		teardown()
	})
	if wiring.mdnsReporter == nil {
		t.Fatal("wireMatterRuntime wired no mDNS reporter")
	}
	if got := wiring.mdnsReporter.MatterMdns(); got.Advertising {
		t.Fatal("mdns_advertise=noop reported Advertising = true")
	}
}
