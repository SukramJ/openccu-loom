// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package fabric implements the Matter Fabric primitive: a logical
// security domain shared between a Matter root CA, a node, and a set
// of operational peers.
//
// A Fabric is identified by:
//
//   - 64-bit Fabric ID
//   - 64-bit Node ID
//   - Root CA public key (P-256, 65-byte uncompressed)
//
// From these, the protocol derives a CompressedFabricID — an 8-byte
// value used as the public-facing fabric identifier in mDNS records
// and DestinationId calculations during Sigma1.
//
// Per Matter Core Specification §4.13.2.4:
//
//	CompressedFabricID = HKDF(salt=fabricID, IKM=rootPubKey,
//	                          info="CompressedFabric", L=8)
//
// where:
//   - salt is the 8-byte big-endian Fabric ID,
//   - IKM is the 65-byte uncompressed root public key (with the 0x04
//     prefix stripped per Matter spec — leaving 64 bytes of X || Y).
//
// The fabric package is dependency-free Go stdlib (uses crypto/hkdf
// and crypto/sha256 from Go 1.24+).
package fabric
