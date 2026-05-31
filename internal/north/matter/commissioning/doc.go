// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package commissioning orchestrates Matter on-network commissioning
// from the PASE handshake through NOC installation.
//
// openccu-loom is the *commissionee* — the device being added to a
// fabric. The commissioner (Apple Home, Google Home, chip-tool, …)
// drives the protocol; this package is the bridge-side state machine
// that responds to each phase.
//
// Phases (Matter Core Spec §5):
//
//  1. PASE establishment — Spake2+ handshake using the operator-
//     supplied passcode. Produces a PASE [secure/channel.Session]
//     good for the commissioning window only.
//  2. Attestation — commissioner sends an AttestationRequest with a
//     32-byte nonce; the bridge returns the AttestationElements TLV
//     blob plus an ECDSA signature over (elements ‖ attestation
//     challenge) using the bridge's DAC private key.
//  3. CSR — commissioner sends CSRRequest with a 32-byte nonce; the
//     bridge generates a fresh P-256 keypair and returns a PKCS#10
//     CSR wrapped in NOCSR Elements TLV plus another DAC signature.
//  4. NOC install — commissioner sends AddTrustedRootCertificate +
//     AddNOC; the bridge persists both via the OperationalCredentials
//     cluster (Stufe 4) and binds the new fabric.
//  5. CASE handover — commissioner re-establishes a session via
//     Sigma using the freshly installed NOC; the bridge tears down
//     the PASE session.
//
// The DAC/PAI/PAA chain is X.509 DER (Matter §6.2/§6.3/§6.4) — not
// Matter-TLV. The Matter NOC chain is TLV-encoded; that decoder lives
// in [internal/north/matter/secure/mattercert].
package commissioning
