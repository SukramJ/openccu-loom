// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package attestation

import (
	"bytes"
	"crypto/x509"
	"crypto/x509/pkix"
	"testing"
)

func TestBuildTestChain_RoundTrip(t *testing.T) {
	t.Parallel()
	chain, err := BuildTestChain(0xFFF1, 0x8001)
	if err != nil {
		t.Fatalf("BuildTestChain: %v", err)
	}

	paaCert, err := x509.ParseCertificate(TestPAAFFF1Cert)
	if err != nil {
		t.Fatalf("parse PAA: %v", err)
	}
	paiCert, err := x509.ParseCertificate(chain.PAI)
	if err != nil {
		t.Fatalf("parse PAI: %v", err)
	}
	dacCert, err := x509.ParseCertificate(chain.DAC)
	if err != nil {
		t.Fatalf("parse DAC: %v", err)
	}

	// PAA → PAI signature.
	if err := paiCert.CheckSignatureFrom(paaCert); err != nil {
		t.Errorf("PAA did not sign PAI: %v", err)
	}
	// PAI → DAC signature.
	if err := dacCert.CheckSignatureFrom(paiCert); err != nil {
		t.Errorf("PAI did not sign DAC: %v", err)
	}

	// PAI is a CA with pathLen 0; DAC is a leaf.
	if !paiCert.IsCA {
		t.Error("PAI must be a CA")
	}
	if !paiCert.MaxPathLenZero {
		t.Error("PAI must encode MaxPathLen=0")
	}
	if dacCert.IsCA {
		t.Error("DAC must not be a CA")
	}

	// AKI / SKID linkage.
	if !bytes.Equal(paiCert.AuthorityKeyId, paaCert.SubjectKeyId) {
		t.Errorf("PAI.AKI != PAA.SKID")
	}
	if !bytes.Equal(dacCert.AuthorityKeyId, paiCert.SubjectKeyId) {
		t.Errorf("DAC.AKI != PAI.SKID")
	}
	if len(paiCert.SubjectKeyId) != 20 || len(dacCert.SubjectKeyId) != 20 {
		t.Error("SKIDs must be 20 bytes (SHA-1 length)")
	}
}

func TestBuildTestChain_DACSubjectCarriesVIDAndPID(t *testing.T) {
	t.Parallel()
	chain, err := BuildTestChain(0xFFF1, 0x8001)
	if err != nil {
		t.Fatalf("BuildTestChain: %v", err)
	}
	dac, err := x509.ParseCertificate(chain.DAC)
	if err != nil {
		t.Fatalf("parse DAC: %v", err)
	}
	gotVID, gotPID := extractVIDPID(t, dac.Subject.Names)
	if gotVID != "FFF1" {
		t.Errorf("DAC subject VID: got %q, want %q", gotVID, "FFF1")
	}
	if gotPID != "8001" {
		t.Errorf("DAC subject PID: got %q, want %q", gotPID, "8001")
	}

	// PAI carries VID only.
	pai, err := x509.ParseCertificate(chain.PAI)
	if err != nil {
		t.Fatalf("parse PAI: %v", err)
	}
	gotPAIVID, gotPAIPID := extractVIDPID(t, pai.Subject.Names)
	if gotPAIVID != "FFF1" {
		t.Errorf("PAI subject VID: got %q", gotPAIVID)
	}
	if gotPAIPID != "" {
		t.Errorf("PAI subject must not carry PID, got %q", gotPAIPID)
	}
}

func TestBuildTestChain_KeyUsage(t *testing.T) {
	t.Parallel()
	chain, err := BuildTestChain(0xFFF1, 0x8001)
	if err != nil {
		t.Fatalf("BuildTestChain: %v", err)
	}
	pai, _ := x509.ParseCertificate(chain.PAI)
	dac, _ := x509.ParseCertificate(chain.DAC)
	if pai.KeyUsage&x509.KeyUsageCertSign == 0 {
		t.Error("PAI missing KeyCertSign")
	}
	if pai.KeyUsage&x509.KeyUsageCRLSign == 0 {
		t.Error("PAI missing CRLSign")
	}
	if dac.KeyUsage&x509.KeyUsageDigitalSignature == 0 {
		t.Error("DAC missing DigitalSignature")
	}
	if dac.KeyUsage&x509.KeyUsageCertSign != 0 {
		t.Error("DAC must not have CertSign")
	}
}

// extractVIDPID pulls the matter-oid-vid / matter-oid-pid attribute
// values out of a parsed Subject DN. Returns ("", "") when neither is
// present.
func extractVIDPID(t *testing.T, names []pkix.AttributeTypeAndValue) (vid, pid string) {
	t.Helper()
	for _, atv := range names {
		switch {
		case atv.Type.Equal(oidMatterVendorID):
			if s, ok := atv.Value.(string); ok {
				vid = s
			}
		case atv.Type.Equal(oidMatterProductID):
			if s, ok := atv.Value.(string); ok {
				pid = s
			}
		}
	}
	return vid, pid
}
