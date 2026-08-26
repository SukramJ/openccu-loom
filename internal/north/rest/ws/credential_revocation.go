// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package ws

// SessionRevoking is the HTTP half of a credential revocation: the
// server-side sessions a subject holds. *auth.SessionStore satisfies it,
// and so does the REST layer's SessionRevoker port, which is what lets a
// revoker be decorated on its way into the router.
type SessionRevoking interface {
	// RevokeBySubject drops every session of subject and reports how many
	// it dropped.
	RevokeBySubject(subject string) int
	// RevokeBySubjectExcept is RevokeBySubject but preserves keepSID — the
	// self-service password change keeps the caller's own session.
	RevokeBySubjectExcept(subject, keepSID string) int
}

// socketCloser is the WebSocket half. [*Hub] satisfies it.
type socketCloser interface {
	CloseBySubject(subject string) int
}

// RevokeWithSockets returns a revoker that performs the session
// revocation and then closes the subject's open WebSocket connections.
//
// Revocation used to stop at the HTTP boundary: sessions and bearer
// tokens were invalidated while an already-open socket kept dispatching
// commands under the identity it captured at upgrade, so a demoted or
// deleted principal retained admin-tier writes for the life of that
// connection. The two halves belong to one operation, which is why they
// are composed here rather than left to each call site to remember.
//
// A nil hub returns sessions unchanged, so a daemon without a live
// WebSocket surface keeps the plain session behaviour.
func RevokeWithSockets(sessions SessionRevoking, hub *Hub) SessionRevoking {
	if hub == nil {
		return sessions
	}
	return sessionAndSocketRevoker{sessions: sessions, sockets: hub}
}

// sessionAndSocketRevoker is the composed revoker [RevokeWithSockets]
// returns.
type sessionAndSocketRevoker struct {
	sessions SessionRevoking
	sockets  socketCloser
}

// RevokeBySubject revokes the subject's sessions and closes its sockets.
// The returned count stays the session count so the REST callers' logging
// and audit semantics are unchanged.
func (r sessionAndSocketRevoker) RevokeBySubject(subject string) int {
	n := 0
	if r.sessions != nil {
		n = r.sessions.RevokeBySubject(subject)
	}
	r.sockets.CloseBySubject(subject)
	return n
}

// RevokeBySubjectExcept revokes every session but keepSID, and closes the
// subject's sockets. The kept session is an HTTP session id, which a
// socket does not carry, so the sockets go too — a password change is
// exactly the event after which a connection must re-present credentials,
// and the caller's browser simply reconnects with its surviving cookie.
func (r sessionAndSocketRevoker) RevokeBySubjectExcept(subject, keepSID string) int {
	n := 0
	if r.sessions != nil {
		n = r.sessions.RevokeBySubjectExcept(subject, keepSID)
	}
	r.sockets.CloseBySubject(subject)
	return n
}
