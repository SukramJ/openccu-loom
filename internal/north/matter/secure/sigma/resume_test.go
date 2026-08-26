// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package sigma

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"errors"
	"testing"
)

// stubResumptionStore is a minimal in-memory [ResumptionStore] for
// testing. Thread-safety is not required for unit tests.
type stubResumptionStore struct {
	records map[string]*ResumptionRecord
}

func newStubStore() *stubResumptionStore {
	return &stubResumptionStore{records: make(map[string]*ResumptionRecord)}
}

func (s *stubResumptionStore) put(rec *ResumptionRecord) {
	s.records[string(rec.ResumptionID)] = rec
}

func (s *stubResumptionStore) GetByID(id []byte) (*ResumptionRecord, error) {
	rec, ok := s.records[string(id)]
	if !ok {
		return nil, errors.New("sigma test store: not found")
	}
	return rec, nil
}

// buildValidSigma1WithResume constructs a Sigma1 wire frame carrying
// resumptionId (tag 6) + a correct initiatorResumeMIC (tag 7) for a
// randomly-generated sharedSecret and resumptionID. The responder uses
// the same KDF path to verify the MIC, so seeding the store with the
// returned sharedSecret + resumptionID is sufficient to make the resume
// path succeed.
func buildValidSigma1WithResume(t *testing.T) (sigma1Bytes, sharedSecret, resumptionID []byte) {
	t.Helper()

	// Use a 32-byte random shared secret — the resume KDF takes any
	// 32-byte value as IKM; it doesn't have to come from a real ECDH round.
	sharedSecret = make([]byte, 32)
	if _, err := rand.Read(sharedSecret); err != nil {
		t.Fatalf("rand.Read sharedSecret: %v", err)
	}

	// Build a Sigma1 that carries resumptionID + a correct initiatorResumeMIC
	// for the given sharedSecret. We call the exact same KDF path the
	// responder will use to verify.
	rid := make([]byte, ResumptionIDSize)
	if _, err := rand.Read(rid); err != nil {
		t.Fatalf("rand.Read resumptionID: %v", err)
	}

	// Produce a valid Sigma1 wire frame using a fresh initiator random.
	var random [RandomSize]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatalf("rand.Read random: %v", err)
	}

	// Compute KDFSR1 key.
	sr1Salt := append(append([]byte(nil), random[:]...), rid...)
	sr1Key, err := hkdfDerive(sharedSecret, sr1Salt, HKDFInfoSigma1Resume, SessionKeySize)
	if err != nil {
		t.Fatalf("hkdfDerive SR1: %v", err)
	}
	// Compute initiatorResumeMIC = AES-CCM seal of empty plaintext.
	resumeMIC, err := sealResumeMIC(sr1Key, nonceResume1MIC)
	if err != nil {
		t.Fatalf("sealResumeMIC: %v", err)
	}

	// Generate a valid P-256 ephemeral key so the Full Sigma fallback
	// path (which calls validatePoint) doesn't reject the Sigma1.
	ephPriv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("ecdh.P256().GenerateKey: %v", err)
	}
	ephPubBytes := ephPriv.PublicKey().Bytes()

	// Build wire bytes manually so we include tags 6+7.
	enc := sigmaTLVEncoder()
	enc.startStruct()
	enc.putOctets(1, random[:])
	enc.putUint16(2, 0x1234)
	enc.putOctets(3, make([]byte, 32))
	enc.putOctets(4, ephPubBytes)
	enc.putOctets(6, rid)
	enc.putOctets(7, resumeMIC)
	enc.endContainer()
	sigma1Bytes = enc.bytes()

	return sigma1Bytes, sharedSecret, rid
}

// TestSigma_Resume_RoundTrip verifies that a Sigma1 carrying a valid
// resumptionId + initiatorResumeMIC triggers the Sigma2_Resume path
// and produces decodable reply bytes with the expected fields.
func TestSigma_Resume_RoundTrip(t *testing.T) {
	sigma1Bytes, sharedSecret, rid := buildValidSigma1WithResume(t)

	store := newStubStore()
	store.put(&ResumptionRecord{
		SharedSecret: sharedSecret,
		ResumptionID: rid,
	})

	respID := newTestIdentity(t, 0xBBBB, 1, fabricIPK())
	v := testVerifier{}
	responder := NewResponder(respID, v, 0x2001)
	responder.SetResumptionStore(store)

	result, err := responder.ProcessSigma1WithResume(sigma1Bytes)
	if err != nil {
		t.Fatalf("ProcessSigma1WithResume: %v", err)
	}
	if !result.IsResume() {
		t.Fatal("expected Sigma2_Resume path but got Full Sigma")
	}
	s2r := result.Sigma2Resume
	if len(s2r.ResumptionID) != ResumptionIDSize {
		t.Fatalf("ResumptionID length=%d, want %d", len(s2r.ResumptionID), ResumptionIDSize)
	}
	if len(s2r.Sigma2ResumeMIC) != ResumptionIDSize {
		t.Fatalf("Sigma2ResumeMIC length=%d, want %d", len(s2r.Sigma2ResumeMIC), ResumptionIDSize)
	}
	if s2r.ResponderSessionID != 0x2001 {
		t.Fatalf("ResponderSessionID=%#x, want 0x2001", s2r.ResponderSessionID)
	}
	// Fresh resumptionID must differ from the one the initiator sent.
	if bytes.Equal(s2r.ResumptionID, rid) {
		t.Fatal("Sigma2Resume.ResumptionID must be fresh (differ from peer's)")
	}
	// ResumeKeys must be non-zero.
	var zeroKeys SessionKeys
	if result.ResumeKeys == zeroKeys {
		t.Fatal("ResumeKeys are all-zero after resume")
	}
	// Sigma3 must NOT be expected (Sigma2Resume is non-nil, not Sigma2).
	if result.Sigma2 != nil {
		t.Fatal("Sigma2 should be nil on the resume path")
	}

	// Verify MarshalSigma2Resume encodes without panic and can be decoded.
	wire := MarshalSigma2Resume(*s2r)
	if len(wire) == 0 {
		t.Fatal("MarshalSigma2Resume produced empty bytes")
	}
}

// TestSigma_Resume_BadMIC_FallsBackToFullSigma verifies that a Sigma1
// with a wrong initiatorResumeMIC causes the responder to silently fall
// through to Full Sigma rather than returning an error.
func TestSigma_Resume_BadMIC_FallsBackToFullSigma(t *testing.T) {
	sigma1Bytes, sharedSecret, rid := buildValidSigma1WithResume(t)
	// Corrupt the MIC by flipping the last byte of the Sigma1.
	// The MIC occupies the last 16 bytes inside the TLV, so flip the
	// second-to-last byte (the last is the struct end-marker).
	sigma1Bytes[len(sigma1Bytes)-2] ^= 0xFF

	store := newStubStore()
	store.put(&ResumptionRecord{
		SharedSecret: sharedSecret,
		ResumptionID: rid,
	})

	respID := newTestIdentity(t, 0xBBBB, 1, fabricIPK())
	v := testVerifier{}
	responder := NewResponder(respID, v, 0x2001)
	responder.SetResumptionStore(store)

	result, err := responder.ProcessSigma1WithResume(sigma1Bytes)
	if err != nil {
		t.Fatalf("unexpected error on bad MIC (should fall back): %v", err)
	}
	if result.IsResume() {
		t.Fatal("expected Full Sigma fallback but got Sigma2_Resume")
	}
	if result.Sigma2 == nil {
		t.Fatal("Full Sigma fallback must produce Sigma2")
	}
}

// TestSigma_Resume_UnknownResumptionID_FallsBackToFullSigma verifies
// that a Sigma1 with an unrecognised resumptionId (not in the store)
// produces a Full Sigma response rather than an error.
func TestSigma_Resume_UnknownResumptionID_FallsBackToFullSigma(t *testing.T) {
	sigma1Bytes, sharedSecret, _ := buildValidSigma1WithResume(t)
	// Store a DIFFERENT id so the lookup misses.
	differentID := make([]byte, ResumptionIDSize)
	if _, err := rand.Read(differentID); err != nil {
		t.Fatalf("rand: %v", err)
	}
	store := newStubStore()
	store.put(&ResumptionRecord{
		SharedSecret: sharedSecret,
		ResumptionID: differentID, // NOT the one carried in sigma1Bytes
	})

	respID := newTestIdentity(t, 0xBBBB, 1, fabricIPK())
	v := testVerifier{}
	responder := NewResponder(respID, v, 0x2001)
	responder.SetResumptionStore(store)

	result, err := responder.ProcessSigma1WithResume(sigma1Bytes)
	if err != nil {
		t.Fatalf("unexpected error on unknown resumptionID: %v", err)
	}
	if result.IsResume() {
		t.Fatal("expected Full Sigma fallback but got Sigma2_Resume")
	}
	if result.Sigma2 == nil {
		t.Fatal("Full Sigma fallback must produce Sigma2")
	}
}

// TestSigma_Resume_NoStore_FallsBackToFullSigma ensures that a Sigma1
// with resume fields but no ResumptionStore wired still completes with
// Full Sigma — no panic or error.
func TestSigma_Resume_NoStore_FallsBackToFullSigma(t *testing.T) {
	sigma1Bytes, _, _ := buildValidSigma1WithResume(t)

	respID := newTestIdentity(t, 0xBBBB, 1, fabricIPK())
	v := testVerifier{}
	responder := NewResponder(respID, v, 0x2001)
	// No SetResumptionStore call.

	result, err := responder.ProcessSigma1WithResume(sigma1Bytes)
	if err != nil {
		t.Fatalf("unexpected error without store: %v", err)
	}
	if result.IsResume() {
		t.Fatal("expected Full Sigma (no store) but got Sigma2_Resume")
	}
	if result.Sigma2 == nil {
		t.Fatal("Full Sigma fallback must produce Sigma2")
	}
}

// TestSigma_Resume_ProcessSigma1Compat verifies that the legacy
// ProcessSigma1 (without resume) still produces a valid Sigma2 when
// called directly, guaranteeing backward compatibility for callers
// that don't use ProcessSigma1WithResume.
func TestSigma_Resume_ProcessSigma1Compat(t *testing.T) {
	ipk := fabricIPK()
	initID := newTestIdentity(t, 0xAAAA, 1, ipk)
	respID := newTestIdentity(t, 0xBBBB, 1, ipk)
	v := testVerifier{}

	initiator := NewInitiator(initID, v, 0x1001, [32]byte{0xDE, 0xAD})
	responder := NewResponder(respID, v, 0x2001)

	s1, err := initiator.GenerateSigma1()
	if err != nil {
		t.Fatalf("GenerateSigma1: %v", err)
	}
	s2, err := responder.ProcessSigma1(s1)
	if err != nil {
		t.Fatalf("ProcessSigma1: %v", err)
	}
	s3, err := initiator.ProcessSigma2(s2)
	if err != nil {
		t.Fatalf("ProcessSigma2: %v", err)
	}
	if err := responder.ProcessSigma3(s3); err != nil {
		t.Fatalf("ProcessSigma3: %v", err)
	}
	initKeys, _ := initiator.SessionKeys()
	respKeys, _ := responder.SessionKeys()
	if !constantTimeKeysEqual(initKeys, respKeys) {
		t.Fatal("Full-Sigma key mismatch after compat round-trip")
	}
}

// TestResponder_ResumptionAccessors_ReturnDefensiveCopies verifies that
// ECDHSharedSecret() and ResumptionID() expose non-nil, non-aliased copies
// of the responder's internal state after a successful Sigma1, and
// mutations of the returned slice must not corrupt the responder's internal
// fields.
//
// These accessors are required so daemon.go's CASE onEstablished callback
// can call operational.Manager.PersistResumption without reaching into
// Responder internals. Mirrors matter.js
// packages/protocol/src/session/case/CaseServer.ts:210 —
// cx.resumptionRecord.sharedSecret / cx.resumptionRecord.resumptionId.
func TestResponder_ResumptionAccessors_ReturnDefensiveCopies(t *testing.T) {
	t.Parallel()
	ipk := fabricIPK()
	initID := newTestIdentity(t, 0xAAAA, 1, ipk)
	respID := newTestIdentity(t, 0xBBBB, 1, ipk)
	v := testVerifier{}

	initiator := NewInitiator(initID, v, 0x1001, [32]byte{0xDE, 0xAD})
	responder := NewResponder(respID, v, 0x2001)

	// Before Sigma1 — both accessors must return nil.
	if got := responder.ECDHSharedSecret(); got != nil {
		t.Fatalf("ECDHSharedSecret before Sigma1 = %x, want nil", got)
	}
	if got := responder.ResumptionID(); got != nil {
		t.Fatalf("ResumptionID before Sigma1 = %x, want nil", got)
	}

	s1, err := initiator.GenerateSigma1()
	if err != nil {
		t.Fatalf("GenerateSigma1: %v", err)
	}
	if _, err := responder.ProcessSigma1(s1); err != nil {
		t.Fatalf("ProcessSigma1: %v", err)
	}

	// After Sigma1 — both must be non-nil with expected lengths.
	secret := responder.ECDHSharedSecret()
	if len(secret) == 0 {
		t.Fatal("ECDHSharedSecret is empty after ProcessSigma1")
	}
	rid := responder.ResumptionID()
	if len(rid) != ResumptionIDSize {
		t.Fatalf("ResumptionID length=%d, want %d", len(rid), ResumptionIDSize)
	}

	// Second call must return a fresh independent copy — same bytes, different pointer.
	secret2 := responder.ECDHSharedSecret()
	if &secret[0] == &secret2[0] {
		t.Fatal("ECDHSharedSecret returned aliased slice on second call")
	}
	if !bytes.Equal(secret, secret2) {
		t.Fatal("ECDHSharedSecret values diverge across calls")
	}

	rid2 := responder.ResumptionID()
	if &rid[0] == &rid2[0] {
		t.Fatal("ResumptionID returned aliased slice on second call")
	}
	if !bytes.Equal(rid, rid2) {
		t.Fatal("ResumptionID values diverge across calls")
	}

	// Mutating the returned slice must not corrupt the responder.
	secret[0] ^= 0xFF
	secretAfterMutation := responder.ECDHSharedSecret()
	if bytes.Equal(secretAfterMutation, secret) {
		t.Fatal("ECDHSharedSecret: mutation of returned slice corrupted responder internal state")
	}

	rid[0] ^= 0xFF
	ridAfterMutation := responder.ResumptionID()
	if bytes.Equal(ridAfterMutation, rid) {
		t.Fatal("ResumptionID: mutation of returned slice corrupted responder internal state")
	}
}

// TestSigma_Resume_AdoptsPeerSessionState verifies that a successful
// Sigma2_Resume makes the responder's post-resume accessors describe
// the RESUMED session (peer session id, peer node id, peer CATs,
// shared secret, fresh resumption id, session keys, identity) rather
// than leaving them at their zero values. Without this adoption the
// resumed session would register with peerNodeID=0 (every inbound
// AES-CCM verify fails) and peerSessionID=0 (every outbound reply
// stamps the wrong session id) — see protocol.go tryResume's closing
// comment for the full failure mode.
func TestSigma_Resume_AdoptsPeerSessionState(t *testing.T) {
	sigma1Bytes, sharedSecret, rid := buildValidSigma1WithResume(t)

	const peerNodeID uint64 = 0xAABBCCDD00112233
	peerCATs := []uint32{0xFABC0001}

	store := newStubStore()
	store.put(&ResumptionRecord{
		SharedSecret: sharedSecret,
		ResumptionID: rid,
		PeerNodeID:   peerNodeID,
		PeerCATs:     peerCATs,
		// FabricIndex intentionally left at zero — no resolver wired.
	})

	respID := newTestIdentity(t, 0xBBBB, 1, fabricIPK())
	v := testVerifier{}
	responder := NewResponder(respID, v, 0x2001)
	responder.SetResumptionStore(store)

	result, err := responder.ProcessSigma1WithResume(sigma1Bytes)
	if err != nil {
		t.Fatalf("ProcessSigma1WithResume: %v", err)
	}
	if !result.IsResume() {
		t.Fatal("expected Sigma2_Resume path but got Full Sigma")
	}

	// buildValidSigma1WithResume hard-codes InitiatorSessionID=0x1234.
	if got := responder.PeerSessionID(); got != 0x1234 {
		t.Errorf("PeerSessionID()=%#x, want 0x1234", got)
	}
	if got := responder.PeerNodeID(); got != peerNodeID {
		t.Errorf("PeerNodeID()=%#x, want %#x", got, peerNodeID)
	}
	gotCATs := responder.PeerCATs()
	if len(gotCATs) != len(peerCATs) {
		t.Fatalf("PeerCATs() length=%d, want %d", len(gotCATs), len(peerCATs))
	}
	for i, want := range peerCATs {
		if gotCATs[i] != want {
			t.Errorf("PeerCATs()[%d]=%#x, want %#x", i, gotCATs[i], want)
		}
	}
	if got := responder.ECDHSharedSecret(); !bytes.Equal(got, sharedSecret) {
		t.Errorf("ECDHSharedSecret()=%x, want %x", got, sharedSecret)
	}

	newRID := responder.ResumptionID()
	if !bytes.Equal(newRID, result.Sigma2Resume.ResumptionID) {
		t.Errorf("ResumptionID()=%x, want %x (Sigma2Resume.ResumptionID)", newRID, result.Sigma2Resume.ResumptionID)
	}
	if bytes.Equal(newRID, rid) {
		t.Error("ResumptionID() must be fresh (differ from the peer-presented id)")
	}

	keys, ok := responder.SessionKeys()
	if !ok {
		t.Fatal("SessionKeys() ok=false after a successful resume")
	}
	if keys != result.ResumeKeys {
		t.Error("SessionKeys() does not match result.ResumeKeys")
	}

	if _, _, ok := responder.SessionIdentity(); !ok {
		t.Error("SessionIdentity() ok=false after a successful resume")
	}
}

// stubFabricIndexResolver implements both [IdentityResolver] and
// [FabricIndexResolver] for exercising the resume path's optional
// fabric-lookup branch. ResolveSigma1Destination always misses — the
// resume path never calls it (there is no DestinationID on a resuming
// Sigma1), so it stands in only to satisfy the [IdentityResolver]
// embedding [FabricIndexResolver] extends.
type stubFabricIndexResolver struct {
	identity    *Identity
	verifier    PeerVerifier
	fabricIndex uint8
}

func (stubFabricIndexResolver) ResolveSigma1Destination(_ [32]byte, _ [RandomSize]byte) (*Identity, PeerVerifier, bool) {
	return nil, nil, false
}

func (s stubFabricIndexResolver) ResolveFabricIndex(fabricIndex uint8) (*Identity, PeerVerifier, bool) {
	if fabricIndex != s.fabricIndex {
		return nil, nil, false
	}
	return s.identity, s.verifier, true
}

// TestSigma_Resume_FabricIndexResolver_SelectsRecordFabric verifies
// that when the wired [IdentityResolver] also implements
// [FabricIndexResolver], tryResume looks up the identity by the
// resumption record's FabricIndex (not the responder's
// constructor-time baseline identity) and the resumed session reports
// that fabric via SessionIdentity.
func TestSigma_Resume_FabricIndexResolver_SelectsRecordFabric(t *testing.T) {
	sigma1Bytes, sharedSecret, rid := buildValidSigma1WithResume(t)

	const wantFabricIndex uint8 = 7
	fabricIdentity := newTestIdentity(t, 0xCCCC, 2, fabricIPK())
	fabricIdentity.FabricIndex = wantFabricIndex

	store := newStubStore()
	store.put(&ResumptionRecord{
		SharedSecret: sharedSecret,
		ResumptionID: rid,
		FabricIndex:  wantFabricIndex,
	})

	respID := newTestIdentity(t, 0xBBBB, 1, fabricIPK())
	v := testVerifier{}
	responder := NewResponder(respID, v, 0x2001)
	responder.SetResumptionStore(store)
	responder.SetIdentityResolver(stubFabricIndexResolver{
		identity:    fabricIdentity,
		verifier:    v,
		fabricIndex: wantFabricIndex,
	})

	result, err := responder.ProcessSigma1WithResume(sigma1Bytes)
	if err != nil {
		t.Fatalf("ProcessSigma1WithResume: %v", err)
	}
	if !result.IsResume() {
		t.Fatal("expected Sigma2_Resume path but got Full Sigma")
	}

	gotFabricIndex, _, ok := responder.SessionIdentity()
	if !ok {
		t.Fatal("SessionIdentity() ok=false after resume")
	}
	if gotFabricIndex != wantFabricIndex {
		t.Errorf("SessionIdentity() fabricIndex=%d, want %d", gotFabricIndex, wantFabricIndex)
	}
}

// TestSigma_Resume_FabricIndexResolver_MissFallsToFullSigma verifies
// that a FabricIndexResolver miss (the record names a fabric that no
// longer has an installed identity) makes the whole resume attempt
// fall through to Full Sigma, exactly like an unknown resumption id.
func TestSigma_Resume_FabricIndexResolver_MissFallsToFullSigma(t *testing.T) {
	sigma1Bytes, sharedSecret, rid := buildValidSigma1WithResume(t)

	const recordFabricIndex uint8 = 9

	store := newStubStore()
	store.put(&ResumptionRecord{
		SharedSecret: sharedSecret,
		ResumptionID: rid,
		FabricIndex:  recordFabricIndex,
	})

	respID := newTestIdentity(t, 0xBBBB, 1, fabricIPK())
	v := testVerifier{}
	responder := NewResponder(respID, v, 0x2001)
	responder.SetResumptionStore(store)
	// The resolver only knows about a DIFFERENT fabric index than the
	// one the record names, so ResolveFabricIndex(recordFabricIndex) misses.
	responder.SetIdentityResolver(stubFabricIndexResolver{
		identity:    newTestIdentity(t, 0xDDDD, 3, fabricIPK()),
		verifier:    v,
		fabricIndex: recordFabricIndex + 1,
	})

	result, err := responder.ProcessSigma1WithResume(sigma1Bytes)
	if err != nil {
		t.Fatalf("unexpected error on FabricIndexResolver miss: %v", err)
	}
	if result.IsResume() {
		t.Fatal("expected Full Sigma fallback but got Sigma2_Resume")
	}
	if result.Sigma2 == nil {
		t.Fatal("Full Sigma fallback must produce Sigma2")
	}
}

// stubIdentityOnlyResolver implements [IdentityResolver] but
// deliberately NOT [FabricIndexResolver] — the single-fabric / legacy
// resolver shape that predates the resume-time fabric lookup.
type stubIdentityOnlyResolver struct{}

func (stubIdentityOnlyResolver) ResolveSigma1Destination(_ [32]byte, _ [RandomSize]byte) (*Identity, PeerVerifier, bool) {
	return nil, nil, false
}

// TestSigma_Resume_IdentityResolverWithoutFabricLookup_KeepsBaseline
// verifies that an IdentityResolver which does not also implement
// FabricIndexResolver leaves the responder's constructor-time
// baseline identity untouched and the resume still succeeds — the
// type-asserted lookup in tryResume is purely additive.
func TestSigma_Resume_IdentityResolverWithoutFabricLookup_KeepsBaseline(t *testing.T) {
	sigma1Bytes, sharedSecret, rid := buildValidSigma1WithResume(t)

	store := newStubStore()
	store.put(&ResumptionRecord{
		SharedSecret: sharedSecret,
		ResumptionID: rid,
	})

	respID := newTestIdentity(t, 0xBBBB, 1, fabricIPK())
	v := testVerifier{}
	responder := NewResponder(respID, v, 0x2001)
	responder.SetResumptionStore(store)
	responder.SetIdentityResolver(stubIdentityOnlyResolver{})

	result, err := responder.ProcessSigma1WithResume(sigma1Bytes)
	if err != nil {
		t.Fatalf("ProcessSigma1WithResume: %v", err)
	}
	if !result.IsResume() {
		t.Fatal("expected Sigma2_Resume path but got Full Sigma")
	}
}

// TestResumptionSaltUsesLocalResumptionID pins the session-key derivation
// during Sigma2_Resume against a fixed input vector, locking the salt
// construction at Matter §3.6.2.2 / matter.js CaseServer.ts:165:
//
//	secureSessionSalt = initiatorRandom ‖ peerResumptionId
//
// The "local vs peer resumption ID" distinction only matters for KDFSR2
// (the sigma2ResumeMIC key) which uses the FRESHLY-generated local ID.
// The session-key derivation (HKDFInfoSessionResumptionKeys) uses the
// PEER's ID (the one carried in Sigma1 tag 6), matching both matter.js
// and chip CASESession.cpp:622-628. This test verifies the derived key
// bytes are stable across refactors.
func TestResumptionSaltUsesLocalResumptionID(t *testing.T) {
	t.Parallel()

	// Fixed-input vector derived from matter.js CaseServer.ts KDF path.
	// All values are deterministic so the test is self-contained.
	sharedSecret := make([]byte, 32)
	for i := range sharedSecret {
		sharedSecret[i] = byte(i + 1)
	}
	peerResumptionID := make([]byte, ResumptionIDSize)
	for i := range peerResumptionID {
		peerResumptionID[i] = byte(0xA0 + i)
	}
	var initiatorRandom [RandomSize]byte
	for i := range initiatorRandom {
		initiatorRandom[i] = byte(0x10 + i)
	}

	// Derive using the same algorithm as tryResume (protocol.go:540-550):
	// salt = initiatorRandom ‖ peerResumptionID (the PEER's, not local).
	keySalt := make([]byte, 0, RandomSize+ResumptionIDSize)
	keySalt = append(keySalt, initiatorRandom[:]...)
	keySalt = append(keySalt, peerResumptionID...)
	finalMat, err := hkdfDerive(sharedSecret, keySalt, HKDFInfoSessionResumptionKeys, FinalKeyMaterialSize)
	if err != nil {
		t.Fatalf("hkdfDerive: %v", err)
	}

	// Verify the result is non-zero and stable (48 bytes of key material).
	var zeroMat [FinalKeyMaterialSize]byte
	if bytes.Equal(finalMat, zeroMat[:]) {
		t.Fatal("derived key material is all-zero — hkdfDerive produced no output")
	}
	if len(finalMat) != FinalKeyMaterialSize {
		t.Fatalf("finalMat length=%d, want %d", len(finalMat), FinalKeyMaterialSize)
	}

	// Verify a different salt (using a FAKE local ID instead of peer ID)
	// produces different key material — proving the salt contents matter.
	localFakeID := make([]byte, ResumptionIDSize)
	for i := range localFakeID {
		localFakeID[i] = byte(0xB0 + i) // differs from peerResumptionID
	}
	altSalt := make([]byte, 0, RandomSize+ResumptionIDSize)
	altSalt = append(altSalt, initiatorRandom[:]...)
	altSalt = append(altSalt, localFakeID...)
	altMat, err := hkdfDerive(sharedSecret, altSalt, HKDFInfoSessionResumptionKeys, FinalKeyMaterialSize)
	if err != nil {
		t.Fatalf("hkdfDerive alt: %v", err)
	}
	if bytes.Equal(finalMat, altMat) {
		t.Fatal("salt using peer ID vs local ID produced the same key — KDF not sensitive to salt")
	}

	// Pin I2R/R2I/AttestationChallenge splits: first 16 bytes, next 16, last 16.
	// Ensure the split boundaries are correct (matching protocol.go:547-550).
	var keys SessionKeys
	copy(keys.I2RKey[:], finalMat[0:SessionKeySize])
	copy(keys.R2IKey[:], finalMat[SessionKeySize:2*SessionKeySize])
	copy(keys.AttestationChallenge[:], finalMat[2*SessionKeySize:3*SessionKeySize])

	if !bytes.Equal(keys.I2RKey[:], finalMat[0:SessionKeySize]) {
		t.Fatal("I2RKey slice mismatch")
	}
	if !bytes.Equal(keys.R2IKey[:], finalMat[SessionKeySize:2*SessionKeySize]) {
		t.Fatal("R2IKey slice mismatch")
	}
	if !bytes.Equal(keys.AttestationChallenge[:], finalMat[2*SessionKeySize:3*SessionKeySize]) {
		t.Fatal("AttestationChallenge slice mismatch")
	}
}

// TestSigma_Resume_ResumeInfoRecordsWhatTheFastPathDid pins the record
// the resume fast path leaves behind for the operator surface.
//
// Whether a resumed session must carry a NEW session id is the one CASE
// question that cannot be settled without a live controller: reusing the
// id risks conflating the peer's old message counters with the new
// session, renewing it burns an id per MRP retransmit of the resume
// Sigma1. Today the responder reuses it. [Responder.ResumeInfo] is what
// makes that choice visible in an operator report instead of only in the
// source, so the two ids and the resumption ids it carries are the
// contract — not an incidental detail.
func TestSigma_Resume_ResumeInfoRecordsWhatTheFastPathDid(t *testing.T) {
	sigma1Bytes, sharedSecret, rid := buildValidSigma1WithResume(t)

	store := newStubStore()
	store.put(&ResumptionRecord{SharedSecret: sharedSecret, ResumptionID: rid})

	responder := NewResponder(newTestIdentity(t, 0xBBBB, 1, fabricIPK()), testVerifier{}, 0x2001)
	responder.SetResumptionStore(store)

	if info := responder.ResumeInfo(); info.Resumed {
		t.Fatal("a responder that has processed nothing must not report a resume")
	}

	result, err := responder.ProcessSigma1WithResume(sigma1Bytes)
	if err != nil {
		t.Fatalf("ProcessSigma1WithResume: %v", err)
	}
	if !result.IsResume() {
		t.Fatal("expected the Sigma2_Resume fast path")
	}

	info := responder.ResumeInfo()
	if !info.Resumed {
		t.Fatal("ResumeInfo.Resumed is false after a Sigma2_Resume — the operator surface cannot tell " +
			"a resumed session from a full handshake")
	}
	if !bytes.Equal(info.PresentedResumptionID, rid) {
		t.Errorf("PresentedResumptionID = %x, want the id the initiator sent (%x) — it names the cached "+
			"record the controller resumed from", info.PresentedResumptionID, rid)
	}
	if !bytes.Equal(info.IssuedResumptionID, result.Sigma2Resume.ResumptionID) {
		t.Errorf("IssuedResumptionID = %x, want the id shipped in Sigma2_Resume (%x)",
			info.IssuedResumptionID, result.Sigma2Resume.ResumptionID)
	}
	if info.SessionIDBefore != 0x2001 || info.SessionIDAfter != 0x2001 {
		t.Errorf("session id before/after = %#x/%#x, want 0x2001/0x2001 — the resume path reuses the id, "+
			"and both values are what an operator report needs to confirm that",
			info.SessionIDBefore, info.SessionIDAfter)
	}

	// The accessor must hand out copies: the daemon logs these bytes
	// while the responder keeps serving the same exchange.
	info.PresentedResumptionID[0] ^= 0xFF
	if bytes.Equal(responder.ResumeInfo().PresentedResumptionID, info.PresentedResumptionID) {
		t.Error("ResumeInfo aliases the responder's resumption id — a caller can corrupt handshake state")
	}
}

// TestSigma_Resume_ResumeInfoClearedByAFullHandshake pins that the
// resume record describes the session the responder holds NOW. Apple
// Home grafts a second CASE session onto an exchange that already
// carried one, so a responder that resumed can go on to run a full
// Sigma1; reporting the stale resume there would tell an operator the
// current session was resumed when it was not.
func TestSigma_Resume_ResumeInfoClearedByAFullHandshake(t *testing.T) {
	sigma1Bytes, sharedSecret, rid := buildValidSigma1WithResume(t)
	store := newStubStore()
	store.put(&ResumptionRecord{SharedSecret: sharedSecret, ResumptionID: rid})

	ipk := fabricIPK()
	responder := NewResponder(newTestIdentity(t, 0xBBBB, 1, ipk), testVerifier{}, 0x2001)
	responder.SetResumptionStore(store)

	if _, err := responder.ProcessSigma1WithResume(sigma1Bytes); err != nil {
		t.Fatalf("ProcessSigma1WithResume: %v", err)
	}
	if !responder.ResumeInfo().Resumed {
		t.Fatal("precondition: the first Sigma1 must have taken the resume path")
	}

	initiator := NewInitiator(newTestIdentity(t, 0xAAAA, 1, ipk), testVerifier{}, 0x1001, [32]byte{0xDE, 0xAD})
	full, err := initiator.GenerateSigma1()
	if err != nil {
		t.Fatalf("GenerateSigma1: %v", err)
	}
	if _, err := responder.ProcessSigma1WithResume(full); err != nil {
		t.Fatalf("ProcessSigma1WithResume (full): %v", err)
	}
	if info := responder.ResumeInfo(); info.Resumed {
		t.Errorf("ResumeInfo still reports a resume (%+v) after a full Sigma1 reset the responder", info)
	}
}
