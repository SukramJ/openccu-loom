// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mattercert

import (
	"encoding/hex"
	"fmt"
	"testing"
)

// Dumps and parses an Apple NOC captured during a live pair attempt.
// Used for offline analysis of the certificate structure. Run:
//
//	go test ./internal/north/matter/secure/mattercert -run TestDumpAppleNOC -v
//
// Diagnostic test — no assertions.
func TestDumpAppleNOC(t *testing.T) {
	const appleNOCHex = "1530010101240201370326140ADC880726156903C46718260433FD9031240500370626156903C4672611389D8E48182407012408013009410494463D0A3EE3C4F263C725D9BDC3BE89C030568E52DCC56A1155AEA605510DAA2BEE9C83264F2DE9968B22E2ABC68A6DE32542D46E3301977C2C49B7468082F0370A350128011824020136030402040118300414A80B5BF3B11A3C6FBB7C9D3BB6A88C561B47ACCF300514E73FCB911EE49D8EF42A3908B2B9C9292A54282318300B40F955EDDD229218CB68247F7E7D2D309EA82667867523DA08E5ED79992A6FE96A49880FAC9CC99F0705F16F8928419733F1FF329B5BFDF6602C0B14D1EEF7DDF718"
	const appleRCACPubKeyHex = "041808BD63CB387CD0A4D72E56B0A2CC1AF6EE6C8F7B5CDDB5B9BBEFFD797A11C7834E1939871FBAA890D078241886EDF97143B75AB827C60D449FDB5EBCCBE44A"

	raw, err := hex.DecodeString(appleNOCHex)
	if err != nil {
		t.Fatalf("hex: %v", err)
	}
	c, err := Decode(raw)
	if err != nil {
		t.Fatalf("decode NOC: %v", err)
	}
	fmt.Printf("=== Apple NOC (raw=%d bytes) ===\n", len(raw))
	fmt.Printf("SerialNumber       : %x\n", c.SerialNumber)
	fmt.Printf("SignatureAlgorithm : %d\n", c.SignatureAlgorithm)
	fmt.Printf("Issuer (HasFabric=%v fabric=%016X HasRCAC=%v rcac=%016X HasICAC=%v icac=%016X)\n",
		c.Issuer.HasFabricID, c.Issuer.MatterFabricID,
		c.Issuer.HasRCACID, c.Issuer.MatterRCACID,
		c.Issuer.HasICACID, c.Issuer.MatterICACID)
	fmt.Printf("Subject (HasFabric=%v fabric=%016X HasNode=%v node=%016X HasICAC=%v icac=%016X)\n",
		c.Subject.HasFabricID, c.Subject.MatterFabricID,
		c.Subject.HasNodeID, c.Subject.MatterNodeID,
		c.Subject.HasICACID, c.Subject.MatterICACID)
	fmt.Printf("Subject DN order  : %v\n", c.Subject.Order)
	fmt.Printf("Issuer  DN order  : %v\n", c.Issuer.Order)
	fmt.Printf("NotBefore         : %d (matter-epoch sec)\n", c.NotBefore)
	fmt.Printf("NotAfter          : %d (matter-epoch sec; %#x)\n", c.NotAfter, c.NotAfter)
	fmt.Printf("PublicKeyAlgorithm: %d\n", c.PublicKeyAlgorithm)
	fmt.Printf("EllipticCurveID   : %d\n", c.EllipticCurveID)
	fmt.Printf("PublicKey (%d B)  : %x\n", len(c.PublicKey), c.PublicKey)
	fmt.Printf("Extensions:\n")
	fmt.Printf("  BasicConstraints: HasBC=%v IsCA=%v HasPathLen=%v PathLen=%d\n",
		c.Extensions.HasBasicConstraints, c.Extensions.BasicConstraintsIsCA,
		c.Extensions.BasicConstraintsHasPathLen, c.Extensions.BasicConstraintsPathLen)
	fmt.Printf("  KeyUsage        : has=%v bitmap=0x%04x\n", c.Extensions.HasKeyUsage, c.Extensions.KeyUsage)
	fmt.Printf("  ExtendedKeyUsage: has=%v values=%v\n", c.Extensions.HasExtendedKeyUsage, c.Extensions.ExtendedKeyUsage)
	fmt.Printf("  SubjectKeyID    : has=%v bytes=%x\n", c.Extensions.HasSubjectKeyID, c.Extensions.SubjectKeyID)
	fmt.Printf("  AuthorityKeyID  : has=%v bytes=%x\n", c.Extensions.HasAuthorityKeyID, c.Extensions.AuthorityKeyID)
	fmt.Printf("  FutureExtensions: %d entries\n", len(c.Extensions.FutureExtensions))
	fmt.Printf("Signature (%d B) : %x\n", len(c.Signature), c.Signature)
	fmt.Printf("\nIsRoot=%v IsICA=%v IsNOC=%v\n", c.IsRoot(), c.IsICA(), c.IsNOC())

	pub, _ := hex.DecodeString(appleRCACPubKeyHex)
	fmt.Printf("\nApple RCAC PublicKey: %x (%d B)\n", pub, len(pub))
}
