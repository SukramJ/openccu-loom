-- +goose Up
-- +goose StatementBegin

-- Bug-I root cause (2026-05-11): Matter §11.18.5
-- TrustedRootCertificates serves a list<octet_string<400>> where each
-- entry is the full Matter Certificate TLV envelope (the RCAC bytes the
-- commissioner sent via AddTrustedRootCertificate). Apple Home
-- validates every entry as a Matter Certificate TLV and silently
-- discards the entire Subscribe-Initial ReportData stream on a single
-- schema mismatch — which is what happened while we served the bare
-- 65-byte EC-P256 public key.
--
-- Mirrors matter.js HEAD `Fabric.ts:68` (`readonly rootCert: Bytes`)
-- persisted via `FabricManager.persistFabrics()` (Fabric.ts:160-170,
-- FabricManager.ts:172-188).
--
-- root_public_key is kept alongside root_cert: it is the trust anchor
-- consumed by the CASE sigma key lookup + CompressedFabricID HKDF
-- (Matter §4.13.2.4) and is computationally redundant with root_cert
-- (extractable via mattercert.Decode), but caching it avoids re-parsing
-- on every Sigma1 lookup.
--
-- Existing fabric rows get root_cert = NULL via the ALTER. Those
-- fabrics will be omitted from TrustedRootCertificates by the cluster
-- server (serving them with the old EC-pubkey-as-cert representation
-- would re-trigger Bug I); the affected commissioners must re-pair to
-- repopulate root_cert. This is acceptable: the only existing pairing
-- impacted is the Apple Home pair that Bug I currently blocks anyway.

ALTER TABLE matter_fabrics ADD COLUMN root_cert BLOB;

-- +goose StatementEnd

-- Down is destructive: the trusted-root certificate bytes for every
-- already-paired fabric are deleted. Every commissioner that depends on
-- TrustedRootCertificates carrying the full certificate (Apple Home, per the
-- Bug I note above) must be removed and re-paired — the same impact the Up
-- migration was written to fix in the first place.
-- +goose Down
-- +goose StatementBegin

-- SQLite ≥ 3.35 supports DROP COLUMN.
ALTER TABLE matter_fabrics DROP COLUMN root_cert;

-- +goose StatementEnd
