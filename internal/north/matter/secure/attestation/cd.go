// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package attestation

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"encoding/asn1"
	"fmt"
	"math/big"

	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// CMS / PKCS#7 OIDs (RFC 5652 §3, §5.4) and the digest /
// signature-algorithm OIDs the SignerInfo references.
var (
	oidPKCS7SignedData = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 2}
	oidPKCS7Data       = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 1}
	oidSHA256          = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 1}
	oidECDSAWithSHA256 = asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 3, 2}
)

// BuildTestCertificationDeclaration returns a CMS-SignedData blob
// that wraps a Matter Certification Declaration (Matter §6.3) for
// the given (vendor, product) pair, signed by the CSA Test CMS key
// from Matter 1.1 Spec Appendix F.
//
// Apple Home, Google Home, and chip-tool whitelist the signing key's
// SubjectKeyIdentifier ([TestCMSSignerSKID]) in their CSA Test trust
// store; commissioners verify the CD by looking up that SKID, hashing
// the inner TLV, and checking the embedded ECDSA signature.
//
// The inner CD payload mirrors matter.js's defaults:
//
//	formatVersion       = 1
//	vendorId            = vid
//	productIdArray      = [pid]
//	deviceTypeId        = 22 (Root Node)
//	certificateId       = "CSA00000SWC00000-00"
//	securityLevel       = 0
//	securityInformation = 0
//	versionNumber       = 1
//	certificationType   = 0  (TEST)
//
// Operators that ship a real product replace the CD via
// `north.matter.attestation.cd_path`; the daemon then loads the
// production blob and ignores the embedded test material.
func BuildTestCertificationDeclaration(vid, pid uint16) ([]byte, error) {
	inner, err := encodeCDInnerTLV(vid, pid)
	if err != nil {
		return nil, fmt.Errorf("encode CD inner: %w", err)
	}
	return cmsSign(inner, TestCMSSignerPrivateKey, TestCMSSignerSKID)
}

// encodeCDInnerTLV builds the Matter-TLV encoded CD struct that the
// CMS SignedData wraps. Tag numbers are stable per Matter §6.3.1; do
// not reorder.
func encodeCDInnerTLV(vid, pid uint16) ([]byte, error) {
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutUint(tlv.ContextTag(0), 1)            // FormatVersion
	enc.PutUint(tlv.ContextTag(1), uint64(vid))  // VendorID
	enc.StartArray(tlv.ContextTag(2))            // ProductIDArray
	enc.PutUint(tlv.AnonymousTag(), uint64(pid)) //
	if err := enc.EndContainer(); err != nil {   // end array
		return nil, err
	}
	enc.PutUint(tlv.ContextTag(3), 22)                    // DeviceTypeID (Root Node)
	enc.PutUTF8(tlv.ContextTag(4), "CSA00000SWC00000-00") // CertificateID
	enc.PutUint(tlv.ContextTag(5), 0)                     // SecurityLevel
	enc.PutUint(tlv.ContextTag(6), 0)                     // SecurityInformation
	enc.PutUint(tlv.ContextTag(7), 1)                     // VersionNumber
	enc.PutUint(tlv.ContextTag(8), 0)                     // CertificationType (TEST)
	if err := enc.EndContainer(); err != nil {            // end struct
		return nil, err
	}
	return enc.Bytes()
}

// cmsSign wraps `eContent` in a CMS SignedData ContentInfo, signed by
// `key`, with the SignerInfo identifying the signer via
// SubjectKeyIdentifier (skid). The signature is computed over the
// raw eContent bytes — no signedAttrs are emitted, matching the CD
// layout matter.js produces.
func cmsSign(eContent []byte, key *ecdsa.PrivateKey, skid []byte) ([]byte, error) {
	digest := sha256.Sum256(eContent)
	r, s, err := ecdsa.Sign(rand.Reader, key, digest[:])
	if err != nil {
		return nil, fmt.Errorf("ecdsa sign: %w", err)
	}
	sigDER, err := asn1.Marshal(struct {
		R, S *big.Int
	}{r, s})
	if err != nil {
		return nil, fmt.Errorf("marshal ecdsa sig: %w", err)
	}

	signerInfo := cmsSignerInfo{
		Version:            3,
		SID:                asn1.RawValue{Class: 2, Tag: 0, IsCompound: false, Bytes: skid}, // [0] IMPLICIT
		DigestAlgorithm:    pkix7AlgorithmIdentifier{Algorithm: oidSHA256, Parameters: asn1.RawValue{}},
		SignatureAlgorithm: pkix7AlgorithmIdentifier{Algorithm: oidECDSAWithSHA256, Parameters: asn1.RawValue{}},
		Signature:          sigDER,
	}

	encapContent := cmsEncapContentInfo{
		EContentType: oidPKCS7Data,
		EContent:     asn1.RawValue{Class: 2, Tag: 0, IsCompound: true, Bytes: marshalOctetString(eContent)},
	}

	signed := cmsSignedData{
		Version:          3,
		DigestAlgorithms: []pkix7AlgorithmIdentifier{{Algorithm: oidSHA256, Parameters: asn1.RawValue{}}},
		EncapContentInfo: encapContent,
		SignerInfos:      []cmsSignerInfo{signerInfo},
	}
	signedDER, err := asn1.Marshal(signed)
	if err != nil {
		return nil, fmt.Errorf("marshal SignedData: %w", err)
	}
	contentInfo := cmsContentInfo{
		ContentType: oidPKCS7SignedData,
		Content:     asn1.RawValue{Class: 2, Tag: 0, IsCompound: true, Bytes: signedDER},
	}
	return asn1.Marshal(contentInfo)
}

// marshalOctetString returns the DER-encoded OCTET STRING wrapping
// the eContent bytes. Used inside the explicit [0] eContent context
// tag of EncapsulatedContentInfo.
func marshalOctetString(b []byte) []byte {
	out, _ := asn1.Marshal(b) // []byte → OCTET STRING
	return out
}

// CMS SignedData structures (RFC 5652 §5.1, §10.1).
//
// asn1 struct tags follow Go's `encoding/asn1` conventions; explicit
// context tags are used where RFC 5652 requires them.

type cmsContentInfo struct {
	ContentType asn1.ObjectIdentifier
	Content     asn1.RawValue `asn1:"explicit,tag:0"`
}

type cmsSignedData struct {
	Version          int
	DigestAlgorithms []pkix7AlgorithmIdentifier `asn1:"set"`
	EncapContentInfo cmsEncapContentInfo
	SignerInfos      []cmsSignerInfo `asn1:"set"`
}

type cmsEncapContentInfo struct {
	EContentType asn1.ObjectIdentifier
	EContent     asn1.RawValue `asn1:"explicit,tag:0,optional"`
}

type cmsSignerInfo struct {
	Version            int
	SID                asn1.RawValue
	DigestAlgorithm    pkix7AlgorithmIdentifier
	SignatureAlgorithm pkix7AlgorithmIdentifier
	Signature          []byte
}

// pkix7AlgorithmIdentifier mirrors crypto/x509/pkix.AlgorithmIdentifier
// but tolerates an absent Parameters field — required for the SHA256
// digestAlgorithm where matter.js emits no `NULL` parameter.
type pkix7AlgorithmIdentifier struct {
	Algorithm  asn1.ObjectIdentifier
	Parameters asn1.RawValue `asn1:"optional"`
}
