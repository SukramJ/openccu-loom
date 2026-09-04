// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package setup encodes Matter Onboarding Payloads per Matter Core
// Specification 1.5.1 §5.1 (Manual Pairing Code) and §5.7 (QR Code
// Onboarding Payload).
//
// Two outputs:
//
//   - [QRCode] returns the "MT:..." string. The bytes are a 88-bit
//     little-endian struct (version, vendor_id, product_id,
//     custom_flow, discovery_caps, discriminator, passcode,
//     padding) base38-encoded with the alphabet from §5.7.4.
//
//   - [ManualCode] returns the 11-digit decimal code with a Verhoeff
//     check digit at position 11. The 4-bit upper discriminator and
//     the 27-bit passcode are split across the first 10 digits per
//     §5.1.4; digit 1 carries the version + vendor/product flag.
package setup

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"strings"
)

// DiscoveryCaps is the bitmap exposed in the QR onboarding payload
// (Matter §5.7.4 RendezvousInformation). Bridge advertises ON_NETWORK
// (bit 2) only; BLE / Soft-AP are not v1.1 transports.
type DiscoveryCaps uint8

const (
	// DiscoveryNone disables the bitmap (commissioner-driven discovery).
	DiscoveryNone DiscoveryCaps = 0
	// DiscoverySoftAP advertises an open Soft-AP for BLE-less commissioning.
	DiscoverySoftAP DiscoveryCaps = 1 << 0
	// DiscoveryBLE advertises BLE commissioning availability.
	DiscoveryBLE DiscoveryCaps = 1 << 1
	// DiscoveryOnIP advertises mDNS commissionable.
	DiscoveryOnIP DiscoveryCaps = 1 << 2
)

// Payload is the input to [QRCode]. Field bit-widths match Matter
// §5.1.4.1.
type Payload struct {
	// Version is the onboarding payload format version. Matter 1.x
	// defines exactly one (0); reserved 3 bits.
	Version uint8
	// VendorID is the IANA-assigned vendor identifier (16 bits).
	VendorID uint16
	// ProductID is the vendor-assigned product identifier (16 bits).
	ProductID uint16
	// CustomFlow selects the commissioning flow. 0 = Standard, 1 =
	// User-Action-Required, 2 = Custom. 2 bits.
	CustomFlow uint8
	// DiscoveryCaps is the rendezvous bitmap. 8 bits.
	DiscoveryCaps DiscoveryCaps
	// Discriminator is the 12-bit Matter commissioning discriminator.
	Discriminator uint16
	// Passcode is the 27-bit Matter setup code (1..99999998).
	Passcode uint32
}

// Validate returns nil when the payload's bit-widths are observed.
func (p Payload) Validate() error {
	if p.Version > 0x07 {
		return fmt.Errorf("setup: Version=%d exceeds 3-bit width", p.Version)
	}
	if p.CustomFlow > 0x03 {
		return fmt.Errorf("setup: CustomFlow=%d exceeds 2-bit width", p.CustomFlow)
	}
	if p.Discriminator > MaxDiscriminator {
		return fmt.Errorf("setup: Discriminator=0x%X exceeds 12-bit width", p.Discriminator)
	}
	if p.Passcode == 0 || p.Passcode > 99999998 {
		return fmt.Errorf("setup: Passcode=%d outside 1..99999998", p.Passcode)
	}
	return nil
}

// MaxDiscriminator is the widest value the commissioning discriminator
// carries. matter.js declares the field as `BitField(45, 12)` in the
// QR-code payload and rejects anything above 4095 outright
// (packages/types/src/schema/PairingCodeSchema.ts: the QrPairingCode bit
// map, and the manual-pairing-code encoder's
// "discriminator value must be less than 4096").
//
// It is exported because the config validator has to reject an
// out-of-range discriminator before the daemon boots, and a second
// spelling there would be a copy of this package's own bit-width rule.
const MaxDiscriminator = 0x0FFF

// base38Alphabet is the 38-character set from Matter §5.7.4.
const base38Alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ-."

// QRCode encodes p into the "MT:..." onboarding string per Matter
// §5.7. Errors out when p fails Validate.
func QRCode(p Payload) (string, error) {
	if err := p.Validate(); err != nil {
		return "", err
	}

	// Pack into a single 88-bit big.Int per §5.1.4.1, low-bit-first
	// allocation order (matches connectedhomeip's reference implementation
	// `QRCodeSetupPayloadGenerator::payloadBase38Representation`).
	bits := new(big.Int)
	pos := uint(0)
	put := func(v uint64, width uint) {
		x := new(big.Int).SetUint64(v)
		x.Lsh(x, pos)
		bits.Or(bits, x)
		pos += width
	}
	put(uint64(p.Version), 3)
	put(uint64(p.VendorID), 16)
	put(uint64(p.ProductID), 16)
	put(uint64(p.CustomFlow), 2)
	put(uint64(p.DiscoveryCaps), 8)
	put(uint64(p.Discriminator), 12)
	put(uint64(p.Passcode), 27)
	put(0, 4) // padding to 88 bits.

	// 88 bits → 11 bytes little-endian.
	raw := make([]byte, 11)
	tmp := new(big.Int).Set(bits)
	for i := range 11 {
		raw[i] = byte(new(big.Int).And(tmp, big.NewInt(0xFF)).Uint64() & 0xFF)
		tmp.Rsh(tmp, 8)
	}

	return "MT:" + base38Encode(raw), nil
}

// base38Encode encodes b into Matter base38 per §5.7.4. Each pair of
// input bytes produces 3 output characters; a trailing single byte
// produces 2; an empty trailer produces nothing.
func base38Encode(b []byte) string {
	var sb strings.Builder
	// Process in groups of 3 bytes → 5 base38 characters.
	i := 0
	for ; i+3 <= len(b); i += 3 {
		v := uint32(b[i]) | uint32(b[i+1])<<8 | uint32(b[i+2])<<16
		for range 5 {
			sb.WriteByte(base38Alphabet[v%38])
			v /= 38
		}
	}
	switch len(b) - i {
	case 2:
		v := uint32(b[i]) | uint32(b[i+1])<<8
		for range 4 {
			sb.WriteByte(base38Alphabet[v%38])
			v /= 38
		}
	case 1:
		v := uint32(b[i])
		for range 2 {
			sb.WriteByte(base38Alphabet[v%38])
			v /= 38
		}
	}
	return sb.String()
}

// ManualCode returns the 11-digit manual pairing code per Matter §5.1.4.
//
// Layout:
//
//	digit 1   :  (versionFlag<<2) | (discriminator >> 10) & 0x3
//	digits 2-6:  ((discriminator & 0x300) << 6) | (passcode & 0x3FFF), 5 digits
//	digits 7-10: passcode >> 14, 4 digits
//	digit 11  :  Verhoeff check digit over digits 1..10
//
// versionFlag is 0 in v1.1 (vendor + product not embedded). When
// vendor/product carry is needed, supply a 21-digit code via a
// future ManualCodeLong helper.
func ManualCode(discriminator uint16, passcode uint32) (string, error) {
	if discriminator > MaxDiscriminator {
		return "", fmt.Errorf("setup: Discriminator=0x%X exceeds 12-bit width", discriminator)
	}
	if passcode == 0 || passcode > 99999998 {
		return "", fmt.Errorf("setup: Passcode=%d outside 1..99999998", passcode)
	}

	upperDisc := uint32(discriminator>>10) & 0x3
	midDisc := uint32(discriminator>>8) & 0x3 // discriminator bits 9..8

	digit1 := upperDisc
	d2to6 := (midDisc << 14) | (passcode & 0x3FFF)
	d7to10 := passcode >> 14

	first := fmt.Sprintf("%01d", digit1)
	mid := fmt.Sprintf("%05d", d2to6)
	last := fmt.Sprintf("%04d", d7to10)

	tenDigits := first + mid + last
	if len(tenDigits) != 10 {
		return "", fmt.Errorf("setup: assembled %d digits, want 10", len(tenDigits))
	}

	check, err := verhoeffCheck(tenDigits)
	if err != nil {
		return "", err
	}
	return tenDigits + string(rune('0'+check)), nil //nolint:gosec // G115: check is a Verhoeff digit 0..9; '0'+check is 48..57, well within valid rune range; see #20
}

// verhoeffTable_d, verhoeffTable_p, verhoeffTable_inv are the permutation,
// dihedral-multiplication, and inverse tables of the Verhoeff scheme
// (Wikipedia: Verhoeff algorithm).
var (
	verhoeffTableD = [10][10]int{
		{0, 1, 2, 3, 4, 5, 6, 7, 8, 9},
		{1, 2, 3, 4, 0, 6, 7, 8, 9, 5},
		{2, 3, 4, 0, 1, 7, 8, 9, 5, 6},
		{3, 4, 0, 1, 2, 8, 9, 5, 6, 7},
		{4, 0, 1, 2, 3, 9, 5, 6, 7, 8},
		{5, 9, 8, 7, 6, 0, 4, 3, 2, 1},
		{6, 5, 9, 8, 7, 1, 0, 4, 3, 2},
		{7, 6, 5, 9, 8, 2, 1, 0, 4, 3},
		{8, 7, 6, 5, 9, 3, 2, 1, 0, 4},
		{9, 8, 7, 6, 5, 4, 3, 2, 1, 0},
	}
	verhoeffTableP = [8][10]int{
		{0, 1, 2, 3, 4, 5, 6, 7, 8, 9},
		{1, 5, 7, 6, 2, 8, 3, 0, 9, 4},
		{5, 8, 0, 3, 7, 9, 6, 1, 4, 2},
		{8, 9, 1, 6, 0, 4, 3, 5, 2, 7},
		{9, 4, 5, 3, 1, 2, 6, 8, 7, 0},
		{4, 2, 8, 6, 5, 7, 3, 9, 0, 1},
		{2, 7, 9, 3, 8, 0, 6, 4, 1, 5},
		{7, 0, 4, 6, 9, 1, 3, 2, 5, 8},
	}
	verhoeffTableInv = [10]int{0, 4, 3, 2, 1, 5, 6, 7, 8, 9}
)

// verhoeffCheck returns the Verhoeff check digit for the given
// decimal-digit string (rightmost char treated as position 1).
func verhoeffCheck(digits string) (int, error) {
	c := 0
	// Iterate from least-significant digit; position is 1-based for the P table.
	for i := range len(digits) {
		ch := digits[len(digits)-1-i]
		if ch < '0' || ch > '9' {
			return 0, errors.New("setup: ManualCode digits contain non-decimal byte")
		}
		c = verhoeffTableD[c][verhoeffTableP[(i+1)%8][ch-'0']]
	}
	return verhoeffTableInv[c], nil
}

// trivialPINs lists passcodes that are explicitly forbidden because they are
// trivially guessable. Mirrors chip src/crypto/CHIPCryptoPAL.cpp
// IsValidSetupPIN repeated-digit and sequential-digit checks.
var trivialPINs = [...]uint32{
	0o0000000,
	11111111,
	22222222,
	33333333,
	44444444,
	55555555,
	66666666,
	77777777,
	88888888,
	99999999,
	12345678,
	87654321,
}

// IsValidSetupPIN returns true when passcode is a legal Matter Setup PIN:
// non-zero, within the 27-bit range 1..99999998, and not in the trivial-code
// blacklist. Mirrors chip src/crypto/CHIPCryptoPAL.cpp IsValidSetupPIN.
func IsValidSetupPIN(passcode uint32) bool {
	if passcode == 0 || passcode > 99999998 {
		return false
	}
	for _, banned := range trivialPINs {
		if passcode == banned {
			return false
		}
	}
	return true
}

var _ = binary.LittleEndian // reserved for future endian-explicit helpers
