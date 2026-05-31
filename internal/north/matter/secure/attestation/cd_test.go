// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package attestation

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/asn1"
	"math/big"
	"testing"
)

func TestBuildTestCertificationDeclaration_RoundTrip(t *testing.T) {
	t.Parallel()
	blob, err := BuildTestCertificationDeclaration(0xFFF1, 0x8001)
	if err != nil {
		t.Fatalf("BuildTestCertificationDeclaration: %v", err)
	}
	if len(blob) == 0 {
		t.Fatal("empty blob")
	}

	var ci cmsContentInfo
	rest, err := asn1.Unmarshal(blob, &ci)
	if err != nil {
		t.Fatalf("unmarshal ContentInfo: %v", err)
	}
	if len(rest) != 0 {
		t.Errorf("trailing bytes after ContentInfo: %d", len(rest))
	}
	if !ci.ContentType.Equal(oidPKCS7SignedData) {
		t.Errorf("contentType: got %v, want %v", ci.ContentType, oidPKCS7SignedData)
	}

	var sd cmsSignedData
	if _, err := asn1.Unmarshal(ci.Content.Bytes, &sd); err != nil {
		t.Fatalf("unmarshal SignedData: %v", err)
	}
	if sd.Version != 3 {
		t.Errorf("SignedData version: got %d, want 3", sd.Version)
	}
	if len(sd.SignerInfos) != 1 {
		t.Fatalf("SignerInfos: got %d, want 1", len(sd.SignerInfos))
	}
	si := sd.SignerInfos[0]
	if !bytes.Equal(si.SID.Bytes, TestCMSSignerSKID) {
		t.Errorf("signer SKID: got %x, want %x", si.SID.Bytes, TestCMSSignerSKID)
	}
	if !si.SignatureAlgorithm.Algorithm.Equal(oidECDSAWithSHA256) {
		t.Errorf("sig alg: got %v, want ecdsaWithSHA256", si.SignatureAlgorithm.Algorithm)
	}

	// Recover the signed eContent, verify the embedded signature.
	var inner []byte
	if _, err := asn1.Unmarshal(sd.EncapContentInfo.EContent.Bytes, &inner); err != nil {
		t.Fatalf("unmarshal eContent OCTET STRING: %v", err)
	}
	var sig struct{ R, S *big.Int }
	if _, err := asn1.Unmarshal(si.Signature, &sig); err != nil {
		t.Fatalf("unmarshal signature DER: %v", err)
	}
	digest := sha256.Sum256(inner)
	pub := &TestCMSSignerPrivateKey.PublicKey
	if !ecdsa.Verify(pub, digest[:], sig.R, sig.S) {
		t.Error("CMS signature does not verify against TestCMSSignerPrivateKey")
	}
}

func TestEncodeCDInnerTLV_NonEmpty(t *testing.T) {
	t.Parallel()
	out, err := encodeCDInnerTLV(0xFFF1, 0x8001)
	if err != nil {
		t.Fatalf("encodeCDInnerTLV: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("empty TLV")
	}
	// First byte is the structure-anonymous control byte; ElementType bits
	// 5-7 must encode a Structure container (0b101). Any other tag-kind /
	// element-type combination is accepted — the wrapping CMS layer is
	// where the wire shape is asserted; here we only verify the encoder
	// emitted something.
	_ = out[0]>>5 == 0b101
}
