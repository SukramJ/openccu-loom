// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/config"
)

// TestLoadVendorAttestation_PEMRoundTrip verifies that a fully-
// populated config — DAC + DAC-key + PAI + CD all PEM-encoded — loads
// cleanly and produces a key whose public part matches the cert.
func TestLoadVendorAttestation_PEMRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("priv: %v", err)
	}

	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test DAC"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("certDER: %v", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatalf("keyDER: %v", err)
	}

	dacPath := filepath.Join(dir, "dac.pem")
	keyPath := filepath.Join(dir, "dac.key.pem")
	paiPath := filepath.Join(dir, "pai.pem")
	cdPath := filepath.Join(dir, "cd.bin")

	mustWrite(t, dacPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER}))
	mustWrite(t, keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
	mustWrite(t, paiPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})) // re-use as PAI for the test
	mustWrite(t, cdPath, []byte{0x30, 0x82, 0x01, 0x00, 0x00, 0x01})                           // dummy CMS-like blob

	cfg := config.NorthMatterAttestation{
		DACPath:    dacPath,
		DACKeyPath: keyPath,
		PAIPath:    paiPath,
		CDPath:     cdPath,
	}
	logger := slog.New(slog.DiscardHandler)
	gotKey, gotDAC, gotPAI, gotCD, ok := loadVendorAttestation(cfg, logger)
	if !ok {
		t.Fatal("vendor load returned ok=false on a valid PEM bundle")
	}
	if gotKey == nil || gotKey.D.Cmp(priv.D) != 0 { //nolint:staticcheck // SA1019: D compared directly; test verifies the loader preserved the raw scalar.
		t.Errorf("loaded key does not match supplied private key")
	}
	if len(gotDAC) == 0 || len(gotPAI) == 0 || len(gotCD) == 0 {
		t.Errorf("expected non-empty DAC/PAI/CD bytes; got dac=%d pai=%d cd=%d",
			len(gotDAC), len(gotPAI), len(gotCD))
	}
}

// TestLoadVendorAttestation_MissingPathFalls verifies that any
// missing path → caller falls back to dev attestation, signaled via
// (nil-everything, false).
func TestLoadVendorAttestation_MissingPathFalls(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.DiscardHandler)
	cfg := config.NorthMatterAttestation{} // all paths empty
	_, _, _, _, ok := loadVendorAttestation(cfg, logger)
	if ok {
		t.Error("empty paths should not produce a vendor bundle; want ok=false")
	}
}

// TestLoadVendorAttestation_KeyMismatchFails verifies that a DAC
// cert + unrelated private key trigger the safety check (mismatch
// surfaces before the bundle is shipped to chip-tool).
func TestLoadVendorAttestation_KeyMismatchFails(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	otherPriv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)

	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "mismatch"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		BasicConstraintsValid: true,
	}
	certDER, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	otherKeyDER, _ := x509.MarshalPKCS8PrivateKey(otherPriv)

	dacPath := filepath.Join(dir, "dac.pem")
	keyPath := filepath.Join(dir, "key.pem")
	paiPath := filepath.Join(dir, "pai.pem")
	cdPath := filepath.Join(dir, "cd.bin")
	mustWrite(t, dacPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER}))
	mustWrite(t, keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: otherKeyDER}))
	mustWrite(t, paiPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER}))
	mustWrite(t, cdPath, []byte("cd"))

	cfg := config.NorthMatterAttestation{DACPath: dacPath, DACKeyPath: keyPath, PAIPath: paiPath, CDPath: cdPath}
	logger := slog.New(slog.DiscardHandler)
	if _, _, _, _, ok := loadVendorAttestation(cfg, logger); ok {
		t.Error("DAC/key mismatch should trigger fallback; want ok=false")
	}
}

func mustWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
