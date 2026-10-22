// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/SukramJ/go-fabric/node"
	"github.com/SukramJ/go-fabric/secure/attestation"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/health"
)

// writePEMCert writes an ECDSA cert (self-signed) and returns the DER bytes +
// file path.
func writePEMCert(t *testing.T, dir string, priv *ecdsa.PrivateKey, name string) (der []byte, path string) {
	t.Helper()
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(42),
		Subject:      pkix.Name{CommonName: name},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}
	path = filepath.Join(dir, name+".pem")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer f.Close()
	if err := pem.Encode(f, &pem.Block{Type: "CERTIFICATE", Bytes: der}); err != nil {
		t.Fatalf("pem.Encode: %v", err)
	}
	return der, path
}

// writePEMKey writes a PKCS#8 private key PEM and returns the path.
func writePEMKey(t *testing.T, dir string, priv *ecdsa.PrivateKey, name string) string {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatalf("MarshalPKCS8: %v", err)
	}
	path := filepath.Join(dir, name+".pem")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer f.Close()
	if err := pem.Encode(f, &pem.Block{Type: "PRIVATE KEY", Bytes: der}); err != nil {
		t.Fatalf("pem.Encode: %v", err)
	}
	return path
}

// mustWrite writes data to path or fails the test.
func mustWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func newP256Key(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return k
}

// startWithAttestation boots the bridge with mutate's attestation files,
// the path a configured chain travels: matterAttestation hands the files to
// the node facade, which loads them (node.Attestation.Files).
func startWithAttestation(t *testing.T, mutate func(a *config.NorthMatterAttestation)) *matterBridgeBundle {
	t.Helper()
	cfg := matterTestConfig(t, "ccu-attestation")
	mutate(&cfg.North.Matter.Attestation)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	bundle := startMatterBridge(ctx, cfg, buildTestRegistry(t, "ccu-attestation"), openTestLoomDB(t), health.NewTracker(), nil, slog.New(slog.DiscardHandler))
	if bundle == nil {
		t.Fatal("startMatterBridge returned nil; a chain that does not load must leave the bridge running")
	}
	t.Cleanup(bundle.stop)
	return bundle
}

// TestMatterAttestationRefusesAPartialChain: a configuration naming only
// some of the attestation files is an error that blocks commissioning, not
// a silent switch to the test chain.
func TestMatterAttestationRefusesAPartialChain(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_, dacPath := writePEMCert(t, dir, newP256Key(t), "dac")
	bundle := startWithAttestation(t, func(a *config.NorthMatterAttestation) { a.DACPath = dacPath })
	if bundle.commissioningBlocked == nil {
		t.Fatal("a chain with only a DAC path left commissioning open, want it blocked")
	}
	if bundle.node.AttestationError() == nil {
		t.Fatal("node.AttestationError is nil for a chain with only a DAC path")
	}
}

// TestMatterAttestationRefusesAKeyThatIsNotTheDACs: the DAC key must be
// the DAC's own, or the node would sign attestation with a key the
// commissioner cannot verify.
func TestMatterAttestationRefusesAKeyThatIsNotTheDACs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dacKey := newP256Key(t)
	_, dacPath := writePEMCert(t, dir, dacKey, "dac")
	_, paiPath := writePEMCert(t, dir, newP256Key(t), "pai")
	keyPath := writePEMKey(t, dir, newP256Key(t), "other-key")
	cdPath := filepath.Join(dir, "cd.der")
	mustWrite(t, cdPath, []byte{0x30, 0x03, 0x02, 0x01, 0x01})
	bundle := startWithAttestation(t, func(a *config.NorthMatterAttestation) {
		a.DACPath, a.DACKeyPath, a.PAIPath, a.CDPath = dacPath, keyPath, paiPath, cdPath
	})
	if !errors.Is(bundle.commissioningBlocked, attestation.ErrKeyMismatch) {
		t.Fatalf("commissioning blocked by %v, want attestation.ErrKeyMismatch", bundle.commissioningBlocked)
	}
}

// TestMatterAttestationServesAConfiguredChain: a consistent DAC / key /
// PAI / CD reaches the node verbatim and leaves commissioning open.
func TestMatterAttestationServesAConfiguredChain(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dacKey := newP256Key(t)
	_, dacPath := writePEMCert(t, dir, dacKey, "dac")
	_, paiPath := writePEMCert(t, dir, newP256Key(t), "pai")
	keyPath := writePEMKey(t, dir, dacKey, "dac-key")
	cdPath := filepath.Join(dir, "cd.der")
	mustWrite(t, cdPath, []byte{0x30, 0x03, 0x02, 0x01, 0x01})
	set := func(a *config.NorthMatterAttestation) {
		a.DACPath, a.DACKeyPath, a.PAIPath, a.CDPath = dacPath, keyPath, paiPath, cdPath
	}
	mc := config.NorthMatter{}
	set(&mc.Attestation)
	got := matterAttestation(mc, slog.New(slog.DiscardHandler))
	if got.Files == nil || *got.Files != (node.AttestationFiles{DACPath: dacPath, DACKeyPath: keyPath, PAIPath: paiPath, CDPath: cdPath}) {
		t.Fatalf("attestation files = %+v, want the configured paths verbatim", got.Files)
	}
	if !got.BlockCommissioningOnError {
		t.Fatal("BlockCommissioningOnError is false: a chain that does not load would fail node.New and take the bridge down")
	}
	if bundle := startWithAttestation(t, set); bundle.commissioningBlocked != nil {
		t.Fatalf("a consistent chain blocked commissioning: %v", bundle.commissioningBlocked)
	}
}
