// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package core_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/core"
	mstore "github.com/SukramJ/openccu-loom/internal/north/matter/store"
	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"

	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/mattercert"
)

// ---- In-memory StoreFacade (opcreds + group key) ----

type fakeStore struct {
	mu        sync.RWMutex
	fabrics   map[uint8]mstore.FabricRecord
	nextIdx   uint8
	identity  map[uint8]mstore.IdentityRecord
	groupKeys map[[2]uint64]mstore.GroupKeySet     // key: [fabric, gks-id]
	groupMaps map[[2]uint64]mstore.GroupKeyMapping // key: [fabric, group-id]
	acls      map[uint8][]mstore.ACLEntry          // key: fabric
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		fabrics:   make(map[uint8]mstore.FabricRecord),
		nextIdx:   1,
		identity:  make(map[uint8]mstore.IdentityRecord),
		groupKeys: make(map[[2]uint64]mstore.GroupKeySet),
		groupMaps: make(map[[2]uint64]mstore.GroupKeyMapping),
		acls:      make(map[uint8][]mstore.ACLEntry),
	}
}

func (f *fakeStore) ListFabrics(_ context.Context) ([]mstore.FabricRecord, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]mstore.FabricRecord, 0, len(f.fabrics))
	for _, rec := range f.fabrics {
		out = append(out, rec)
	}
	return out, nil
}

func (f *fakeStore) GetFabric(_ context.Context, fabricIndex uint8) (mstore.FabricRecord, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	r, ok := f.fabrics[fabricIndex]
	if !ok {
		return mstore.FabricRecord{}, mstore.ErrFabricNotFound
	}
	return r, nil
}

func (f *fakeStore) AddFabric(_ context.Context, rec mstore.FabricRecord) (uint8, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	idx := f.nextIdx
	f.nextIdx++
	rec.FabricIndex = idx
	f.fabrics[idx] = rec
	return idx, nil
}

func (f *fakeStore) UpdateFabricLabel(_ context.Context, fabricIndex uint8, label string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.fabrics[fabricIndex]
	if !ok {
		return mstore.ErrFabricNotFound
	}
	r.Label = label
	f.fabrics[fabricIndex] = r
	return nil
}

func (f *fakeStore) UpdateFabricNodeID(_ context.Context, fabricIndex uint8, nodeID uint64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.fabrics[fabricIndex]
	if !ok {
		return mstore.ErrFabricNotFound
	}
	r.NodeID = nodeID
	f.fabrics[fabricIndex] = r
	return nil
}

func (f *fakeStore) RemoveFabric(_ context.Context, fabricIndex uint8) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.fabrics[fabricIndex]; !ok {
		return mstore.ErrFabricNotFound
	}
	delete(f.fabrics, fabricIndex)
	delete(f.identity, fabricIndex)
	// Mirror FK CASCADE: remove all group keys for this fabric.
	for k := range f.groupKeys {
		if k[0] == uint64(fabricIndex) {
			delete(f.groupKeys, k)
		}
	}
	for k := range f.groupMaps {
		if k[0] == uint64(fabricIndex) {
			delete(f.groupMaps, k)
		}
	}
	return nil
}

func (f *fakeStore) RemoveGroupKeysByFabric(_ context.Context, fabricIndex uint8) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for k := range f.groupKeys {
		if k[0] == uint64(fabricIndex) {
			delete(f.groupKeys, k)
		}
	}
	for k := range f.groupMaps {
		if k[0] == uint64(fabricIndex) {
			delete(f.groupMaps, k)
		}
	}
	return nil
}

func (f *fakeStore) UpsertIdentity(_ context.Context, rec mstore.IdentityRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.identity[rec.FabricIndex] = rec
	return nil
}

func (f *fakeStore) GetIdentity(_ context.Context, fabricIndex uint8) (mstore.IdentityRecord, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	r, ok := f.identity[fabricIndex]
	if !ok {
		return mstore.IdentityRecord{}, mstore.ErrIdentityNotFound
	}
	return r, nil
}

// ReplaceACL is the AddNOC default-entry insertion path. Entries are
// kept so a test can assert on what AddNOC actually persisted.
func (f *fakeStore) ReplaceACL(_ context.Context, fabricIndex uint8, entries []mstore.ACLEntry) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(entries) == 0 {
		delete(f.acls, fabricIndex)
		return nil
	}
	f.acls[fabricIndex] = append([]mstore.ACLEntry(nil), entries...)
	return nil
}

// ListACL returns the entries persisted for fabricIndex.
func (f *fakeStore) ListACL(_ context.Context, fabricIndex uint8) ([]mstore.ACLEntry, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return append([]mstore.ACLEntry(nil), f.acls[fabricIndex]...), nil
}

// GroupStoreFacade methods.
func gksKey(fabricIndex uint8, groupKeySetID uint16) [2]uint64 {
	return [2]uint64{uint64(fabricIndex), uint64(groupKeySetID)}
}

func gmKey(fabricIndex uint8, groupID uint16) [2]uint64 {
	return [2]uint64{uint64(fabricIndex), uint64(groupID)}
}

func (f *fakeStore) UpsertGroupKeySet(_ context.Context, rec mstore.GroupKeySet) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.groupKeys[gksKey(rec.FabricIndex, rec.GroupKeySetID)] = rec
	return nil
}

func (f *fakeStore) GetGroupKeySet(_ context.Context, fabricIndex uint8, groupKeySetID uint16) (mstore.GroupKeySet, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	r, ok := f.groupKeys[gksKey(fabricIndex, groupKeySetID)]
	if !ok {
		return mstore.GroupKeySet{}, mstore.ErrGroupKeySetNotFound
	}
	return r, nil
}

func (f *fakeStore) ListGroupKeySets(_ context.Context, fabricIndex uint8) ([]mstore.GroupKeySet, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	var out []mstore.GroupKeySet
	for k, v := range f.groupKeys {
		if k[0] == uint64(fabricIndex) {
			out = append(out, v)
		}
	}
	return out, nil
}

func (f *fakeStore) RemoveGroupKeySet(_ context.Context, fabricIndex uint8, groupKeySetID uint16) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.groupKeys, gksKey(fabricIndex, groupKeySetID))
	return nil
}

func (f *fakeStore) SetGroupKeyMapping(_ context.Context, m mstore.GroupKeyMapping) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.groupMaps[gmKey(m.FabricIndex, m.GroupID)] = m
	return nil
}

func (f *fakeStore) RemoveGroupKeyMapping(_ context.Context, fabricIndex uint8, groupID uint16) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.groupMaps, gmKey(fabricIndex, groupID))
	return nil
}

func (f *fakeStore) ListGroupKeyMappings(_ context.Context, fabricIndex uint8) ([]mstore.GroupKeyMapping, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	var out []mstore.GroupKeyMapping
	for k, v := range f.groupMaps {
		if k[0] == uint64(fabricIndex) {
			out = append(out, v)
		}
	}
	return out, nil
}

// ---- TLV cert helpers (reused from verify_test conventions) ----

const (
	testSigAlgoECDSA    uint64 = 1
	testPubAlgoEC       uint64 = 1
	testCurvePrime256v1 uint64 = 1

	// testDefaultFabricID / testDefaultNodeID are the subject identity
	// [buildCoreTestTBSForSubject] / [buildCoreSignedCert] stamp when a caller does
	// not need to control them explicitly. Kept as named constants (rather
	// than repeating the hex literal) so tests that must keep the same
	// FabricID stable across an AddNOC → UpdateNOC pair — while varying
	// NodeID — reference one source of truth.
	testDefaultFabricID uint64 = 0xBBBB
	testDefaultNodeID   uint64 = 0xAAAA
)

func marshalTestPub(priv *ecdsa.PrivateKey) []byte {
	return elliptic.Marshal(elliptic.P256(), priv.PublicKey.X, priv.PublicKey.Y) //nolint:staticcheck // SA1019: canonical raw-point encoding for Matter TLV test fixtures
}

// buildCoreTestTBSForSubject builds the TLV to-be-signed cert body,
// stamping the caller-supplied fabricID/nodeID into the NOC subject.
// isRoot==true ignores both (root certs carry an RCAC-ID subject, not
// a FabricID/NodeID pair).
func buildCoreTestTBSForSubject(t *testing.T, pub []byte, isRoot bool, fabricID, nodeID uint64) []byte {
	t.Helper()
	e := tlv.NewEncoder()
	e.StartStruct(tlv.AnonymousTag())
	e.PutOctets(tlv.ContextTag(1), []byte{0x01})   // serial
	e.PutUint(tlv.ContextTag(2), testSigAlgoECDSA) // sig algo

	// Issuer
	e.StartList(tlv.ContextTag(3))
	e.PutUint(tlv.ContextTag(20), uint64(0x0001)) // RCAC-ID
	if err := e.EndContainer(); err != nil {
		t.Fatalf("EndContainer issuer: %v", err)
	}

	e.PutUint(tlv.ContextTag(4), uint64(1000)) // NotBefore
	e.PutUint(tlv.ContextTag(5), uint64(0))    // NotAfter (never)

	// Subject
	e.StartList(tlv.ContextTag(6))
	if isRoot {
		e.PutUint(tlv.ContextTag(20), uint64(0x0001)) // RCAC-ID → IsRoot()
	} else {
		// NOC: NodeID (tag 17) + FabricID (tag 21)
		e.PutUint(tlv.ContextTag(17), nodeID)
		e.PutUint(tlv.ContextTag(21), fabricID)
	}
	if err := e.EndContainer(); err != nil {
		t.Fatalf("EndContainer subject: %v", err)
	}

	e.PutUint(tlv.ContextTag(7), testPubAlgoEC)       // pub algo
	e.PutUint(tlv.ContextTag(8), testCurvePrime256v1) // curve
	e.PutOctets(tlv.ContextTag(9), pub)               // public key

	// Extensions (tag 10). RCAC certificates require KeyUsage and
	// BasicConstraints so ValidateRCAC passes. NOC certs carry no
	// extensions (empty list); ValidateRCAC is not called on NOCs.
	e.StartList(tlv.ContextTag(10))
	if isRoot {
		// BasicConstraints (extTag=1): isCA=true (tag 1), pathLen=1 (tag 2).
		e.StartStruct(tlv.ContextTag(1))
		e.PutBool(tlv.ContextTag(1), true) // isCA
		e.PutUint(tlv.ContextTag(2), 1)    // pathLen = 1
		if err := e.EndContainer(); err != nil {
			t.Fatalf("EndContainer basicConstraints: %v", err)
		}
		// KeyUsage (extTag=2): keyCertSign (bit 5 = 0x20) | cRLSign (bit 6 = 0x40).
		e.PutUint(tlv.ContextTag(2), uint64(0x60))
	}
	if err := e.EndContainer(); err != nil {
		t.Fatalf("EndContainer ext: %v", err)
	}

	if err := e.EndContainer(); err != nil {
		t.Fatalf("EndContainer top: %v", err)
	}
	raw, err := e.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	return raw
}

func buildCoreSignedCert(t *testing.T, priv *ecdsa.PrivateKey, isRoot bool, signerPriv *ecdsa.PrivateKey) []byte {
	t.Helper()
	return buildCoreSignedCertForSubject(t, marshalTestPub(priv), isRoot, signerPriv, testDefaultFabricID, testDefaultNodeID)
}

// buildCoreSignedCertForPubKey mints a certificate over pub — a raw
// 65-byte uncompressed EC-P256 point — rather than deriving the point
// from a private key the test holds. Real commissioners work this way:
// the CSRResponse's embedded PKCS#10 request carries the device's own
// pending public key (see [extractPendingCSRPubKey]), and the CA signs
// a certificate over THAT key, never a throwaway key of its own. A NOC
// minted over any other key fails openccu-loom's
// AddNOC/UpdateNOC "public key must match the pending CSR" check —
// which is the correct rejection path for tests that intentionally
// exercise it (see [buildTestNOCAndRoot]), but the wrong fixture for
// tests that need a NOC that actually installs.
//
// fabricID/nodeID let the caller pin the subject identity explicitly,
// which UpdateNOC-flow tests need to hold FabricID stable across
// AddNOC → UpdateNOC while varying NodeID (UpdateNOC permits a NodeID
// change but rejects a FabricID change per Matter §11.18.6.9).
func buildCoreSignedCertForPubKey(t *testing.T, pub []byte, isRoot bool, signerPriv *ecdsa.PrivateKey, fabricID, nodeID uint64) []byte {
	t.Helper()
	return buildCoreSignedCertForSubject(t, pub, isRoot, signerPriv, fabricID, nodeID)
}

// buildCoreSignedCertForSubject is the shared implementation behind
// [buildCoreSignedCert] and [buildCoreSignedCertForPubKey].
func buildCoreSignedCertForSubject(t *testing.T, pub []byte, isRoot bool, signerPriv *ecdsa.PrivateKey, fabricID, nodeID uint64) []byte {
	t.Helper()
	tbs := buildCoreTestTBSForSubject(t, pub, isRoot, fabricID, nodeID)

	// Build a probe cert with a zero-placeholder signature so Decode
	// succeeds; then derive the DER TBS bytes and sign over those.
	// This matches the verification path in mattercert.Verifier which
	// hashes TBSToDER(cert) — not the raw TLV bytes.
	if len(tbs) == 0 || tbs[len(tbs)-1] != 0x18 {
		t.Fatalf("buildCoreTestTBSForSubject: trailing byte not End-of-Container, got %#x", tbs[len(tbs)-1])
	}
	probeRaw := append([]byte(nil), tbs[:len(tbs)-1]...)
	probeRaw = append(probeRaw, 0x30, 11, 0x40)
	probeRaw = append(probeRaw, make([]byte, 64)...)
	probeRaw = append(probeRaw, 0x18)
	probeCert, err := mattercert.Decode(probeRaw)
	if err != nil {
		t.Fatalf("decode probe cert: %v", err)
	}
	tbsDER, err := mattercert.TBSToDER(probeCert)
	if err != nil {
		t.Fatalf("TBSToDER: %v", err)
	}

	hash := sha256.Sum256(tbsDER)
	r, s, serr := ecdsa.Sign(rand.Reader, signerPriv, hash[:])
	if serr != nil {
		t.Fatalf("ecdsa.Sign: %v", serr)
	}
	sig := make([]byte, 64)
	rb, sb := r.Bytes(), s.Bytes()
	copy(sig[32-len(rb):32], rb)
	copy(sig[64-len(sb):64], sb)

	// Re-encode with signature.
	e := tlv.NewEncoder()
	e.StartStruct(tlv.AnonymousTag())
	e.PutOctets(tlv.ContextTag(1), []byte{0x01})
	e.PutUint(tlv.ContextTag(2), testSigAlgoECDSA)

	e.StartList(tlv.ContextTag(3))
	e.PutUint(tlv.ContextTag(20), uint64(0x0001))
	if err := e.EndContainer(); err != nil {
		t.Fatalf("EndContainer issuer2: %v", err)
	}

	e.PutUint(tlv.ContextTag(4), uint64(1000))
	e.PutUint(tlv.ContextTag(5), uint64(0))

	e.StartList(tlv.ContextTag(6))
	if isRoot {
		e.PutUint(tlv.ContextTag(20), uint64(0x0001))
	} else {
		e.PutUint(tlv.ContextTag(17), nodeID)
		e.PutUint(tlv.ContextTag(21), fabricID)
	}
	if err := e.EndContainer(); err != nil {
		t.Fatalf("EndContainer subject2: %v", err)
	}

	e.PutUint(tlv.ContextTag(7), testPubAlgoEC)
	e.PutUint(tlv.ContextTag(8), testCurvePrime256v1)
	e.PutOctets(tlv.ContextTag(9), pub)

	e.StartList(tlv.ContextTag(10))
	if isRoot {
		e.StartStruct(tlv.ContextTag(1))
		e.PutBool(tlv.ContextTag(1), true)
		e.PutUint(tlv.ContextTag(2), 1)
		if err := e.EndContainer(); err != nil {
			t.Fatalf("EndContainer basicConstraints2: %v", err)
		}
		e.PutUint(tlv.ContextTag(2), uint64(0x60))
	}
	if err := e.EndContainer(); err != nil {
		t.Fatalf("EndContainer ext2: %v", err)
	}

	e.PutOctets(tlv.ContextTag(11), sig)

	if err := e.EndContainer(); err != nil {
		t.Fatalf("EndContainer top2: %v", err)
	}
	raw, err := e.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	return raw
}

// buildTestNOCAndRoot returns rootRaw, nocRaw, rootPriv for AddNOC tests.
//
// nocRaw is minted over a throwaway key that is NOT the pending CSR
// key any CSRRequest call would install — it is a validly-signed NOC
// chained to rootRaw, but its embedded public key never matches
// whatever AddNOC/UpdateNOC currently holds as the pending key. That
// makes this fixture correct for exactly one purpose: exercising the
// NOCStatusInvalidPublicKey rejection path (chip
// FabricTable.cpp:890 / matter.js Fabric.ts:524-526). Tests that need
// a NOC that actually installs must mint over the cluster's real
// pending key instead — see [commissionTestFabric],
// [issueCSRPendingPubKey], and [buildCoreSignedCertForPubKey].
func buildTestNOCAndRoot(t *testing.T) (
	rootRaw []byte,
	nocRaw []byte,
	rootPriv *ecdsa.PrivateKey,
) {
	t.Helper()

	var err error

	rootPriv, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey root: %v", err)
	}

	nocPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey noc: %v", err)
	}

	rootRaw = buildCoreSignedCert(t, rootPriv, true, rootPriv)
	nocRaw = buildCoreSignedCert(t, nocPriv, false, rootPriv)

	return rootRaw, nocRaw, rootPriv
}

// extractPendingCSRPubKey decodes a CSRResponse.NOCSRElements payload
// (Matter §11.18.7.6: an anonymous Structure with context tag 1 = DER
// PKCS#10 CSR, tag 2 = the echoed nonce), parses the CSR, and returns
// the embedded ECDSA public key both as the raw uncompressed EC-P256
// point (65 bytes, 0x04-prefixed — the shape [buildCoreSignedCertForPubKey]
// expects) and in typed form. This is the public key a real
// commissioner signs the follow-up NOC over.
func extractPendingCSRPubKey(t *testing.T, nocsrElements []byte) (raw []byte, pub *ecdsa.PublicKey) {
	t.Helper()

	dec := tlv.NewDecoder(nocsrElements)
	outer, err := dec.Next()
	if err != nil {
		t.Fatalf("NOCSRElements: decode outer element: %v", err)
	}
	if outer.Type != tlv.TypeStructure {
		t.Fatalf("NOCSRElements: outer element type = %v, want Structure", outer.Type)
	}

	var csrDER []byte
	for {
		el, err := dec.Next()
		if err != nil {
			t.Fatalf("NOCSRElements: decode field: %v", err)
		}
		if el.IsEndContainer {
			break
		}
		if el.Tag.Kind == tlv.TagKindContext && el.Tag.Number == 1 {
			csrDER = el.Octets
		}
	}
	if len(csrDER) == 0 {
		t.Fatal("NOCSRElements: CSR (context tag 1) missing or empty")
	}

	csr, err := x509.ParseCertificateRequest(csrDER)
	if err != nil {
		t.Fatalf("ParseCertificateRequest: %v", err)
	}
	ecdsaPub, ok := csr.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		t.Fatalf("CSR public key type = %T, want *ecdsa.PublicKey", csr.PublicKey)
	}
	raw = elliptic.Marshal(elliptic.P256(), ecdsaPub.X, ecdsaPub.Y) //nolint:staticcheck // SA1019: canonical raw-point encoding matches the NOC subject public-key wire field
	return raw, ecdsaPub
}

// issueCSRPendingPubKey invokes CSRRequest (opcode 0x04) against oc and
// returns the raw 65-byte EC-P256 public key from the returned
// CSRResponse — the key a real commissioner would sign the follow-up
// NOC over. forUpdate mirrors [core.CSRRequest.IsForUpdateNOC]; callers
// issuing an UpdateNOC-bound CSR pass a ctx that already carries the
// CASE fabric filter (see [im.WithFabricFilter]).
func issueCSRPendingPubKey(ctx context.Context, t *testing.T, oc *core.OperationalCredentials, forUpdate bool) []byte {
	t.Helper()

	resp, err := oc.MatterInvoke(ctx, 0x04, core.CSRRequest{
		CSRNonce:       make([]byte, 32),
		IsForUpdateNOC: forUpdate,
	})
	if err != nil {
		t.Fatalf("CSRRequest(IsForUpdateNOC=%v): %v", forUpdate, err)
	}
	csrResp, ok := resp.(core.CSRResponse)
	if !ok {
		t.Fatalf("CSRRequest response type = %T, want core.CSRResponse", resp)
	}
	raw, _ := extractPendingCSRPubKey(t, csrResp.NOCSRElements)
	return raw
}

// commissionTestFabric drives the real commissioning sequence —
// AddTrustedRootCertificate → CSRRequest → AddNOC with a NOC minted
// over the CSR's actual pending public key — against oc, and returns
// the trust root (raw Matter Certificate TLV bytes + private key) plus
// the FabricIndex AddNOC installed. Fails the test immediately if
// AddNOC does not return NOCStatusOK, so callers can treat the return
// values as a successfully committed fabric.
func commissionTestFabric(ctx context.Context, t *testing.T, oc *core.OperationalCredentials) (rootRaw []byte, rootPriv *ecdsa.PrivateKey, fabricIndex uint8) {
	t.Helper()

	var err error
	rootPriv, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey root: %v", err)
	}
	rootRaw = buildCoreSignedCert(t, rootPriv, true, rootPriv)

	if _, err := oc.MatterInvoke(ctx, 0x0B, core.AddTrustedRootCertificateRequest{
		RootCACertificate: rootRaw,
	}); err != nil {
		t.Fatalf("AddTrustedRootCertificate: %v", err)
	}

	pendingPub := issueCSRPendingPubKey(ctx, t, oc, false)
	nocRaw := buildCoreSignedCertForPubKey(t, pendingPub, false, rootPriv, testDefaultFabricID, testDefaultNodeID)

	resp, err := oc.MatterInvoke(ctx, 0x06, core.AddNOCRequest{
		NOCValue:         nocRaw,
		IPKValue:         make([]byte, 16),
		CaseAdminSubject: 0xABCD,
		AdminVendorID:    0x1234,
	})
	if err != nil {
		t.Fatalf("AddNOC: %v", err)
	}
	nr, ok := resp.(core.NOCResponse)
	if !ok {
		t.Fatalf("AddNOC response type = %T, want core.NOCResponse", resp)
	}
	if nr.StatusCode != core.NOCStatusOK {
		t.Fatalf("AddNOC StatusCode = %d (%s), want OK", nr.StatusCode, nr.DebugText)
	}
	return rootRaw, rootPriv, nr.FabricIndex
}

// mintUpdateNOC issues an IsForUpdateNOC CSR against fabCtx (a context
// already carrying the CASE fabric filter for fabricIndex — see
// [im.WithFabricFilter]) and mints a NOC over the resulting pending
// key, signed by signerPriv and stamped with fabricID/nodeID. Returns
// the raw NOC bytes ready for an UpdateNOC (0x07) invoke on the same
// fabCtx. Used by UpdateNOC behaviour tests that need to vary the
// signer / FabricID / NodeID independently while still minting over
// the cluster's real pending key.
func mintUpdateNOC(fabCtx context.Context, t *testing.T, oc *core.OperationalCredentials, signerPriv *ecdsa.PrivateKey, fabricID, nodeID uint64) []byte {
	t.Helper()

	// A real UpdateNOC runs in a fresh FailSafe window: the commissioner
	// re-arms ArmFailSafe (→ ClearPendingState) before the update CSRRequest.
	// Without that reset, the CSRRequest lands in the same window as the
	// initial AddNOC and is rejected with ConstraintError — matter.js
	// OperationalCredentialsServer.ts:131-137 (failsafeContext.fabricIndex !==
	// undefined). Simulate the re-arm here so the helper reflects the real flow.
	oc.ClearPendingState()
	pendingPub := issueCSRPendingPubKey(fabCtx, t, oc, true)
	return buildCoreSignedCertForPubKey(t, pendingPub, false, signerPriv, fabricID, nodeID)
}
