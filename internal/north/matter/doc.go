// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package matter is the v1.1 Matter bridge. Subpackages:
//
//   - tlv/           TLV codec (Matter Core Spec §A.7)
//   - transport/     UDP/IPv6, MRP, message framing
//   - secure/        Spake2+ (PASE), Sigma (CASE), session keys, AES-CCM
//   - im/            Interaction Model
//   - endpoint/      endpoint topology assembler
//   - cluster/       cluster protocol impls (wire format only)
//   - commissioning/ on-network discovery, attestation, fabric join
//   - mdns/          DNS-SD operational + commissionable advertisement
//   - attestation/   DAC/PAI/PAA chain validation
//   - server/        fabric, sessions, message dispatcher
//   - store/         fabric / NOC / shared-secrets persistence
//
// Architecture: rich model, dumb bridge — this package owns the Matter
// wire format only. Per-DataPoint cluster projection lives on the model
// types via the source-surface interfaces in
// [github.com/SukramJ/openccu-loom/pkg/interfaces].
//
// SPECIFICATION.md §6 and ADR 0012 are authoritative for design intent.
package matter
