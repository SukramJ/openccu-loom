// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"bytes"
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

	"github.com/SukramJ/go-fabric/secure/attestation"

	"github.com/SukramJ/openccu-loom/internal/config"
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

// TestMatterAttestationRefusesAPartialChain: a configuration naming only
// some of the attestation files is an error, not a silent switch to the
// test chain.
func TestMatterAttestationRefusesAPartialChain(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_, dacPath := writePEMCert(t, dir, newP256Key(t), "dac")
	mc := config.NorthMatter{}
	mc.Attestation.DACPath = dacPath
	if _, err := matterAttestation(mc, slog.New(slog.DiscardHandler)); err == nil {
		t.Fatal("matterAttestation with only a DAC path returned no error, want a refusal")
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
	mc := config.NorthMatter{}
	mc.Attestation.DACPath = dacPath
	mc.Attestation.DACKeyPath = keyPath
	mc.Attestation.PAIPath = paiPath
	mc.Attestation.CDPath = cdPath
	_, err := matterAttestation(mc, slog.New(slog.DiscardHandler))
	if !errors.Is(err, attestation.ErrKeyMismatch) {
		t.Fatalf("matterAttestation with a foreign key = %v, want attestation.ErrKeyMismatch", err)
	}
}

// TestMatterAttestationServesAConfiguredChain: a consistent DAC / key /
// PAI / CD reaches the node verbatim.
func TestMatterAttestationServesAConfiguredChain(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dacKey := newP256Key(t)
	dacDER, dacPath := writePEMCert(t, dir, dacKey, "dac")
	paiDER, paiPath := writePEMCert(t, dir, newP256Key(t), "pai")
	keyPath := writePEMKey(t, dir, dacKey, "dac-key")
	cd := []byte{0x30, 0x03, 0x02, 0x01, 0x01}
	cdPath := filepath.Join(dir, "cd.der")
	mustWrite(t, cdPath, cd)
	mc := config.NorthMatter{}
	mc.Attestation.DACPath = dacPath
	mc.Attestation.DACKeyPath = keyPath
	mc.Attestation.PAIPath = paiPath
	mc.Attestation.CDPath = cdPath
	got, err := matterAttestation(mc, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("matterAttestation: %v", err)
	}
	if got.Chain == nil {
		t.Fatal("matterAttestation returned no chain for a configured one")
	}
	if !bytes.Equal(got.Chain.DAC, dacDER) || !bytes.Equal(got.Chain.PAI, paiDER) {
		t.Fatal("the served DAC / PAI are not the configured certificates")
	}
	if !bytes.Equal(got.CertificationDeclaration, cd) {
		t.Fatalf("CertificationDeclaration = %x, want %x", got.CertificationDeclaration, cd)
	}
}
