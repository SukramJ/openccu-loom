// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package attestation builds the Device Attestation chain (PAA → PAI →
// DAC) and the Certification Declaration (CD) the bridge presents to
// commissioners.
//
// The default chain uses the official **CSA Test PAA** vectors from
// the Matter 1.1+ specification (Appendix F + connectedhomeip
// `credentials/test/attestation/`). Apple Home, Google Home, and the
// chip-tool reference commissioner all whitelist this PAA's Subject
// Key Identifier (SKID `6AFD22771F511FECBF1641976710DCDC31A1717E` for
// VID 0xFFF1, `785CE705B86B8F4E6FC793AA60CB43EA696882D5` for the
// VID-less variant). With these PAA bytes embedded, the bridge can
// pair against any commissioner that ships the standard CSA test
// trust store — no `--bypass-attestation-verifier` flag required, no
// vendor-supplied DAC needed.
//
// Operators that ship a real product replace the chain via
// `north.matter.attestation.{dac,dac_key,pai,cd}_path`; the daemon
// then loads the production bundle and ignores the embedded test
// material.
package attestation

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"encoding/hex"
)

// PAA: Matter Test PAA — VID 0xFFF1.
//
// Source: matter.js ChipPAAuthorities.ts (verbatim) — the Matter 1.1
// specification appendix exposes the same key in PEM form. The
// constants below match the X.509 certificate already shipped in
// connectedhomeip's test attestation directory.
//
// Subject:  CN="Matter Test PAA", VendorID=0xFFF1
// Issuer:   self
// Validity: 2021-06-28 → 9999-12-31
// SKID:     6AFD22771F511FECBF1641976710DCDC31A1717E
// SHA256 fingerprint: matches the trust-anchor in chip-tool's
// `credentials/test/attestation/Chip-Test-PAA-FFF1-Cert.der`.
var (
	// TestPAAFFF1Cert is the PAA certificate in X.509 DER encoding,
	// ready to feed into [crypto/x509.ParseCertificate].
	TestPAAFFF1Cert = mustHex(
		"308201bd30820164a00302010202084ea8e83182d41c1c300a06082a8648ce3d040302" +
			"30303118301606035504030c0f4d617474657220546573742050414131143012060a" +
			"2b0601040182a27c02010c04464646313020170d3231303632383134323334335a18" +
			"0f39393939313233313233353935395a30303118301606035504030c0f4d61747465" +
			"7220546573742050414131143012060a2b0601040182a27c02010c04464646313059" +
			"301306072a8648ce3d020106082a8648ce3d03010703420004b6cb6372887f2928f5" +
			"bac81aa9d93ae2431cada9d79e242f65177ef9ced932a28ecd03baaf6a8fca184a1a" +
			"503542960d453f303f1f19421d751e8f8f1a9a9b75a366306430120603551d130101" +
			"ff040830060101ff020101300e0603551d0f0101ff040403020106301d0603551d0e" +
			"041604146afd22771f511fecbf1641976710dcdc31a1717e301f0603551d23041830" +
			"168014" +
			"6afd22771f511fecbf1641976710dcdc31a1717e300a06082a8648ce3d0403020347" +
			"003044022050aa8002f4d932a9a00538f65368ad0fffc8efbbc9beb7da569835cf9a" +
			"a7510e022023bac8fe0f23e75445b65339081a47994929c72aaf0a1548d40d034d51" +
			"4b25de",
	)

	// TestPAAFFF1PrivateKey is the secp256r1 private scalar that signed
	// [TestPAAFFF1Cert]. Embedded as an [ecdsa.PrivateKey] so the
	// PAI/DAC builders can sign certificates with it directly.
	TestPAAFFF1PrivateKey = mustECDSAKey(
		"6512caecaecfc543d60623161597162f014684c565a129b62fd28c27ab1ccc50",
		"04b6cb6372887f2928f5bac81aa9d93ae2431cada9d79e242f65177ef9ced932a2"+
			"8ecd03baaf6a8fca184a1a503542960d453f303f1f19421d751e8f8f1a9a9b75",
	)

	// TestPAAFFF1SKID is the 20-byte Subject Key Identifier of the PAA
	// certificate — used as the AuthorityKeyIdentifier on the PAI we
	// derive from this PAA.
	TestPAAFFF1SKID = mustHex("6afd22771f511fecbf1641976710dcdc31a1717e")
)

// PAA: Matter Test PAA — no Vendor ID. Used when the commissioner
// expects a VID-less root (some early Apple firmware revisions
// shipped only this trust anchor).
var (
	// TestPAANoVIDCert is the X.509 DER for the VID-less Matter Test
	// PAA, signed by its own embedded key.
	TestPAANoVIDCert = mustHex(
		"3082019130820137a00302010202070b8fbaa8dd86ee300a06082a8648ce3d040302" +
			"301a3118301606035504030c0f4d61747465722054657374205041413020170d3231" +
			"303632383134323334335a180f39393939313233313233353935395a301a31183016" +
			"06035504030c0f4d61747465722054657374205041413059301306072a8648ce3d02" +
			"0106082a8648ce3d0301070342000410ef02a81a87b68121fba8d31978f807a317e5" +
			"0aa8a828446828914b933de8edd4a5c39c9ff71a4ce3647fd7f62653b7d2495fcba4" +
			"c0f47f876880039e07204aa366306430120603551d130101ff040830060101ff0201" +
			"01300e0603551d0f0101ff040403020106301d0603551d0e04160414785ce705b86b" +
			"8f4e6fc793aa60cb43ea696882d5301f0603551d23041830168014785ce705b86b8f" +
			"4e6fc793aa60cb43ea696882d5300a06082a8648ce3d0403020348003045022100b9" +
			"efdb3ea06a52ec0bf01e61daed2c2d156ddb6cf014101dab798fac05fa47e5022060" +
			"061d3e35d60d9d4b0d448dad7612f7e85c582e3fc312dc18794dd373715e5d",
	)

	// TestPAANoVIDPrivateKey is the EC private scalar that signed
	// [TestPAANoVIDCert].
	TestPAANoVIDPrivateKey = mustECDSAKey(
		"e1f073c934853baffb38bf7e8bdad7a0a674107c7769892a0ff2e06c1a2ef7a7",
		"0410ef02a81a87b68121fba8d31978f807a317e50aa8a828446828914b933de8ed"+
			"d4a5c39c9ff71a4ce3647fd7f62653b7d2495fcba4c0f47f876880039e07204a",
	)

	// TestPAANoVIDSKID is the SKID of the VID-less Matter Test PAA.
	TestPAANoVIDSKID = mustHex("785ce705b86b8f4e6fc793aa60cb43ea696882d5")
)

// TestCMSSignerPrivateKey is the EC private scalar from Matter 1.1
// Core Specification Appendix F. Commissioners verify the CMS-signed
// CertificationDeclaration against the matching SubjectKeyIdentifier
// in their CSA Test trust store.
//
// PEM form (per spec):
//
//	-----BEGIN EC PRIVATE KEY-----
//	MHcCAQEEIK7zSEEW6UgexXvgRy30G/SZBk5QJK2GnspeiJgC1IB1oAoGCCqGSM49
//	AwEHoUQDQgAEPDmJIkUrVcrzicJb0bykZWlSzLkOiGkkmthHRlMBTL+V1oeWXgNr
//	UhxRA35rjO3vyh60QEZpT6CIgu7WUZ3sug==
//	-----END EC PRIVATE KEY-----
//
// The Appendix-F PEM only documents D; the public key is derived
// here. SKID is checked separately in [TestCMSSignerSKID] tests.
var TestCMSSignerPrivateKey = mustECDSAKey(
	"aef3484116e9481ec57be0472df41bf499064e5024ad869eca5e889802d48075",
	"",
)

// TestCMSSignerSKID is the 20-byte SubjectKeyIdentifier of the
// CMS-signing certificate published in Matter 1.1 Appendix F. The
// signed CD references this SKID; commissioners verify by looking it
// up in their CSA Test trust store.
var TestCMSSignerSKID = mustHex("62fa823359acfaa9963e1cfa140addf504f37160")

// mustHex panics at init time on a malformed hex literal — every
// constant in this file is checked-in source, so a decode failure is a
// build error caught long before any binary ships.
func mustHex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		// invariant: every call site passes a checked-in string literal
		// (package-level var initializers only); the argument is never
		// derived from remote input, so a decode failure here is a
		// typo caught at process start, not something a peer can
		// trigger.
		panic("attestation: malformed hex literal: " + err.Error())
	}
	return b
}

// mustECDSAKey reconstructs a P-256 [ecdsa.PrivateKey] from its hex
// scalar D. The public key is derived from D · G; the optional pubHex
// argument is checked against that derivation so a swapped/typo'd hex
// literal panics at init rather than producing a key whose signatures
// the commissioner silently rejects. Pass `""` to skip the check
// (useful when only the private scalar is documented).
func mustECDSAKey(privHex, pubHex string) *ecdsa.PrivateKey {
	// ParseRawPrivateKey derives the public half and rejects a scalar
	// outside [1, n-1]; PublicKey.Bytes returns the uncompressed
	// (0x04 || X || Y) encoding, which is both the shape pubHex is
	// documented in and the one SHA-1'd for the SKID below.
	priv, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), mustHex(privHex))
	if err != nil {
		// invariant: privHex is a checked-in CSA Test PAA fixture literal
		// (see callers below), never remote input — a scalar this rejects
		// is a transcription error in this file, caught at init on every
		// boot.
		panic("attestation: invalid P-256 private scalar: " + err.Error())
	}
	if pubHex != "" {
		want := mustHex(pubHex)
		if len(want) != 65 || want[0] != 0x04 {
			panic("attestation: malformed uncompressed P-256 point")
		}
		got, err := priv.PublicKey.Bytes()
		if err != nil {
			panic("attestation: cannot encode derived public key: " + err.Error())
		}
		if !bytes.Equal(got, want) {
			// invariant: both operands derive from checked-in literals —
			// a mismatch is a copy-paste error between the two documented
			// fixture values, not a runtime/remote condition.
			panic("attestation: P-256 public key does not match private scalar")
		}
	}
	return priv
}
