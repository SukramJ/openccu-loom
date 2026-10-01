// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package pairing

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/auth"
)

// Protocol limits. They are the contract's own throttle — the HTTP layer
// adds the shared login rate limiter in front, but these hold even for a
// caller that gets past it.
const (
	// Lifetime is how long a request waits for the administrator.
	Lifetime = 5 * time.Minute
	// MaxPending bounds the requests waiting at once, across all callers.
	MaxPending = 5
	// PerHour bounds the requests one address may open per hour.
	PerHour = 10
	// MuteFor silences an (address, app) pair after a rejection.
	MuteFor = 10 * time.Minute
	// Interval is the minimum spacing between plain polls.
	Interval = 2 * time.Second
	// MaxWait caps one long poll.
	MaxWait = 30 * time.Second
	// finishedLinger keeps a decided request for the client's last poll.
	finishedLinger = time.Minute
	maxPurposeLen  = 200
)

// The states a poll reports. They mirror the occulited protocol this
// daemon speaks as a client, so both pairings feel identical.
const (
	StatePending  = "pending"
	StateApproved = "approved"
	StateRejected = "rejected"
	StateExpired  = "expired"
)

// Errors the HTTP layer maps to problem responses.
var (
	ErrOff       = errors.New("pairing is switched off: an administrator enables it in the API tokens panel, or creates a token there")
	ErrNotLocal  = errors.New("pairing is open to the local networks only")
	ErrLimit     = errors.New("too many pairing requests")
	ErrMuted     = errors.New("a request of this app from this address was rejected a moment ago")
	ErrInvalid   = errors.New("invalid pairing request")
	ErrUnknown   = errors.New("no such pairing request (any more)")
	ErrPollAuth  = errors.New("not this request's poll secret")
	ErrSlowDown  = errors.New("slow down")
	ErrWrongCode = errors.New("the code is not this request's")
	ErrNotReady  = errors.New("the client has not revealed its half of the code yet")
)

// RoleFor validates the requested role. Pairing hands out everyday
// access only: viewer and operator. An admin token — users, tokens,
// config, system actions — is never minted from a network request an
// administrator merely compares six digits for; it is created
// deliberately in the tokens panel.
func RoleFor(role string) (auth.Role, error) {
	switch auth.Role(role) {
	case auth.RoleViewer:
		return auth.RoleViewer, nil
	case auth.RoleOperator:
		return auth.RoleOperator, nil
	case auth.RoleAdmin:
		return "", fmt.Errorf("%w: the admin role cannot be paired — create an admin token in the tokens panel", ErrInvalid)
	default:
		return "", fmt.Errorf("%w: role is viewer or operator", ErrInvalid)
	}
}

// Ask is a client's request.
type Ask struct {
	App        string `json:"app"`
	AppVersion string `json:"app_version"`
	Instance   string `json:"instance"`
	Name       string `json:"name"`
	Role       string `json:"role"`
	Purpose    string `json:"purpose"`
	// Commit is the hex SHA-256 of the client's nonce, revealed with its
	// first poll.
	Commit string `json:"commit"`
}

// Answer is the accepted request: what the client needs to derive the
// code and to poll. Poll is a secret; it never appears in a URL.
type Answer struct {
	ID          string `json:"id"`
	Poll        string `json:"poll"`
	Nonce       string `json:"nonce"`
	ExpiresIn   int    `json:"expires_in"`
	Interval    int    `json:"interval"`
	Fingerprint string `json:"fingerprint"`
}

// Result is a poll's answer. Token is present exactly once, on the
// first poll that sees the approval; the request is gone afterwards.
type Result struct {
	State   string `json:"state"`
	Token   string `json:"token,omitempty"`
	Subject string `json:"subject,omitempty"`
	Role    string `json:"role,omitempty"`
}

// View is one pending request as the admin card renders it — never the
// poll secret, and only after the client revealed its nonce, because
// before that no code exists to compare.
type View struct {
	ID          string    `json:"id"`
	App         string    `json:"app"`
	AppVersion  string    `json:"app_version,omitempty"`
	Instance    string    `json:"instance,omitempty"`
	Name        string    `json:"name"`
	Address     string    `json:"address"`
	Role        string    `json:"role"`
	Purpose     string    `json:"purpose,omitempty"`
	Code        string    `json:"code"`
	Fingerprint string    `json:"fingerprint,omitempty"`
	Created     time.Time `json:"created"`
	Expires     time.Time `json:"expires"`
	// LookAlike marks a second pending request from the same address or
	// the same app+instance — "compare the code carefully".
	LookAlike bool `json:"look_alike,omitempty"`
}

// Minter turns an approved request into a real API token. The production
// implementation wraps the sqlite token store; the subject arrives
// pre-slugged from the app and instance.
type Minter interface {
	MintPairedToken(ctx context.Context, subject string, role auth.Role) (token, fingerprint string, err error)
}

type request struct {
	id          string
	pollHash    [32]byte
	nonce       []byte
	commit      []byte
	clientNonce []byte
	fingerprint []byte
	ask         Ask
	role        auth.Role
	address     string
	created     time.Time
	expires     time.Time
	state       string
	result      Result
	lastPoll    time.Time
	// changed is closed and replaced on every state change so long polls
	// wake immediately.
	changed chan struct{}
}

// Manager keeps the pending requests. All state is in memory: a request
// lives five minutes, and a daemon restart voiding it is exactly what an
// operator expects of an unapproved credential request.
type Manager struct {
	// Minter mints the token of an approved request. Required.
	Minter Minter
	// Enabled gates the whole surface; nil means enabled.
	Enabled func() bool
	// Local reports whether a remote address (host without port) is on a
	// local network. Nil accepts every address.
	Local func(host string) bool
	// Fingerprint returns the SHA-256 of the certificate DER this daemon
	// currently serves, or nil over plain HTTP / behind a TLS-terminating
	// proxy. Nil means no fingerprint.
	Fingerprint func() []byte
	// OnChange runs after every change of the pending list (outside the
	// lock) — the WS broadcast hangs here.
	OnChange func()
	Log      *slog.Logger

	mu      sync.Mutex
	reqs    map[string]*request
	mutes   map[string]time.Time
	perHour map[string][]time.Time
}

func (m *Manager) log() *slog.Logger {
	if m.Log != nil {
		return m.Log
	}
	return slog.Default()
}

var (
	appIDRe   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,47}$`)
	hex64Re   = regexp.MustCompile(`^[0-9a-f]{64}$`)
	noCtrlRe  = regexp.MustCompile(`^[^\x00-\x1f\x7f]*$`)
	slugDrop  = regexp.MustCompile(`[^a-z0-9._-]+`)
	slugSquee = regexp.MustCompile(`--+`)
)

func randBytes(n int) (asHex string, raw []byte, err error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", nil, err
	}
	return hex.EncodeToString(b), b, nil
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		s = s[:n]
	}
	return s
}

// Subject derives the token subject from app and instance: lower-case
// slug, at most 28 characters so a numbering suffix still fits the
// canonical subject rules.
func Subject(app, instance string) string {
	s := strings.ToLower(app)
	if instance != "" {
		s += "-" + strings.ToLower(instance)
	}
	s = slugDrop.ReplaceAllString(s, "-")
	s = slugSquee.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-._")
	if len(s) > 28 {
		s = strings.Trim(s[:28], "-._")
	}
	if len(s) < 2 {
		return "paired-client"
	}
	return s
}

// expireLocked retires overdue requests and mutes; reports whether the
// pending list changed.
func (m *Manager) expireLocked(now time.Time) bool {
	changed := false
	for id, r := range m.reqs {
		if r.state == StatePending && !now.Before(r.expires) {
			r.state = StateExpired
			m.signalLocked(r)
			changed = true
			m.log().Info("pairing: request expired", "app", r.ask.App, "instance", r.ask.Instance, "address", r.address)
		}
		if r.state != StatePending && now.Sub(r.expires) > finishedLinger {
			delete(m.reqs, id)
		}
	}
	for k, until := range m.mutes {
		if !now.Before(until) {
			delete(m.mutes, k)
		}
	}
	return changed
}

func (m *Manager) signalLocked(r *request) {
	close(r.changed)
	r.changed = make(chan struct{})
}

func (m *Manager) notify() {
	if m.OnChange != nil {
		m.OnChange()
	}
}

// Request accepts a client's ask from addr (host only, no port).
// fingerprint is ignored in favour of the Manager's Fingerprint seam so
// every request binds to the certificate the daemon actually serves.
func (m *Manager) Request(a Ask) (Answer, error) {
	return m.RequestFrom(a, "")
}

// RequestFrom is Request with the caller's address for the protocol
// limits and the admin card.
func (m *Manager) RequestFrom(a Ask, addr string) (Answer, error) { //nolint:funlen // one straight-line protocol admission: validate, gate, limit, mint state
	if m.Enabled != nil && !m.Enabled() {
		return Answer{}, ErrOff
	}
	if m.Local != nil && !m.Local(addr) {
		m.log().Warn("pairing: refused a request from outside the local networks", "address", addr, "app", a.App)
		return Answer{}, ErrNotLocal
	}
	a.App = clip(a.App, 48)
	a.AppVersion = clip(a.AppVersion, 32)
	a.Instance = clip(a.Instance, 64)
	a.Name = clip(a.Name, 80)
	a.Purpose = clip(a.Purpose, maxPurposeLen)
	if !appIDRe.MatchString(a.App) {
		return Answer{}, fmt.Errorf("%w: app is a short id (letters, digits, . _ -)", ErrInvalid)
	}
	for _, s := range []string{a.AppVersion, a.Instance, a.Name, a.Purpose} {
		if !noCtrlRe.MatchString(s) {
			return Answer{}, fmt.Errorf("%w: no control characters", ErrInvalid)
		}
	}
	role, err := RoleFor(a.Role)
	if err != nil {
		return Answer{}, err
	}
	if !hex64Re.MatchString(strings.ToLower(a.Commit)) {
		return Answer{}, fmt.Errorf("%w: commit is the hex SHA-256 of the client's nonce", ErrInvalid)
	}
	if a.Name == "" {
		a.Name = a.App
		if a.Instance != "" {
			a.Name += " on " + a.Instance
		}
	}

	var fp []byte
	if m.Fingerprint != nil {
		fp = m.Fingerprint()
	}

	now := time.Now()
	m.mu.Lock()
	if m.reqs == nil {
		m.reqs = map[string]*request{}
		m.mutes = map[string]time.Time{}
		m.perHour = map[string][]time.Time{}
	}
	listChanged := m.expireLocked(now)
	muteKey := addr + "|" + a.App
	if until, ok := m.mutes[muteKey]; ok && now.Before(until) {
		m.mu.Unlock()
		if listChanged {
			m.notify()
		}
		return Answer{}, ErrMuted
	}
	recent := m.perHour[addr][:0]
	for _, t := range m.perHour[addr] {
		if now.Sub(t) < time.Hour {
			recent = append(recent, t)
		}
	}
	m.perHour[addr] = recent
	if len(recent) >= PerHour {
		m.mu.Unlock()
		return Answer{}, fmt.Errorf("%w: %d an hour from one address", ErrLimit, PerHour)
	}
	pending := 0
	for _, r := range m.reqs {
		if r.state != StatePending {
			continue
		}
		pending++
		if r.address == addr && r.ask.App == a.App {
			m.mu.Unlock()
			return Answer{}, fmt.Errorf("%w: one request per app and address at a time", ErrLimit)
		}
	}
	if pending >= MaxPending {
		m.mu.Unlock()
		return Answer{}, fmt.Errorf("%w: %d are already waiting", ErrLimit, MaxPending)
	}

	id, _, err := randBytes(8)
	if err != nil {
		m.mu.Unlock()
		return Answer{}, err
	}
	pollSecret, _, err := randBytes(24)
	if err != nil {
		m.mu.Unlock()
		return Answer{}, err
	}
	nonceHex, nonce, err := randBytes(16)
	if err != nil {
		m.mu.Unlock()
		return Answer{}, err
	}
	commit, _ := hex.DecodeString(strings.ToLower(a.Commit))
	r := &request{
		id: id, pollHash: sha256.Sum256([]byte(pollSecret)), nonce: nonce, commit: commit,
		fingerprint: fp, ask: a, role: role, address: addr,
		created: now, expires: now.Add(Lifetime), state: StatePending, changed: make(chan struct{}),
	}
	m.reqs[id] = r
	m.perHour[addr] = append(recent, now)
	m.mu.Unlock()

	m.log().Info("pairing: a client asks for access",
		"app", a.App, "version", a.AppVersion, "instance", a.Instance, "address", addr, "role", string(role))
	if listChanged {
		m.notify()
	}
	return Answer{
		ID: id, Poll: pollSecret, Nonce: nonceHex,
		ExpiresIn: int(Lifetime / time.Second), Interval: int(Interval / time.Second),
		Fingerprint: hex.EncodeToString(fp),
	}, nil
}

func (m *Manager) findLocked(id, pollSecret string) (*request, error) {
	r := m.reqs[id]
	if r == nil {
		return nil, ErrUnknown
	}
	h := sha256.Sum256([]byte(pollSecret))
	if subtle.ConstantTimeCompare(h[:], r.pollHash[:]) != 1 {
		return nil, ErrPollAuth
	}
	return r, nil
}

// Poll is the client's wait. The first poll reveals clientNonce (hex,
// matching the commitment); wait > 0 long-polls until the state changes
// or wait passes. An approved answer carries the token exactly once —
// the request is deleted with the answer.
func (m *Manager) Poll(ctx context.Context, id, pollSecret, clientNonce string, wait time.Duration) (Result, error) {
	now := time.Now()
	m.mu.Lock()
	listChanged := m.expireLocked(now)
	r, err := m.findLocked(id, pollSecret)
	if err != nil {
		m.mu.Unlock()
		if listChanged {
			m.notify()
		}
		return Result{}, err
	}
	revealed := false
	if clientNonce != "" && r.clientNonce == nil {
		cn, decErr := hex.DecodeString(strings.ToLower(clientNonce))
		sum := sha256.Sum256(cn)
		if decErr != nil || len(cn) < 16 || subtle.ConstantTimeCompare(sum[:], r.commit) != 1 {
			m.mu.Unlock()
			return Result{}, fmt.Errorf("%w: the nonce does not match the commitment", ErrInvalid)
		}
		r.clientNonce = cn
		revealed = true
	}
	if wait <= 0 && !revealed && r.state == StatePending &&
		!r.lastPoll.IsZero() && now.Sub(r.lastPoll) < Interval-200*time.Millisecond {
		m.mu.Unlock()
		return Result{}, ErrSlowDown
	}
	r.lastPoll = now
	if wait > MaxWait {
		wait = MaxWait
	}
	ch := r.changed
	state := r.state
	m.mu.Unlock()
	if revealed || listChanged {
		m.notify()
	}

	if state == StatePending && wait > 0 {
		t := time.NewTimer(wait)
		select {
		case <-ch:
		case <-t.C:
		case <-ctx.Done():
		}
		t.Stop()
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.expireLocked(time.Now())
	if r.state == StatePending {
		return Result{State: StatePending}, nil
	}
	res := r.result
	res.State = r.state
	// The answer exists once: a second poll is ErrUnknown, and the token
	// with it is gone from memory.
	delete(m.reqs, id)
	return res, nil
}

// Withdraw is the client giving up its own request.
func (m *Manager) Withdraw(id, pollSecret string) error {
	m.mu.Lock()
	r, err := m.findLocked(id, pollSecret)
	if err == nil {
		delete(m.reqs, id)
		m.signalLocked(r)
	}
	m.mu.Unlock()
	if err != nil {
		return err
	}
	m.log().Info("pairing: the client withdrew its request", "app", r.ask.App, "address", r.address)
	m.notify()
	return nil
}

// Pending lists the requests the admin card shows: revealed ones only,
// oldest first, with the code both sides derived.
func (m *Manager) Pending() []View {
	now := time.Now()
	m.mu.Lock()
	changed := m.expireLocked(now)
	out := []View{}
	for _, r := range m.reqs {
		if r.state != StatePending || r.clientNonce == nil {
			continue
		}
		out = append(out, View{
			ID: r.id, App: r.ask.App, AppVersion: r.ask.AppVersion, Instance: r.ask.Instance,
			Name: r.ask.Name, Address: r.address, Role: string(r.role), Purpose: r.ask.Purpose,
			Code:        Code(r.nonce, r.clientNonce, r.fingerprint),
			Fingerprint: hex.EncodeToString(r.fingerprint),
			Created:     r.created, Expires: r.expires,
		})
	}
	m.mu.Unlock()
	if changed {
		m.notify()
	}
	for i := range out {
		for j := range out {
			if i == j {
				continue
			}
			if out[i].Address == out[j].Address || (out[i].App == out[j].App && out[i].Instance == out[j].Instance) {
				out[i].LookAlike = true
			}
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Created.Before(out[b].Created) })
	return out
}

// PendingCount is the operator-warning feed: how many revealed requests
// wait for a decision.
func (m *Manager) PendingCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expireLocked(time.Now())
	n := 0
	for _, r := range m.reqs {
		if r.state == StatePending && r.clientNonce != nil {
			n++
		}
	}
	return n
}

// Approve mints the token for the request whose code the administrator
// typed. A wrong code REJECTS the request and mutes its (address, app),
// because a wrong code means the administrator compared against a
// different request than the one asking — possibly an interceptor's.
func (m *Manager) Approve(ctx context.Context, id, code, by string) (View, error) {
	m.mu.Lock()
	m.expireLocked(time.Now())
	r := m.reqs[id]
	if r == nil || r.state != StatePending {
		m.mu.Unlock()
		return View{}, ErrUnknown
	}
	if r.clientNonce == nil {
		m.mu.Unlock()
		return View{}, ErrNotReady
	}
	want := Code(r.nonce, r.clientNonce, r.fingerprint)
	if subtle.ConstantTimeCompare([]byte(want), []byte(strings.TrimSpace(code))) != 1 {
		r.state = StateRejected
		m.mutes[r.address+"|"+r.ask.App] = time.Now().Add(MuteFor)
		m.signalLocked(r)
		m.mu.Unlock()
		m.log().Warn("pairing: wrong code — request rejected", "app", r.ask.App, "address", r.address, "by", by)
		m.notify()
		return View{}, ErrWrongCode
	}
	ask, role, address := r.ask, r.role, r.address
	m.mu.Unlock()

	subject := Subject(ask.App, ask.Instance)
	token, fingerprint, err := m.Minter.MintPairedToken(ctx, subject, role)
	if err != nil {
		return View{}, fmt.Errorf("pairing: mint token: %w", err)
	}

	m.mu.Lock()
	if r2 := m.reqs[id]; r2 == r && r.state == StatePending {
		r.state = StateApproved
		r.result = Result{Token: token, Subject: subject, Role: string(role)}
		m.signalLocked(r)
	}
	m.mu.Unlock()
	m.log().Info("pairing: approved — token minted",
		"app", ask.App, "instance", ask.Instance, "address", address,
		"subject", subject, "role", string(role), "fingerprint", fingerprint, "by", by)
	m.notify()
	return View{ID: id, App: ask.App, Instance: ask.Instance, Name: ask.Name, Address: address, Role: string(role)}, nil
}

// Reject turns a request down and mutes its (address, app) pair.
func (m *Manager) Reject(id, by string) (View, error) {
	m.mu.Lock()
	r := m.reqs[id]
	if r == nil || r.state != StatePending {
		m.mu.Unlock()
		return View{}, ErrUnknown
	}
	r.state = StateRejected
	m.mutes[r.address+"|"+r.ask.App] = time.Now().Add(MuteFor)
	m.signalLocked(r)
	view := View{ID: r.id, App: r.ask.App, Instance: r.ask.Instance, Name: r.ask.Name, Address: r.address, Role: string(r.role)}
	m.mu.Unlock()
	m.log().Info("pairing: rejected", "app", view.App, "instance", view.Instance, "address", view.Address, "by", by)
	m.notify()
	return view, nil
}
