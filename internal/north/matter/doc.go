// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package matter is the v1.1 Matter bridge. Subpackages:
//
//   - bootid/        process-lifetime UniqueID salt (rotation off by default)
//   - bridge/        the composition unit: topology assembly, IM dispatcher
//     wiring, UDP listener, mDNS advertisement — what the daemon starts
//   - cluster/       cluster protocol impls (wire format only)
//   - commissioning/ on-network discovery, attestation, fabric join
//   - conformance/   golden-vector regression + load + chip-tool smoke tests
//   - diagevent/     bounded in-memory trace of events explaining a failed pairing
//   - eligibility/   candidate list for the operator-facing allowlist UI
//   - endpoint/      endpoint topology assembler
//   - im/            Interaction Model
//   - mdns/          DNS-SD operational + commissionable advertisement
//   - parity/        embedded matter.js HEAD schema snapshot for parity tests
//   - schema/        typed Go lookups over the parity/ snapshot (generated)
//   - secure/        Spake2+ (PASE), Sigma (CASE), session keys, AES-CCM,
//     DAC/PAI/PAA chain validation (secure/attestation/)
//   - store/         fabric / NOC / shared-secrets persistence
//   - tlv/           TLV codec (Matter Core Spec §A.7)
//   - transport/     UDP/IPv6, MRP, message framing
//
// Architecture: rich model, dumb bridge — this package owns the Matter
// wire format only. Per-DataPoint cluster projection lives on the model
// types via the source-surface interfaces in
// [github.com/SukramJ/openccu-loom/pkg/interfaces].
//
// SPECIFICATION.md §6 and ADR 0012 are authoritative for design intent.
package matter
