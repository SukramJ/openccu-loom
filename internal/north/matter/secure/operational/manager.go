// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package operational

import (
	"context"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/channel"
	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/sigma"
	"github.com/SukramJ/openccu-loom/internal/north/matter/store"
)

// Manager maintains the live set of operational (CASE) sessions and
// brokers resumption-id persistence.
//
// Concurrency: every method is safe for concurrent use. Sessions are
// keyed by Matter session-id (uint16); the manager picks fresh IDs
// from the unused space of [1, 0xFFFE] and reuses the slot when a
// session closes. ID 0 is reserved by Matter for unsecured traffic.
type Manager struct {
	store ResumptionStore

	mu             sync.RWMutex
	sessions       map[uint16]*Entry
	nextID         uint16
	onSessionClose func(sessionID uint16)
}

// SetOnSessionClose registers a callback invoked once per session
// after its slot is removed from the manager. Used by the bridge to
// cascade cleanup into the Interaction-Model subscription manager so
// in-flight subscriptions tied to a now-defunct CASE session are
// terminated together with the session. Mirrors matter.js's
// `packages/protocol/src/session/SessionManager.ts` close-callback
// chain — without the cascade, subscriptions linger and the engine
// keeps shipping reports to a peer whose decryption keys are gone,
// which from Apple's side looks like duplicate noise and disables
// the HMOutlet UI update.
//
// Passing nil clears any prior hook. The callback fires outside the
// manager's lock; it MUST be re-entrant-safe with respect to its own
// state but may call back into other manager APIs safely.
func (m *Manager) SetOnSessionClose(fn func(sessionID uint16)) {
	m.mu.Lock()
	m.onSessionClose = fn
	m.mu.Unlock()
}

// fireOnSessionClose dispatches the close hook for every entry in
// entries. Caller MUST hold no manager locks. nil entries / nil hooks
// are no-ops.
func (m *Manager) fireOnSessionClose(entries []*Entry) {
	m.mu.RLock()
	hook := m.onSessionClose
	m.mu.RUnlock()
	if hook == nil {
		return
	}
	for _, e := range entries {
		if e == nil {
			continue
		}
		hook(e.SessionID)
	}
}

// ResumptionStore is the subset of [store.Store] the manager
// consumes. Defined as an interface so tests pass an in-memory fake.
type ResumptionStore interface {
	UpsertResumption(ctx context.Context, rec store.ResumptionRecord) error
	GetResumptionByID(ctx context.Context, resumptionID []byte) (store.ResumptionRecord, error)
	GetResumptionByPeer(ctx context.Context, fabricIndex uint8, peerNodeID uint64) (store.ResumptionRecord, error)
	RemoveResumption(ctx context.Context, fabricIndex uint8, peerNodeID uint64) error
}

// SessionIdleTimeout is the inactivity duration after which an
// operational session is eligible for eviction by the reaper.
// Mirrors matter.js packages/protocol/src/session/Session.ts
// SESSION_IDLE_INTERVAL_MS (default 5000 ms, but the session-
// eviction poll in OpenCCU-Loom uses a more conservative 5 minutes
// to avoid churning short-lived idle windows between Apple Home
// subscription reports which arrive every 30–60 s).
//
// Per Matter §4.13.2.6.1 the spec defines SessionIdleInterval and
// SessionActiveInterval; our reaper uses a single conservative
// threshold that covers both subscriber-idle and full-session-idle
// scenarios without tracking the CASE vs. PASE state machine.
const SessionIdleTimeout = 5 * time.Minute

// Entry pairs a [channel.Session] with its operational metadata.
type Entry struct {
	// SessionID is the local 16-bit session identifier carried in
	// every encrypted Message Header.
	SessionID uint16
	// FabricIndex is the fabric this session belongs to. The bridge
	// uses it to scope Read / Write / Invoke against fabric-scoped
	// attributes (e.g. ACL).
	FabricIndex uint8
	// Session encrypts/decrypts messages for this peer.
	Session *channel.Session
	// AttestationChallenge is the 16-byte HKDF-derived challenge
	// chip-tool's commissioning flow signs over together with the
	// AttestationElements (Matter §11.18.4.7) and NOCSRElements
	// (§11.18.7.6). For PASE it's the third 16-byte slice of the
	// HKDF-SHA256(IKM=Ke, info="SessionKeys") output; for CASE it
	// comes from the Sigma key schedule.
	AttestationChallenge []byte
	// lastActivity is updated on every inbound Decrypt and outbound
	// Encrypt call so the reaper can evict long-idle entries.
	// Mirrors chip src/transport/SecureSession.h:240-248 MarkActive /
	// MarkActiveRx.
	lastActivity time.Time
	// mu guards lastActivity for concurrent Rx/Tx updates.
	mu sync.Mutex
}

// MarkActiveRx updates the entry's activity timestamp after a
// successful inbound message decryption.
func (e *Entry) MarkActiveRx() {
	e.mu.Lock()
	e.lastActivity = time.Now()
	e.mu.Unlock()
}

// MarkActiveTx updates the entry's activity timestamp after a
// successful outbound message encryption.
func (e *Entry) MarkActiveTx() {
	e.mu.Lock()
	e.lastActivity = time.Now()
	e.mu.Unlock()
}

// LastActivity returns the last time this session exchanged a message.
// Returns the zero time for entries that have never been used (e.g.
// pre-allocated placeholders).
func (e *Entry) LastActivity() time.Time {
	e.mu.Lock()
	t := e.lastActivity
	e.mu.Unlock()
	return t
}

// Errors.
var (
	// ErrSessionExhausted is returned when [1, 0xFFFE] is fully
	// occupied — pathological case, the bridge would never reach it
	// in practice.
	ErrSessionExhausted = errors.New("operational: session id space exhausted")
	// ErrSessionNotFound is returned by [Manager.Get] / [Manager.Close]
	// when no session matches the supplied id.
	ErrSessionNotFound = errors.New("operational: session not found")
)

// NewManager returns a manager backed by s.
func NewManager(s ResumptionStore) *Manager {
	return &Manager{
		store:    s,
		sessions: make(map[uint16]*Entry),
		nextID:   1,
	}
}

// OpenFromSigma constructs a session from the Sigma key-derivation
// output and registers it under a freshly allocated session id.
//
// localNodeID is the bridge's NodeID inside fabricIndex; peerNodeID
// is the commissioner's node identifier extracted from the verified
// peer NOC.
//
// Stale-session eviction: any pre-existing session for the same
// (fabricIndex, peerNodeID) pair is closed before the new entry is
// installed. Apple iOS' Matter daemon caches old session keys across
// daemon restarts and replays them with counter values way above our
// in-memory window's high-water mark; without eviction the bridge
// keeps decrypting against the stale entry, fails authentication,
// and the commissioner aborts the new pair attempt with INVALID_PARAMETER.
// Mirrors matter.js packages/protocol/src/session/SessionManager.ts:
// removeAllSessionsForNode (called on every CASE establishment).
func (m *Manager) OpenFromSigma(fabricIndex uint8, localNodeID, peerNodeID uint64, keys sigma.SessionKeys) (*Entry, error) {
	sess, err := channel.New(channel.Config{
		EncryptKey:  keys.R2IKey[:], // bridge → peer
		DecryptKey:  keys.I2RKey[:], // peer → bridge
		LocalNodeID: localNodeID,
		PeerNodeID:  peerNodeID,
	})
	if err != nil {
		return nil, fmt.Errorf("operational: build session: %w", err)
	}

	m.mu.Lock()
	stale := m.collectStalePeerSessionsLocked(fabricIndex, peerNodeID)
	id, err := m.allocateIDLocked()
	if err != nil {
		m.mu.Unlock()
		sess.Close()
		return nil, err
	}
	entry := &Entry{
		SessionID:   id,
		FabricIndex: fabricIndex,
		Session:     sess,
	}
	m.sessions[id] = entry
	m.mu.Unlock()
	closeStaleEntries(stale)
	m.fireOnSessionClose(stale)
	return entry, nil
}

// AllocateID reserves the next-available session id without
// constructing a Session under it. The caller is responsible for
// passing the same id back to [Manager.OpenFromSigmaWithID] before
// the slot is reaped — and for releasing the slot via [Manager.ReleaseID]
// if the handshake aborts before reaching key derivation.
//
// CASE responders need to know their allocated id BEFORE Sigma2 is
// built, because Sigma2.responderSessionID is what the commissioner
// echoes back as `dest.SessionID` on every operational packet.
// Hard-coding "1" (the previous behaviour) would collide every
// parallel CASE session into a single lookup slot and break peer →
// bridge routing.
func (m *Manager) AllocateID() (uint16, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, err := m.allocateIDLocked()
	if err != nil {
		return 0, err
	}
	// Stake a placeholder so concurrent allocators do not hand the
	// same id out twice; OpenFromSigmaWithID overwrites it.
	m.sessions[id] = &Entry{SessionID: id}
	return id, nil
}

// ReleaseID frees a previously allocated id when the handshake that
// reserved it never reached OpenFromSigmaWithID.
func (m *Manager) ReleaseID(id uint16) {
	m.mu.Lock()
	if e, ok := m.sessions[id]; ok && e.Session == nil {
		delete(m.sessions, id)
	}
	m.mu.Unlock()
}

// OpenFromSigmaWithID is a variant of [Manager.OpenFromSigma] that
// registers the new session under a pre-allocated id (see
// [Manager.AllocateID]). It overwrites any placeholder entry left
// by AllocateID.
//
// peerSessionID is the InitiatorSessionID the commissioner sent in
// Sigma1 — every outbound encrypted reply must carry this in
// Header.SessionID so the peer can resolve the session in its own
// table. Without it Apple Home / chip-tool drops every reply.
//
// peerCATs is the set of CASE Authenticated Tags lifted out of the
// peer's NOC subject at Sigma3 verification. Stored on the session
// so the IM dispatcher's ACL gate can match CAT-bearing ACEs (Matter
// §9.10.5.6). Pass nil when the peer NOC carried no CATs.
func (m *Manager) OpenFromSigmaWithID(id uint16, fabricIndex uint8, localNodeID, peerNodeID uint64, peerSessionID uint16, peerCATs []uint32, keys sigma.SessionKeys) (*Entry, error) {
	sess, err := channel.New(channel.Config{
		EncryptKey:    keys.R2IKey[:],
		DecryptKey:    keys.I2RKey[:],
		LocalNodeID:   localNodeID,
		PeerNodeID:    peerNodeID,
		PeerCATs:      peerCATs,
		PeerSessionID: peerSessionID,
	})
	if err != nil {
		return nil, fmt.Errorf("operational: build session: %w", err)
	}
	// AttestationChallenge is the third 16-byte slice of the 48-byte
	// HKDF-SHA256(IKM=sharedSecret, salt=sessionKeysSalt, info="SessionKeys")
	// output produced by the Sigma key schedule.
	// Mirrors matter.js packages/protocol/src/session/NodeSession.ts:80 —
	// `const attestationKey = keys.slice(32, 48)` — and
	// chip CASESession.cpp:615 — derived from the same 48-byte block.
	// OpenFromPase sets this identically (manager.go:241); CASE must match.
	entry := &Entry{
		SessionID:            id,
		FabricIndex:          fabricIndex,
		Session:              sess,
		AttestationChallenge: append([]byte(nil), keys.AttestationChallenge[:]...),
	}
	m.mu.Lock()
	stale := m.collectStalePeerSessionsLocked(fabricIndex, peerNodeID)
	// The pre-allocated id is in m.sessions as a placeholder Entry
	// (Session==nil) — collectStalePeerSessionsLocked filters those
	// out, so the placeholder survives. Overwrite it with the real
	// entry next.
	m.sessions[id] = entry
	m.mu.Unlock()
	closeStaleEntries(stale)
	m.fireOnSessionClose(stale)
	return entry, nil
}

// OpenFromPase constructs a PASE session from the Spake2+ shared
// secret (Ke) and registers it under a freshly allocated session id.
// PASE keys are derived via HKDF-SHA256(IKM=Ke, salt="", info="SessionKeys", L=48)
// per Matter §4.13.4.2 — the resulting 48 bytes split into
// I2RKey (16) || R2IKey (16) || AttestationChallenge (16).
//
// PASE sessions never carry a fabric (commissioning happens before
// the operational fabric exists). The Entry's FabricIndex stays 0
// to signal that; callers that need to gate fabric-scoped operations
// must check for FabricIndex==0.
func (m *Manager) OpenFromPase(localNodeID, peerNodeID uint64, peerSessionID uint16, sharedSecret []byte) (*Entry, error) {
	if len(sharedSecret) == 0 {
		return nil, errors.New("operational: empty PASE shared secret")
	}
	derived, err := hkdf.Key(sha256.New, sharedSecret, nil, "SessionKeys", 48)
	if err != nil {
		return nil, fmt.Errorf("operational: PASE hkdf: %w", err)
	}
	sess, err := channel.New(channel.Config{
		EncryptKey:    derived[16:32], // R2IKey — bridge → peer (responder→initiator)
		DecryptKey:    derived[0:16],  // I2RKey — peer → bridge
		LocalNodeID:   localNodeID,
		PeerNodeID:    peerNodeID,
		PeerSessionID: peerSessionID,
	})
	if err != nil {
		return nil, fmt.Errorf("operational: build PASE session: %w", err)
	}
	m.mu.Lock()
	id, err := m.allocateIDLocked()
	if err != nil {
		m.mu.Unlock()
		sess.Close()
		return nil, err
	}
	entry := &Entry{
		SessionID:            id,
		FabricIndex:          0, // PASE pre-dates the operational fabric
		Session:              sess,
		AttestationChallenge: append([]byte(nil), derived[32:48]...),
	}
	m.sessions[id] = entry
	m.mu.Unlock()
	return entry, nil
}

// OpenFromPaseWithID is a variant of [Manager.OpenFromPase] that
// registers the new PASE session under a pre-allocated id (see
// [Manager.AllocateID]). It overwrites any placeholder entry left by
// AllocateID.
//
// Use this when the responder session id must be known BEFORE the
// PBKDFParamResponse is sent — the commissioner echoes it back as
// Header.SessionID on every post-PASE-establishment packet (IM reads,
// writes, invokes). Pre-allocating the id and embedding it in the
// PBKDFParamResponse guarantees the bridge's session lookup resolves
// the correct entry for every subsequent datagram, even when other
// sessions occupy the lower-numbered slots.
func (m *Manager) OpenFromPaseWithID(id uint16, localNodeID, peerNodeID uint64, peerSessionID uint16, sharedSecret []byte) (*Entry, error) {
	if len(sharedSecret) == 0 {
		return nil, errors.New("operational: empty PASE shared secret")
	}
	derived, err := hkdf.Key(sha256.New, sharedSecret, nil, "SessionKeys", 48)
	if err != nil {
		return nil, fmt.Errorf("operational: PASE hkdf: %w", err)
	}
	sess, err := channel.New(channel.Config{
		EncryptKey:    derived[16:32], // R2IKey — bridge → peer (responder→initiator)
		DecryptKey:    derived[0:16],  // I2RKey — peer → bridge
		LocalNodeID:   localNodeID,
		PeerNodeID:    peerNodeID,
		PeerSessionID: peerSessionID,
	})
	if err != nil {
		return nil, fmt.Errorf("operational: build PASE session: %w", err)
	}
	entry := &Entry{
		SessionID:            id,
		FabricIndex:          0, // PASE pre-dates the operational fabric
		Session:              sess,
		AttestationChallenge: append([]byte(nil), derived[32:48]...),
	}
	m.mu.Lock()
	// The pre-allocated id is in m.sessions as a placeholder Entry
	// (Session==nil). Overwrite it with the real entry.
	m.sessions[id] = entry
	m.mu.Unlock()
	return entry, nil
}

// Get looks up a session by id.
func (m *Manager) Get(sessionID uint16) (*Entry, error) {
	m.mu.RLock()
	entry, ok := m.sessions[sessionID]
	m.mu.RUnlock()
	if !ok {
		return nil, ErrSessionNotFound
	}
	return entry, nil
}

// AdoptFabricIndex rewrites the FabricIndex on the session identified
// by sessionID. Used by the OperationalCredentials cluster's
// OnFabricInstalled hook: after a successful AddNOC the PASE session
// the commissioner used to send the command must "become" the new
// fabric so the subsequent IM commands (CommissioningComplete, ACL
// reads, group-key writes) find an accessing fabric.
//
// Mirrors chip's `SecureSession::AdoptFabricIndex(newFabricIndex)`
// invoked at OperationalCredentialsCluster.cpp:510-514 BEFORE the
// ACL replace. Without the adoption Apple's commissioner sends
// CommissioningComplete on the same session, the bridge sees
// FabricIndex=0 (PASE) for an access check that requires the just-
// installed fabric, and the operation fails with InvalidAuthentication
// — Apple aborts the pair attempt with `MTRErrorDomain Code=9
// "System Commissioner Pairing — Completing"`.
//
// Returns [ErrSessionNotFound] when no session matches sessionID.
// Idempotent: setting the same FabricIndex twice is a no-op.
func (m *Manager) AdoptFabricIndex(sessionID uint16, newFabricIndex uint8) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.sessions[sessionID]
	if !ok {
		return ErrSessionNotFound
	}
	entry.FabricIndex = newFabricIndex
	return nil
}

// Close terminates a session and frees its id slot.
func (m *Manager) Close(sessionID uint16) error {
	m.mu.Lock()
	entry, ok := m.sessions[sessionID]
	if !ok {
		m.mu.Unlock()
		return ErrSessionNotFound
	}
	delete(m.sessions, sessionID)
	m.mu.Unlock()
	entry.Session.Close()
	m.fireOnSessionClose([]*Entry{entry})
	return nil
}

// CloseFabric tears down every session associated with fabricIndex.
// Used when a fabric is removed via OperationalCredentials.RemoveFabric.
func (m *Manager) CloseFabric(fabricIndex uint8) {
	m.mu.Lock()
	victims := make([]*Entry, 0)
	for id, entry := range m.sessions {
		if entry.FabricIndex == fabricIndex {
			victims = append(victims, entry)
			delete(m.sessions, id)
		}
	}
	m.mu.Unlock()
	closeStaleEntries(victims)
	m.fireOnSessionClose(victims)
}

// CloseFabricExcept tears down every session on fabricIndex except the one
// whose local session ID equals exceptSessionID. UpdateNOC must abort all
// OTHER CASE sessions on the rotated fabric while preserving the invoking
// session, so the NOCResponse still reaches the wire and the commissioner can
// re-CASE on that same session. Closing the invoking session here would drop
// its own response (the reply lookup fails on the deleted session) and the
// commissioner would time out. Mirrors chip
// FabricTable::AbortAllOtherCommunicationOnFabric, which pins the invoking
// exchange's session. exceptSessionID==0 falls back to closing every session
// on the fabric — no operational session is keyed under ID 0.
func (m *Manager) CloseFabricExcept(fabricIndex uint8, exceptSessionID uint16) {
	m.mu.Lock()
	victims := make([]*Entry, 0)
	for id, entry := range m.sessions {
		if entry.FabricIndex == fabricIndex && id != exceptSessionID {
			victims = append(victims, entry)
			delete(m.sessions, id)
		}
	}
	m.mu.Unlock()
	closeStaleEntries(victims)
	m.fireOnSessionClose(victims)
}

// ClosePeer tears down every live session matching the
// (fabricIndex, peerNodeID) pair. Returns the number of sessions
// closed. Exposed so an operator (or a counter-jump heuristic) can
// invalidate a stale session table on demand. Mirrors matter.js
// packages/protocol/src/session/SessionManager.ts:removeAllSessionsForNode.
func (m *Manager) ClosePeer(fabricIndex uint8, peerNodeID uint64) int {
	m.mu.Lock()
	victims := make([]*Entry, 0)
	for id, entry := range m.sessions {
		if entry.Session == nil {
			// Pre-allocated id placeholder — the handshake that owns
			// it has not reached OpenFromSigmaWithID yet. Leaving the
			// placeholder in place is harmless: the placeholder has
			// no key material, so an inbound packet for it cannot
			// authenticate.
			continue
		}
		if entry.FabricIndex == fabricIndex && entry.Session.PeerNodeID() == peerNodeID {
			victims = append(victims, entry)
			delete(m.sessions, id)
		}
	}
	m.mu.Unlock()
	closeStaleEntries(victims)
	m.fireOnSessionClose(victims)
	return len(victims)
}

// collectStalePeerSessionsLocked finds sessions matching
// (fabricIndex, peerNodeID) and removes them from the live map.
// Caller MUST hold m.mu in write mode and MUST close the returned
// entries OUTSIDE the lock — Session.Close acquires its own
// privacy mutex and we keep the operational lock scope minimal.
//
// Pre-allocated placeholders (Entry.Session==nil) are left
// untouched: the handshake that allocated them is mid-flight and
// will overwrite the placeholder on completion. Closing them here
// would also free their session-id slot, allocating which to a
// subsequent session is racy because the in-flight handshake still
// holds the id externally.
func (m *Manager) collectStalePeerSessionsLocked(fabricIndex uint8, peerNodeID uint64) []*Entry {
	var stale []*Entry
	for id, entry := range m.sessions {
		if entry.Session == nil {
			continue
		}
		if entry.FabricIndex == fabricIndex && entry.Session.PeerNodeID() == peerNodeID {
			stale = append(stale, entry)
			delete(m.sessions, id)
		}
	}
	return stale
}

// closeStaleEntries shuts down each session outside any lock. Safe
// against the empty / nil slice — turning the eviction call sites
// into one-liners.
func closeStaleEntries(entries []*Entry) {
	for _, e := range entries {
		if e == nil || e.Session == nil {
			continue
		}
		e.Session.Close()
	}
}

// Active returns the count of live sessions.
func (m *Manager) Active() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.sessions)
}

// StartReaper launches a background goroutine that evicts sessions
// idle for longer than idleTimeout. The goroutine stops when ctx is
// cancelled. poll controls how often the sweep runs; a reasonable
// default is idleTimeout/2.
//
// Sessions with a nil channel (pre-allocated placeholders whose
// handshake has not completed) and sessions where LastActivity is the
// zero time (freshly opened, no traffic yet) are skipped so that
// in-progress commissioning handshakes are not torn down.
//
// Mirrors the session-eviction logic in matter.js
// packages/protocol/src/session/SessionManager.ts and the
// SessionInactiveBaseInterval defined in Matter §4.13.2.6.1.
func (m *Manager) StartReaper(ctx context.Context, idleTimeout, poll time.Duration) {
	go func() {
		ticker := time.NewTicker(poll)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.reapIdle(idleTimeout)
			}
		}
	}()
}

// reapIdle collects and closes sessions whose last-activity timestamp
// is older than idleTimeout. Only entries with an established channel
// and a non-zero last-activity time are considered.
func (m *Manager) reapIdle(idleTimeout time.Duration) {
	cutoff := time.Now().Add(-idleTimeout)
	m.mu.Lock()
	var victims []*Entry
	for id, e := range m.sessions {
		if e.Session == nil {
			continue // placeholder — handshake in progress
		}
		la := e.LastActivity()
		if la.IsZero() {
			continue // never used — commissioning may still be active
		}
		if la.Before(cutoff) {
			victims = append(victims, e)
			delete(m.sessions, id)
		}
	}
	m.mu.Unlock()
	closeStaleEntries(victims)
	m.fireOnSessionClose(victims)
}

// PersistResumption stores a resumption-id pre-shared secret so a
// returning peer can resume via Sigma1.ResumptionID. resumptionID
// must be 16 bytes; sharedSecret must be 32 bytes. Wired in daemon.go
// (matter init) and called from both CASE-onEstablished paths after
// every successful OpenFromSigmaWithID. Mirrors matter.js
// packages/protocol/src/session/case/CaseServer.ts:210
// `this.#sessions.saveResumptionRecord(cx.resumptionRecord)`.
func (m *Manager) PersistResumption(ctx context.Context, fabricIndex uint8, peerNodeID uint64, resumptionID, sharedSecret []byte) error {
	return m.store.UpsertResumption(ctx, store.ResumptionRecord{
		FabricIndex:  fabricIndex,
		PeerNodeID:   peerNodeID,
		ResumptionID: resumptionID,
		SharedSecret: sharedSecret,
	})
}

// LookupResumption finds the resumption record by id only.
func (m *Manager) LookupResumption(ctx context.Context, resumptionID []byte) (store.ResumptionRecord, error) {
	return m.store.GetResumptionByID(ctx, resumptionID)
}

// GenerateResumptionID returns a fresh 16-byte random id from
// crypto/rand. Production callers use this to assign Sigma2's
// ResumptionID field.
func GenerateResumptionID() ([]byte, error) {
	out := make([]byte, 16)
	if _, err := rand.Read(out); err != nil {
		return nil, fmt.Errorf("operational: generate resumption id: %w", err)
	}
	return out, nil
}

// allocateIDLocked walks 1..0xFFFE for the next free session id. The
// caller must hold m.mu in write mode.
func (m *Manager) allocateIDLocked() (uint16, error) {
	const maxID = uint16(0xFFFE)
	for i := 0; i < int(maxID); i++ {
		id := m.nextID
		m.nextID++
		if m.nextID == 0 || m.nextID > maxID {
			m.nextID = 1
		}
		if _, in := m.sessions[id]; !in && id != 0 {
			return id, nil
		}
	}
	return 0, ErrSessionExhausted
}
