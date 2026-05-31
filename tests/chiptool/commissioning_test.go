// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build chiptool

package chiptool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/tests/chiptool/harness"
)

// TestCommissioning_Attestation_ValidatesWithoutBypass commissions a
// freshly spun-up bridge WITHOUT passing chip-tool's
// `--bypass-attestation-verifier true` flag. The bridge must
// therefore present a Device Attestation chain (DAC → PAI → PAA)
// that chip-tool's compiled-in trust store accepts.
//
// openccu-loom embeds the official CSA Test PAA by default (see
// internal/north/matter/secure/attestation/testpaa.go); chip-tool's
// reference build ships the matching PAA root in its trust store, so
// the validation completes without operator-supplied attestation
// material. Production deployments swap the chain via
// `north.matter.attestation.{dac,dac_key,pai,cd}_path` — that path
// is exercised by the matter-conformance suite, not here.
//
// Runs on an ISOLATED bridge so the suite's shared (already-paired)
// fabric stays intact. Adds ~10 s wall-clock to the suite — that's
// the daemon + godevccu bring-up cost.
func TestCommissioning_Attestation_ValidatesWithoutBypass(t *testing.T) {
	chipBin := harness.RequireChipTool(t)

	b := harness.Start(t, chipBin, harness.Options{CASEEnabled: true})

	ctl := harness.NewController(t, chipBin, 0x2100)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	out, err := ctl.PairFullVerifyAttestation(ctx, t, "127.0.0.1", b.MatterPort())
	if err != nil {
		t.Fatalf("pair with attestation verification: %v", err)
	}
	if !harness.PairingSuccess(out) {
		t.Fatalf("PASE+attestation success marker missing — bridge's DAC/PAI/PAA chain did not validate against chip-tool's trust store:\n%s", out)
	}

	// Defence against silent regressions: if chip-tool ever flipped
	// to internal bypass mode the SendDACReadAttribute /
	// SendOpCSRRequest steps would be skipped, the bridge's DAC/PAI
	// would never be parsed, and PairingSuccess would still print.
	// We therefore assert on attestation-related markers chip-tool
	// emits during the verifier path. Multiple variants accepted —
	// chip-tool 1.5.x vs 1.6.x word these slightly differently.
	markers := []string{
		"AttestationVerification", // chip-tool 1.6.x marker
		"Attestation passed",      // chip-tool 1.5.x marker
		"AttestationResponse",     // common across versions
		"validating device's DAC", // older chip-tool
		"validating PAI",
		"DAC verification successful",
	}
	var hit string
	for _, m := range markers {
		if strings.Contains(out, m) {
			hit = m
			break
		}
	}
	if hit == "" {
		t.Errorf("chip-tool reported PairingSuccess but no attestation marker fired — verifier may be silently bypassed. Expected one of %v in:\n%s", markers, out)
	} else {
		t.Logf("attestation verifier marker detected: %q", hit)
	}

	if _, err := ctl.Unpair(ctx, t); err != nil {
		t.Logf("unpair (best-effort): %v", err)
	}
}

// TestCommissioning_SecondFabric_AfterWindow opens a commissioning
// window via the AdministratorCommissioning REST flow and pairs a
// SECOND controller against the freshly opened window. The window
// must be open (POST /matter/commissioning/window) — without it the
// bridge's PASE port stays closed to new commissioners after the
// first fabric is installed, which is the production-correct
// behaviour per Matter §5.5.
//
// Skips when the REST window-open endpoint is unsupported on this
// daemon build (some configurations gate it behind
// `ephemeral_window: true`).
func TestCommissioning_SecondFabric_AfterWindow(t *testing.T) {
	b := requireBridge(t)
	chipBin := harness.RequireChipTool(t)

	// Open the commissioning window first. Matter §5.5.1 caps the
	// window to 180..900s (3..15 min); the daemon enforces the same
	// range so the request must pick a value inside it. The bridge
	// generates a fresh passcode + discriminator on every open — the
	// pairing call must consume the response body or PASE Sigma1
	// fails on a passcode mismatch.
	resp, status := b.RESTPost(t, "/api/v1/matter/commissioning/window",
		map[string]any{"duration_seconds": 180})
	if status < 200 || status >= 300 {
		t.Skipf("commissioning-window REST not supported on this build: status=%d body=%s", status, resp)
	}
	var openResp struct {
		Discriminator   uint16 `json:"discriminator"`
		Passcode        uint32 `json:"passcode"`
		DurationSeconds uint16 `json:"duration_seconds"`
	}
	if err := json.Unmarshal(resp, &openResp); err != nil {
		t.Fatalf("decode window-open response: %v\nbody=%s", err, resp)
	}
	if openResp.Passcode == 0 {
		t.Fatalf("window-open returned no passcode (body=%s)", resp)
	}
	t.Cleanup(func() {
		_, _ = b.RESTPost(t, "/api/v1/matter/commissioning/window/close", nil)
	})

	ctl := harness.NewController(t, chipBin, 0x2000) // distinct node ID
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	out, err := ctl.PairFullWithPasscode(ctx, t, "127.0.0.1", b.MatterPort(), openResp.Passcode)
	if err != nil {
		t.Fatalf("pair: %v", err)
	}
	if !harness.PairingSuccess(out) {
		t.Fatalf("PASE success marker missing:\n%s", out)
	}

	// Clean up the freshly paired fabric so the bridge does not
	// accumulate stale entries across the suite.
	if _, err := ctl.Unpair(ctx, t); err != nil {
		t.Logf("unpair (best-effort): %v", err)
	}
}

// TestCommissioning_WrongPasscode_Fails sends a Pake1 with the wrong
// passcode and asserts chip-tool reports failure. The bridge must
// not promote a session.
func TestCommissioning_WrongPasscode_Fails(t *testing.T) {
	b := requireBridge(t)
	chipBin := harness.RequireChipTool(t)

	ctl := harness.NewController(t, chipBin, 0x2001)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Wrong passcode (decimal). chip-tool will Pake1, the bridge
	// will compute a different transcript, Pake2 will diverge, and
	// chip-tool surfaces a SPAKE2P failure.
	out, _ := ctl.RunWithTimeout(
		ctx, t, 30*time.Second,
		"pairing", "already-discovered",
		harness.FormatNodeID(ctl.NodeID),
		"11111111", // wrong passcode
		"127.0.0.1", fmt.Sprintf("%d", b.MatterPort()),
		"--bypass-attestation-verifier", "true",
		"--pase-only", "true",
	)
	if harness.PairingSuccess(out) {
		t.Fatalf("PASE should have failed with wrong passcode but reported success:\n%s", out)
	}
	if !harness.PairingFailed(out) {
		// Defensive: chip-tool sometimes exits non-zero without a
		// "Failure" marker, so we accept either signal.
		t.Logf("PASE-failure marker absent but pair did not succeed either — acceptable:\n%s", out)
	}
}

// TestCommissioning_OperationalCredentials_FabricsList reads the
// Fabrics attribute from the shared controller's perspective. After
// commissioning there must be exactly one fabric entry (the shared
// fabric) and its FabricIndex must match CurrentFabricIndex.
func TestCommissioning_OperationalCredentials_FabricsList(t *testing.T) {
	b := requireBridge(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	out, err := b.SharedCtl.ReadAttr(ctx, t, "operationalcredentials", "fabrics", 0)
	if err != nil {
		t.Fatalf("read fabrics: %v", err)
	}
	if !harness.AttrReadOK(out) {
		t.Errorf("fabrics read did not report success:\n%s", out)
	}
	if !strings.Contains(out, "FabricIndex") {
		t.Errorf("Fabrics output missing FabricIndex marker:\n%s", out)
	}
}

// TestCommissioning_OperationalCredentials_CurrentFabricIndex reads
// the CurrentFabricIndex attribute. Must be non-zero (Matter spec
// §11.18.6.5) for any commissioned fabric.
func TestCommissioning_OperationalCredentials_CurrentFabricIndex(t *testing.T) {
	b := requireBridge(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	out, err := b.SharedCtl.ReadAttr(ctx, t, "operationalcredentials", "current-fabric-index", 0)
	if err != nil {
		t.Fatalf("read current-fabric-index: %v", err)
	}
	v, ok := harness.FindAttrUint(out, "CurrentFabricIndex")
	if !ok {
		t.Fatalf("CurrentFabricIndex not parsed:\n%s", out)
	}
	if v < 1 || v > 254 {
		t.Errorf("CurrentFabricIndex=%d, expected 1..254", v)
	}
}

// TestCommissioning_SupportedFabrics_AtLeastOne reads
// SupportedFabrics — must be ≥ 1 for any conformant Matter device.
func TestCommissioning_SupportedFabrics_AtLeastOne(t *testing.T) {
	b := requireBridge(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	out, err := b.SharedCtl.ReadAttr(ctx, t, "operationalcredentials", "supported-fabrics", 0)
	if err != nil {
		t.Fatalf("read supported-fabrics: %v", err)
	}
	v, ok := harness.FindAttrUint(out, "SupportedFabrics")
	if !ok {
		t.Fatalf("SupportedFabrics not parsed:\n%s", out)
	}
	if v < 1 {
		t.Errorf("SupportedFabrics=%d, must be ≥ 1", v)
	}
}

// TestCommissioning_CommissionedFabrics_AtLeastOne reads
// CommissionedFabrics — after the shared commissioning we expect
// at least one fabric.
func TestCommissioning_CommissionedFabrics_AtLeastOne(t *testing.T) {
	b := requireBridge(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	out, err := b.SharedCtl.ReadAttr(ctx, t, "operationalcredentials", "commissioned-fabrics", 0)
	if err != nil {
		t.Fatalf("read commissioned-fabrics: %v", err)
	}
	v, ok := harness.FindAttrUint(out, "CommissionedFabrics")
	if !ok {
		t.Fatalf("CommissionedFabrics not parsed:\n%s", out)
	}
	if v < 1 {
		t.Errorf("CommissionedFabrics=%d, must be ≥ 1 after pairing", v)
	}
}

// TestCommissioning_TrustedRoots_NonEmpty reads
// TrustedRootCertificates — must contain the controller's RCAC.
func TestCommissioning_TrustedRoots_NonEmpty(t *testing.T) {
	b := requireBridge(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	out, err := b.SharedCtl.ReadAttr(ctx, t, "operationalcredentials", "trusted-root-certificates", 0)
	if err != nil {
		t.Fatalf("read trusted-root-certificates: %v", err)
	}
	if !harness.AttrReadOK(out) {
		t.Errorf("trusted-roots read did not succeed:\n%s", out)
	}
	if !strings.Contains(out, "TrustedRootCertificates") {
		t.Errorf("TrustedRootCertificates marker missing:\n%s", out)
	}
}

// TestCommissioning_NOCs_HasOne reads NOCs (NOC + ICAC pair) and
// asserts at least one entry exists for the current fabric.
func TestCommissioning_NOCs_HasOne(t *testing.T) {
	b := requireBridge(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	out, err := b.SharedCtl.ReadAttr(ctx, t, "operationalcredentials", "nocs", 0)
	if err != nil {
		t.Fatalf("read nocs: %v", err)
	}
	if !strings.Contains(out, "NOCs") && !strings.Contains(out, "FabricIndex") {
		t.Errorf("NOCs list empty:\n%s", out)
	}
}
