// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mattercert

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/hex"
	"math/big"
	"testing"
)

// TestTBSToDER_AppleHomePairing verifies that an Apple Home Kit
// commissioner's NOC validates against its persisted trust-root
// public key when the signature is checked over the X.509 DER form
// of the TBS (per Matter §6.5 / matter.js Certificate.verifyChain),
// not the Matter-TLV form.
//
// Inputs are the persisted bytes from a real-world Apple Home pairing
// (fabric_index=4 on the developer's machine). Sign-off requires
// `result=true` from ecdsa.Verify(rootKey, sha256(TBS-DER), sig). A
// failure here is the canonical regression for the DER converter.
func TestTBSToDER_AppleHomePairing(t *testing.T) {
	t.Parallel()
	const (
		nocHex  = "1530010101240201370326140ADC88072615909BBFD71826042188903124050037062615909BBFD726116A51830418240701240801300941043D6836B0B86964394EBA333E457F846E7BF5C6F53729E50EEA15CB66241523A2F2E0D1BA73CC4C8435D838AD8776D38D42590883B724D7E0E9BDC8D242456717370A350128011824020136030402040118300414FD505BD807997C683CD3843261571FAD64FF27D3300514E73FCB911EE49D8EF42A3908B2B9C9292A54282318300B40A22F1D9414ACEEDE6D1105DC19A62A22295F6E8AADD686D482AC3E21427F4FDC278605DC4DFDD0B7E723912F7362BA92E00269826CC209E02A2C3765DA9DDE2E18"
		rootHex = "041808BD63CB387CD0A4D72E56B0A2CC1AF6EE6C8F7B5CDDB5B9BBEFFD797A11C7834E1939871FBAA890D078241886EDF97143B75AB827C60D449FDB5EBCCBE44A"
	)

	noc, err := hex.DecodeString(nocHex)
	if err != nil {
		t.Fatalf("decode noc: %v", err)
	}
	rootPub, err := hex.DecodeString(rootHex)
	if err != nil {
		t.Fatalf("decode rootPub: %v", err)
	}

	c, err := Decode(noc)
	if err != nil {
		t.Fatalf("Decode noc: %v", err)
	}

	tbs, err := TBSToDER(c)
	if err != nil {
		t.Fatalf("TBSToDER: %v", err)
	}
	hash := sha256.Sum256(tbs)
	t.Logf("TBS-DER len=%d sha256=%x", len(tbs), hash[:])
	t.Logf("TBS-DER hex=%x", tbs)

	rootKey, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), rootPub)
	if err != nil {
		t.Fatalf("root pubkey off-curve: %v", err)
	}
	r := new(big.Int).SetBytes(c.Signature[:32])
	s := new(big.Int).SetBytes(c.Signature[32:])

	if !ecdsa.Verify(rootKey, hash[:], r, s) {
		t.Fatalf("ecdsa.Verify rejected Apple NOC against persisted root — TBS-DER mismatch")
	}
}
