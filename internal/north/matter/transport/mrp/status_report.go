// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mrp

import "encoding/binary"

// Secure-Channel StatusReport general / protocol codes per Matter Core
// Spec §4.10.1.1 Table 17 (general codes) and §4.10.4 (Secure-Channel
// protocol codes). Only the codes the bridge actually emits are
// named; the remainder live as raw uint16s in the encoder call.
const (
	// SCStatusGeneralSuccess is the GeneralCode for any happy-path
	// status report (Spec §4.10.1.1 Table 17 — SUCCESS).
	SCStatusGeneralSuccess uint16 = 0x0000
	// SCStatusGeneralFailure is the GeneralCode for any error status
	// report (Spec §4.10.1.1 Table 17 — FAILURE).
	SCStatusGeneralFailure uint16 = 0x0001

	// SCStatusProtocolSessionEstablishmentSuccess is the Secure-Channel
	// ProtocolCode the verifier sends after a successful PASE / CASE
	// session establishment (Spec §4.10.4 — SESSION_ESTABLISHMENT_SUCCESS).
	// Pairs with [SCStatusGeneralSuccess] and an empty ProtocolData.
	SCStatusProtocolSessionEstablishmentSuccess uint16 = 0x0000
	// SCStatusProtocolNoSharedTrustRoots — peer has no fabric in
	// common; emitted by CASE (Spec §4.10.4 — NO_SHARED_TRUST_ROOTS).
	SCStatusProtocolNoSharedTrustRoots uint16 = 0x0001
	// SCStatusProtocolInvalidParameter — generic parameter rejection.
	SCStatusProtocolInvalidParameter uint16 = 0x0002
)

// EncodeStatusReport assembles a Secure-Channel StatusReport payload
// per Matter §4.10.1.1: little-endian uint16 generalCode || uint32
// protocolID || uint16 protocolCode || optional protocolData. Caller
// passes the empty payload as nil; encoder allocates the 8-byte
// minimum buffer regardless.
//
// The returned bytes are the StatusReport message body — the SC
// protocol header (Opcode = [SCOpcodeStatusReport], ProtocolID =
// [SecureChannelProtocolID]) is the responsibility of the dispatcher.
func EncodeStatusReport(generalCode uint16, protocolID uint32, protocolCode uint16, protocolData []byte) []byte {
	out := make([]byte, 8+len(protocolData))
	binary.LittleEndian.PutUint16(out[0:2], generalCode)
	binary.LittleEndian.PutUint32(out[2:6], protocolID)
	binary.LittleEndian.PutUint16(out[6:8], protocolCode)
	copy(out[8:], protocolData)
	return out
}
