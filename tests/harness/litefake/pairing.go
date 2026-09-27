// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package litefake

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Client pairing.
//
// An app asks the box for a token with the access levels it wants; the
// box shows the request to an administrator together with a six-digit
// code both sides compute, and the app polls until the request is
// approved (the token is handed out once), rejected or expired. The
// fake plays the administrator through [Fake.ApprovePairing] and
// [Fake.RejectPairing].

// Defaults of the pairing flow.
const (
	DefaultPairingLifetime     = 5 * time.Minute
	DefaultPairingKeep         = time.Minute
	DefaultPairingPollInterval = 2 * time.Second
	pairingMaxWait             = 30 * time.Second
	pairingPendingTotal        = 5
	pairingPerHour             = 10
	pairingMute                = 10 * time.Minute
	pairingTokenNameMax        = 28
)

// Pairing states reported to the polling app.
const (
	pairingPending  = "pending"
	pairingApproved = "approved"
	pairingRejected = "rejected"
	pairingExpired  = "expired"
)

// PairingAccess is the access an app asks for, one level per area.
type PairingAccess struct {
	Devices string `json:"devices,omitempty"`
	Names   string `json:"names,omitempty"`
	System  string `json:"system,omitempty"`
}

// pairingRequestBody is the strictly decoded request body.
type pairingRequestBody struct {
	App        string            `json:"app"`
	AppVersion string            `json:"app_version"`
	Instance   string            `json:"instance"`
	Name       string            `json:"name"`
	Access     PairingAccess     `json:"access"`
	Purpose    map[string]string `json:"purpose"`
	Commit     string            `json:"commit"`
}

// pairingRequest is one request as the box holds it.
type pairingRequest struct {
	id         string
	poll       string
	nonce      []byte
	commit     []byte
	body       pairingRequestBody
	address    string
	created    time.Time
	lastPoll   time.Time
	clientNon  []byte
	state      string
	token      string
	tokenName  string
	scopes     []string
	handedOut  bool
	changed    chan struct{}
	notifyOnce *sync.Once
}

// notify wakes every long poll waiting on the request.
func (p *pairingRequest) notify() {
	p.notifyOnce.Do(func() { close(p.changed) })
	p.changed = make(chan struct{})
	p.notifyOnce = &sync.Once{}
}

// pairingState is the box's pairing bookkeeping.
type pairingState struct {
	mu       sync.Mutex
	disabled bool
	lifetime time.Duration
	keep     time.Duration
	interval time.Duration
	requests map[string]*pairingRequest
	history  map[string][]time.Time
	muted    map[string]time.Time
	// fingerprint is the SHA-256 of the API certificate with
	// [Options.TLS]; nil over plain HTTP.
	fingerprint []byte
}

func newPairingState(o Options) *pairingState {
	return &pairingState{
		disabled: o.PairingDisabled,
		lifetime: o.PairingLifetime,
		keep:     o.PairingKeep,
		interval: o.PairingPollInterval,
		requests: map[string]*pairingRequest{},
		history:  map[string][]time.Time{},
		muted:    map[string]time.Time{},
	}
}

var (
	appPattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,47}$`)
	commitPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// PairingScopes maps requested access levels to token scopes. Each
// level includes the ones below it; the scopes a pairing may never
// grant (*, auth:admin, radio:keys, power, backup) are in no level.
func PairingScopes(a PairingAccess) ([]string, error) {
	var out []string
	switch a.Devices {
	case "":
	case "read":
		out = append(out, "rpc:read")
	case "operate":
		out = append(out, "rpc:read", "rpc:operate")
	case "configure":
		out = append(out, "rpc:read", "rpc:operate", "rpc:configure")
	case "administer":
		out = append(out, "rpc:read", "rpc:operate", "rpc:configure", "rpc:admin")
	default:
		return nil, fmt.Errorf("unknown devices level %q", a.Devices)
	}
	switch a.Names {
	case "":
	case "read":
		out = append(out, "meta:read")
	case "configure":
		out = append(out, "meta:read", "meta:write")
	default:
		return nil, fmt.Errorf("unknown names level %q", a.Names)
	}
	switch a.System {
	case "":
	case "read":
		out = append(out, "system:read", "logs:read")
	case "configure":
		out = append(out, "system:read", "logs:read", "system:write")
	default:
		return nil, fmt.Errorf("unknown system level %q", a.System)
	}
	if len(out) == 0 {
		return nil, errors.New("at least one access area is required")
	}
	return out, nil
}

// PairingCode computes the six-digit code from the box nonce, the
// client nonce and the certificate fingerprint bytes the client saw
// (empty over plain HTTP): the first four bytes of
// SHA-256(nonce ‖ client_nonce ‖ fingerprint) as a big-endian uint32,
// modulo one million, zero-padded to six digits.
func PairingCode(nonce, clientNonce, fingerprint []byte) string {
	h := sha256.New()
	h.Write(nonce)
	h.Write(clientNonce)
	h.Write(fingerprint)
	sum := h.Sum(nil)
	return fmt.Sprintf("%06d", binary.BigEndian.Uint32(sum[:4])%1_000_000)
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func remoteHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (f *Fake) pairingRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/auth/v1/pairing/request", f.handlePairingRequest)
	mux.HandleFunc("GET /api/auth/v1/pairing/request/{id}", f.handlePairingPoll)
	mux.HandleFunc("DELETE /api/auth/v1/pairing/request/{id}", f.handlePairingDelete)
	mux.HandleFunc("POST /api/auth/v1/pairing/{id}/approve", f.handlePairingApprove)
}

// pairingAnswer is the 202 answer to a pairing request.
type pairingAnswer struct {
	ID          string `json:"id"`
	Poll        string `json:"poll"`
	Nonce       string `json:"nonce"`
	ExpiresIn   int    `json:"expires_in"`
	Interval    int    `json:"interval"`
	Fingerprint string `json:"fingerprint"`
}

// validate applies the request body rules.
func (b *pairingRequestBody) validate() error {
	switch {
	case !appPattern.MatchString(b.App):
		return errors.New("invalid app")
	case len(b.AppVersion) > 32:
		return errors.New("app_version is too long")
	case len(b.Instance) > 64:
		return errors.New("instance is too long")
	case len(b.Name) > 80:
		return errors.New("name is too long")
	case !commitPattern.MatchString(b.Commit):
		return errors.New("commit must be 64 hex characters")
	}
	for area, text := range b.Purpose {
		if area != "devices" && area != "names" && area != "system" {
			return fmt.Errorf("unknown purpose area %q", area)
		}
		if len(text) > 200 {
			return fmt.Errorf("purpose of %s is too long", area)
		}
	}
	_, err := PairingScopes(b.Access)
	return err
}

// expireLocked updates request states against the clock and forgets
// requests past their keep time.
func (p *pairingState) expireLocked(now time.Time) {
	for id, req := range p.requests {
		if now.After(req.created.Add(p.lifetime + p.keep)) {
			delete(p.requests, id)
			continue
		}
		if req.state == pairingPending && now.After(req.created.Add(p.lifetime)) {
			req.state = pairingExpired
			req.notify()
		}
	}
}

// handlePairingRequest answers the open POST /api/auth/v1/pairing/request.
func (f *Fake) handlePairingRequest(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20+1))
	if err != nil || len(raw) > 1<<20 {
		writeError(w, http.StatusUnprocessableEntity, "invalid", "unreadable body")
		return
	}
	var body pairingRequestBody
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid", err.Error())
		return
	}
	if err := body.validate(); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid", err.Error())
		return
	}
	if body.Name == "" {
		body.Name = body.App + " on " + body.Instance
	}
	commit, _ := hex.DecodeString(body.Commit)
	addr := remoteHost(r)
	key := addr + "\x00" + body.App
	now := time.Now()

	ps := f.pairing
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if ps.disabled {
		writeError(w, http.StatusForbidden, "pairing-off", "pairing is switched off")
		return
	}
	ps.expireLocked(now)
	pending := 0
	for _, req := range ps.requests {
		if req.state != pairingPending {
			continue
		}
		pending++
		if req.address+"\x00"+req.body.App == key {
			writePairingLimit(w, "a request of this app is already pending")
			return
		}
	}
	if pending >= pairingPendingTotal {
		writePairingLimit(w, "too many pending requests")
		return
	}
	if until, ok := ps.muted[key]; ok && now.Before(until) {
		writePairingLimit(w, "this app is muted after a rejection")
		return
	}
	recent := ps.history[addr][:0:0]
	for _, t := range ps.history[addr] {
		if now.Sub(t) < time.Hour {
			recent = append(recent, t)
		}
	}
	if len(recent) >= pairingPerHour {
		writePairingLimit(w, "too many requests from this address")
		return
	}
	ps.history[addr] = append(recent, now)

	req := &pairingRequest{
		id:         randomHex(8),
		poll:       randomHex(24),
		body:       body,
		commit:     commit,
		address:    addr,
		created:    now,
		state:      pairingPending,
		changed:    make(chan struct{}),
		notifyOnce: &sync.Once{},
	}
	req.nonce, _ = hex.DecodeString(randomHex(16))
	ps.requests[req.id] = req
	writeJSON(w, http.StatusAccepted, pairingAnswer{
		ID:        req.id,
		Poll:      req.poll,
		Nonce:     hex.EncodeToString(req.nonce),
		ExpiresIn: int(ps.lifetime / time.Second),
		Interval:  max(1, int(ps.interval/time.Second)),
		// Empty over plain HTTP.
		Fingerprint: hex.EncodeToString(ps.fingerprint),
	})
}

func writePairingLimit(w http.ResponseWriter, msg string) {
	w.Header().Set("Retry-After", "60")
	writeError(w, http.StatusTooManyRequests, "limit", msg)
}

// pollAnswer is the poll answer; an approval carries the token once.
type pollAnswer struct {
	State  string         `json:"state"`
	Token  string         `json:"token,omitempty"`
	Name   string         `json:"name,omitempty"`
	Scopes []string       `json:"scopes,omitempty"`
	Access *PairingAccess `json:"access,omitempty"`
}

// pairingAuth checks the "Authorization: Pairing <poll>" header of a
// request; it writes the refusal and returns nil on failure. The lock
// is held on success.
func (f *Fake) pairingAuth(w http.ResponseWriter, r *http.Request) *pairingRequest {
	ps := f.pairing
	ps.mu.Lock()
	ps.expireLocked(time.Now())
	req, ok := ps.requests[r.PathValue("id")]
	if !ok {
		ps.mu.Unlock()
		writeError(w, http.StatusNotFound, "not_found", "no such pairing request")
		return nil
	}
	scheme, secret, _ := strings.Cut(r.Header.Get("Authorization"), " ")
	if !strings.EqualFold(scheme, "Pairing") || secret != req.poll {
		ps.mu.Unlock()
		writeError(w, http.StatusForbidden, "forbidden", "wrong poll secret")
		return nil
	}
	return req
}

// handlePairingPoll answers GET /api/auth/v1/pairing/request/{id}. The
// first poll must reveal the client nonce matching the commit; wait
// long-polls up to 30 s for a state change; without wait, polls faster
// than the interval are told to slow down.
func (f *Fake) handlePairingPoll(w http.ResponseWriter, r *http.Request) {
	req := f.pairingAuth(w, r)
	if req == nil {
		return
	}
	ps := f.pairing
	q := r.URL.Query()
	if cn := q.Get("client_nonce"); cn != "" {
		b, err := hex.DecodeString(cn)
		sum := sha256.Sum256(b)
		if err != nil || len(b) < 16 || !bytes.Equal(sum[:], req.commit) {
			ps.mu.Unlock()
			writeError(w, http.StatusUnprocessableEntity, "invalid", "client_nonce does not match the commit")
			return
		}
		req.clientNon = b
	} else if req.clientNon == nil {
		ps.mu.Unlock()
		writeError(w, http.StatusUnprocessableEntity, "invalid", "the first poll must reveal client_nonce")
		return
	}
	now := time.Now()
	wait := time.Duration(0)
	if s := q.Get("wait"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			ps.mu.Unlock()
			writeError(w, http.StatusUnprocessableEntity, "invalid", "invalid wait")
			return
		}
		wait = min(time.Duration(n)*time.Second, pairingMaxWait)
	}
	if wait == 0 && !req.lastPoll.IsZero() && now.Sub(req.lastPoll) < ps.interval {
		ps.mu.Unlock()
		w.Header().Set("Retry-After", strconv.Itoa(max(1, int(ps.interval/time.Second))))
		writeError(w, http.StatusTooManyRequests, "slow_down", "poll less often")
		return
	}
	req.lastPoll = now
	if wait > 0 && req.state == pairingPending {
		changed := req.changed
		deadline := time.Until(req.created.Add(ps.lifetime))
		ps.mu.Unlock()
		timer := time.NewTimer(min(wait, max(deadline, 0)+time.Millisecond))
		select {
		case <-changed:
		case <-timer.C:
		case <-r.Context().Done():
		case <-f.done:
		}
		timer.Stop()
		ps.mu.Lock()
		ps.expireLocked(time.Now())
		if _, ok := ps.requests[req.id]; !ok {
			ps.mu.Unlock()
			writeError(w, http.StatusNotFound, "not_found", "no such pairing request")
			return
		}
	}
	defer ps.mu.Unlock()
	w.Header().Set("Cache-Control", "no-store")
	if req.state != pairingApproved {
		writeJSON(w, http.StatusOK, pollAnswer{State: req.state})
		return
	}
	if req.handedOut {
		writeError(w, http.StatusNotFound, "not_found", "no such pairing request")
		return
	}
	req.handedOut = true
	access := req.body.Access
	writeJSON(w, http.StatusOK, pollAnswer{
		State:  pairingApproved,
		Token:  req.token,
		Name:   req.tokenName,
		Scopes: append([]string{}, req.scopes...),
		Access: &access,
	})
}

// handlePairingDelete answers DELETE /api/auth/v1/pairing/request/{id}.
func (f *Fake) handlePairingDelete(w http.ResponseWriter, r *http.Request) {
	req := f.pairingAuth(w, r)
	if req == nil {
		return
	}
	delete(f.pairing.requests, req.id)
	req.notify()
	f.pairing.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

type approveBody struct {
	Code string `json:"code"`
}

// handlePairingApprove is the administrator's POST
// /api/auth/v1/pairing/{id}/approve {code}.
func (f *Fake) handlePairingApprove(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := f.authorize(w, r, "auth:admin", false); !ok {
		return
	}
	var body approveBody
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid", "invalid body")
		return
	}
	if _, err := f.ApprovePairing(r.PathValue("id"), body.Code); err != nil {
		var pe *PairingError
		if errors.As(err, &pe) {
			writeError(w, pe.Status, pe.Code, pe.Message)
			return
		}
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, okAnswer{OK: true})
}

// PairingError is a refused approval.
type PairingError struct {
	Status  int
	Code    string
	Message string
}

func (e *PairingError) Error() string { return e.Code + ": " + e.Message }

// PendingPairing is a pairing request as an administrator sees it.
type PendingPairing struct {
	ID       string
	App      string
	Instance string
	Name     string
	Access   PairingAccess
	State    string
	Revealed bool
	// Code is the six-digit code, known once the client nonce is
	// revealed; an administrator compares it with the app's display.
	Code string
}

// Pairings lists the requests the fake holds.
func (f *Fake) Pairings() []PendingPairing {
	ps := f.pairing
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.expireLocked(time.Now())
	out := make([]PendingPairing, 0, len(ps.requests))
	for _, req := range ps.requests {
		p := PendingPairing{
			ID: req.id, App: req.body.App, Instance: req.body.Instance, Name: req.body.Name,
			Access: req.body.Access, State: req.state, Revealed: req.clientNon != nil,
		}
		if p.Revealed {
			p.Code = PairingCode(req.nonce, req.clientNon, ps.fingerprint)
		}
		out = append(out, p)
	}
	return out
}

// ApprovePairing approves a revealed request if code matches, creating
// its token (no expiry, no IP ranges) and returning the token name. A
// wrong code rejects the request and mutes the app, as on the box.
func (f *Fake) ApprovePairing(id, code string) (string, error) {
	ps := f.pairing
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.expireLocked(time.Now())
	req, ok := ps.requests[id]
	if !ok || req.state != pairingPending {
		return "", &PairingError{Status: http.StatusNotFound, Code: "not_found", Message: "no pending request " + id}
	}
	if req.clientNon == nil {
		return "", &PairingError{Status: http.StatusConflict, Code: "not-ready", Message: "the client has not revealed its nonce"}
	}
	if code != PairingCode(req.nonce, req.clientNon, ps.fingerprint) {
		req.state = pairingRejected
		ps.muted[req.address+"\x00"+req.body.App] = time.Now().Add(pairingMute)
		req.notify()
		return "", &PairingError{Status: http.StatusConflict, Code: "wrong-code", Message: "the code does not match"}
	}
	scopes, err := PairingScopes(req.body.Access)
	if err != nil {
		return "", err
	}
	secret := "olt_" + randomHex(16)
	name := f.addPairedToken(secret, req.body.App+"-"+req.body.Instance, scopes)
	req.state = pairingApproved
	req.token, req.tokenName, req.scopes = secret, name, scopes
	req.notify()
	return name, nil
}

// RejectPairing rejects a pending request and mutes the app.
func (f *Fake) RejectPairing(id string) error {
	ps := f.pairing
	ps.mu.Lock()
	defer ps.mu.Unlock()
	req, ok := ps.requests[id]
	if !ok || req.state != pairingPending {
		return &PairingError{Status: http.StatusNotFound, Code: "not_found", Message: "no pending request " + id}
	}
	req.state = pairingRejected
	ps.muted[req.address+"\x00"+req.body.App] = time.Now().Add(pairingMute)
	req.notify()
	return nil
}

// SetPairingEnabled switches pairing on or off (off: 403 pairing-off).
func (f *Fake) SetPairingEnabled(enabled bool) {
	f.pairing.mu.Lock()
	f.pairing.disabled = !enabled
	f.pairing.mu.Unlock()
}

// slugify turns "app-instance" into a token name: lowercase, runs of
// other characters as one dash, trimmed, at most 28 characters.
func slugify(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
			continue
		}
		if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > pairingTokenNameMax {
		out = strings.Trim(out[:pairingTokenNameMax], "-")
	}
	if len(out) < 2 {
		out = "app-" + out
	}
	return out
}

// addPairedToken stores a paired token under a unique slug name,
// suffixed -2, -3, … on collision.
func (f *Fake) addPairedToken(secret, base string, scopes []string) string {
	slug := slugify(base)
	f.mu.Lock()
	defer f.mu.Unlock()
	taken := map[string]bool{}
	for _, e := range f.tokens {
		taken[e.name] = true
	}
	name := slug
	for n := 2; taken[name]; n++ {
		name = slug + "-" + strconv.Itoa(n)
	}
	f.tokens[secret] = tokenEntry{name: name, scopes: append([]string{}, scopes...)}
	return name
}
