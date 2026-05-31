// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package core_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"sync"
	"testing"

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
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		fabrics:   make(map[uint8]mstore.FabricRecord),
		nextIdx:   1,
		identity:  make(map[uint8]mstore.IdentityRecord),
		groupKeys: make(map[[2]uint64]mstore.GroupKeySet),
		groupMaps: make(map[[2]uint64]mstore.GroupKeyMapping),
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

// ReplaceACL is the AddNOC default-entry insertion path. Tests do
// not assert on ACL contents yet, so the fake just records the call
// without persistence.
func (f *fakeStore) ReplaceACL(_ context.Context, _ uint8, _ []mstore.ACLEntry) error {
	return nil
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
)

func marshalTestPub(priv *ecdsa.PrivateKey) []byte {
	return elliptic.Marshal(elliptic.P256(), priv.PublicKey.X, priv.PublicKey.Y) //nolint:staticcheck // SA1019: canonical raw-point encoding for Matter TLV test fixtures
}

func buildCoreTestTBS(t *testing.T, pub []byte, isRoot bool) []byte {
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
		e.PutUint(tlv.ContextTag(17), uint64(0xAAAA)) // NodeID
		e.PutUint(tlv.ContextTag(21), uint64(0xBBBB)) // FabricID
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
	pub := marshalTestPub(priv)
	tbs := buildCoreTestTBS(t, pub, isRoot)

	// Build a probe cert with a zero-placeholder signature so Decode
	// succeeds; then derive the DER TBS bytes and sign over those.
	// This matches the verification path in mattercert.Verifier which
	// hashes TBSToDER(cert) — not the raw TLV bytes.
	if len(tbs) == 0 || tbs[len(tbs)-1] != 0x18 {
		t.Fatalf("buildCoreTestTBS: trailing byte not End-of-Container, got %#x", tbs[len(tbs)-1])
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
		e.PutUint(tlv.ContextTag(17), uint64(0xAAAA))
		e.PutUint(tlv.ContextTag(21), uint64(0xBBBB))
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
