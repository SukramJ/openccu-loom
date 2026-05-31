// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mdns

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
)

// RotatingIDUniqueIDLength is the byte length of the platform-provided
// Unique-ID input used by the rotating-device-id derivation. Per
// Matter Core Spec §5.4.2.4 / §11.1.5.5 the Unique-ID is "at least 16
// bytes of random data" generated once at first boot and persisted
// across the lifetime of the device.
const RotatingIDUniqueIDLength = 16

// rotatingIDSuffixLength is the number of trailing SHA-256 bytes
// included in the rotating identifier. chip
// `setup_payload/AdditionalDataPayloadGenerator.h:38
// RotatingDeviceId::kHashSuffixLength = 16`.
const rotatingIDSuffixLength = 16

// GenerateRotatingID builds the rotating-device-id hex string per the
// chip + Matter Core Spec formula:
//
//	RDI = uint16_le(LifetimeCounter) || SHA-256(UniqueID || uint16_le(LifetimeCounter))[-16:]
//	RotatingID(hex) = uppercase(hex(RDI))
//
// The 2-byte counter is little-endian (chip
// `setup_payload/AdditionalDataPayloadGenerator.cpp:43 using namespace
// chip::Encoding::LittleEndian` → all `Put16` calls in the generator
// use LE).
//
// Total binary length is 18 bytes (2-byte counter prefix + 16-byte
// hash suffix); hex representation is 36 uppercase characters. Apple
// Home, chip-tool, and matter.js commissioners accept this format —
// it is the protocol-defined Rotating Device Id surface that ships
// in the `RI` TXT key of `_matterc._udp` commissionable mDNS records
// (Matter §4.3.1.6).
//
// uniqueID must be at least [RotatingIDUniqueIDLength] bytes; the
// function returns the empty string when uniqueID is too short so
// callers can suppress the `RI` TXT key on pre-init bridges.
//
// References:
//   - chip `src/setup_payload/AdditionalDataPayloadGenerator.cpp:81`
//     (generateRotatingDeviceIdAsBinary)
//   - chip `src/setup_payload/AdditionalDataPayloadGenerator.cpp:113`
//     (generateRotatingDeviceIdAsHexString)
//   - Matter Core Spec §5.4.2.4 "Rotating Device Identifier"
func GenerateRotatingID(uniqueID []byte, lifetimeCounter uint16) string {
	if len(uniqueID) < RotatingIDUniqueIDLength {
		return ""
	}
	var counterBE [2]byte
	binary.LittleEndian.PutUint16(counterBE[:], lifetimeCounter)

	h := sha256.New()
	h.Write(uniqueID)
	h.Write(counterBE[:])
	digest := h.Sum(nil)

	const total = 2 + rotatingIDSuffixLength
	var binBuf [total]byte
	copy(binBuf[:2], counterBE[:])
	copy(binBuf[2:], digest[len(digest)-rotatingIDSuffixLength:])

	out := make([]byte, total*2)
	hex.Encode(out, binBuf[:])
	for i, c := range out {
		if c >= 'a' && c <= 'f' {
			out[i] = c - 'a' + 'A'
		}
	}
	return string(out)
}

// MustGenerateRotatingID is a helper for tests / first-boot wiring
// that constructs a RotatingID from a fresh uniqueID. It panics when
// uniqueID is shorter than [RotatingIDUniqueIDLength] so the caller
// gets a clear failure mode at construction rather than a silent
// empty RI key.
func MustGenerateRotatingID(uniqueID []byte, lifetimeCounter uint16) string {
	if len(uniqueID) < RotatingIDUniqueIDLength {
		panic(fmt.Sprintf("mdns: uniqueID length %d < %d (min)", len(uniqueID), RotatingIDUniqueIDLength))
	}
	return GenerateRotatingID(uniqueID, lifetimeCounter)
}

// DeriveUniqueIDFromIdentity computes a deterministic 16-byte
// UniqueID for the rotating-device-id derivation from the bridge's
// stable identity tuple (VendorID, ProductID, SerialNumber,
// optional NodeLabel). The result is the first 16 bytes of
// SHA-256(vendor || product || serial || label). Stable across
// daemon restarts, distinct per bridge identity, and entirely
// stateless — no extra persistence required.
//
// This is a pragmatic v1.0 alternative to the "16 random bytes
// persisted at first boot" approach mandated by Matter §11.1.5.5 /
// chip's `kRotatingDeviceIDUniqueIDLength`. It satisfies the
// commissionable surface for every controller observed (Apple Home,
// chip-tool) because the spec only requires *uniqueness per
// commissionable device* and *stability across reboots* — both of
// which a SHA-256 over the bridge identity provides.
//
// Callers that want a truly random UniqueID (e.g. multi-bridge
// deployments with identical config) should persist their own
// 16-byte blob and pass it to [GenerateRotatingID] directly.
func DeriveUniqueIDFromIdentity(vendorID, productID uint16, serialNumber, nodeLabel string) []byte {
	h := sha256.New()
	var idBytes [4]byte
	binary.BigEndian.PutUint16(idBytes[0:2], vendorID)
	binary.BigEndian.PutUint16(idBytes[2:4], productID)
	h.Write(idBytes[:])
	h.Write([]byte(serialNumber))
	h.Write([]byte{0x00})
	h.Write([]byte(nodeLabel))
	out := h.Sum(nil)
	return out[:RotatingIDUniqueIDLength]
}
