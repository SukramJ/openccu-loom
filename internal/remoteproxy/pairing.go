// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package remoteproxy

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/SukramJ/openccu-loom/internal/build"
	"github.com/SukramJ/openccu-loom/internal/pairingcode"
)

// The box pairing protocol of openccu-lite (occulited): the program asks
// without a credential, shows a six-digit code bound to the certificate it
// saw, and long-polls until the box's administrator approves. An ask that
// names only an add-on yields a token with the scope addon:<id>, which the
// box's lighttpd gate accepts as a Bearer credential for that add-on's
// ingress pages. See docs/adr/0091-remote-addon-box-pairing.md.
const (
	boxPairingPath = "/api/auth/v1/pairing/request"
	// boxPairingAddon is the add-on id of OpenCCU-Loom on the box; its
	// scope opens the URL segment the add-on's ingress lives under.
	boxPairingAddon = "openccu-loom"
	boxPairingApp   = "openccu-loom-remote"
	// boxPairingWait is the long-poll window; the box caps it at 30 s.
	boxPairingWait = 25 * time.Second
	// boxPairingCallTimeout bounds one non-polling call to the box.
	boxPairingCallTimeout = 15 * time.Second
	// boxPairingClientNonceLen is the client's secret nonce; the box wants
	// at least 16 bytes.
	boxPairingClientNonceLen = 32
	// maxBoxPairingBody caps a pairing answer; real ones are a few hundred bytes.
	maxBoxPairingBody = 64 << 10
)

// Box pairing states a poll reports.
const (
	boxPairPending  = "pending"
	boxPairApproved = "approved"
	boxPairRejected = "rejected"
	boxPairExpired  = "expired"
)

// errPairFingerprint aborts a pairing whose certificate the box reports
// differs from the one this connection saw: something between the two
// terminated TLS, and the code would authenticate the interceptor.
var errPairFingerprint = errors.New("the certificate the box reports differs from the one seen — possible interception")

// errPairNoAddonScope reports an approval that does not carry the scope
// of the add-on, which would leave the proxy with a useless token.
var errPairNoAddonScope = errors.New("the box approved the pairing without the OpenCCU-Loom add-on")

// boxPairingError is a refusal the box answered with its error code
// (pairing-off, not-local, limit, slow_down, invalid, not_found, …).
type boxPairingError struct {
	Status     int
	Code       string
	Message    string
	RetryAfter time.Duration
}

func (e *boxPairingError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("box answered %d %s: %s", e.Status, e.Code, e.Message)
	}
	return fmt.Sprintf("box answered %d %s", e.Status, e.Code)
}

// boxPairAsk is the body of the pairing request.
type boxPairAsk struct {
	App        string   `json:"app"`
	AppVersion string   `json:"app_version,omitempty"`
	Instance   string   `json:"instance,omitempty"`
	Name       string   `json:"name,omitempty"`
	Addons     []string `json:"addons"`
	Commit     string   `json:"commit"`
}

// boxPairAnswer is the 202 to the pairing request.
type boxPairAnswer struct {
	ID          string `json:"id"`
	Poll        string `json:"poll"`
	Nonce       string `json:"nonce"`
	ExpiresIn   int    `json:"expires_in"`
	Interval    int    `json:"interval"`
	Fingerprint string `json:"fingerprint"`
}

// boxPairResult is one poll's answer; Token is set only on the one
// approved answer.
type boxPairResult struct {
	State  string   `json:"state"`
	Token  string   `json:"token"`
	Name   string   `json:"name"`
	Scopes []string `json:"scopes"`
	Addons []string `json:"addons"`
}

// boxPairing is a started pairing against one box.
type boxPairing struct {
	box         *url.URL
	id          string
	poll        string
	clientNonce []byte
	code        string
	// fingerprint is the hex SHA-256 of the box certificate the code is
	// bound to; empty over plain HTTP.
	fingerprint string
	expires     time.Time
	interval    time.Duration
	// client talks to the box for the polls, pinned to fingerprint.
	client *http.Client
}

// boxOrigin is the box itself: the pairing API lives at the box root,
// whatever add-on path the instance URL points at.
func boxOrigin(target *url.URL) *url.URL {
	return &url.URL{Scheme: target.Scheme, Host: target.Host}
}

// boxPairingTransport is the transport of the pairing calls. Without a
// pin, certificate verification is off for the request only: the code
// the administrator compares binds the certificate this connection saw,
// so an interceptor's certificate yields a code the box refuses. With a
// pin, exactly the pinned certificate is accepted.
func boxPairingTransport(pin []byte) *http.Transport {
	t := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout: 10 * time.Second,
		IdleConnTimeout:     30 * time.Second,
		// The certificate check is the code comparison (unpinned) or the
		// pin below, never the chain.
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: true}, //nolint:gosec // G402: see above; the code or the pin authenticates the certificate.
	}
	if pin != nil {
		t.TLSClientConfig.VerifyConnection = pinVerifier(pin)
	}
	return t
}

// pinVerifier accepts exactly the leaf certificate whose SHA-256 is pin.
func pinVerifier(pin []byte) func(tls.ConnectionState) error {
	return func(cs tls.ConnectionState) error {
		if len(cs.PeerCertificates) == 0 {
			return errors.New("box presented no certificate")
		}
		sum := sha256.Sum256(cs.PeerCertificates[0].Raw)
		if subtle.ConstantTimeCompare(sum[:], pin) != 1 {
			return fmt.Errorf("box certificate %s is not the paired one", hex.EncodeToString(sum[:]))
		}
		return nil
	}
}

// startBoxPairing asks the box for a token that opens the OpenCCU-Loom
// add-on. It commits to a random secret nonce, sends the request, checks
// that the certificate the box reports is the one this connection saw,
// and derives the code the administrator enters on the box.
func startBoxPairing(ctx context.Context, box *url.URL, instance string) (*boxPairing, error) {
	clientNonce := make([]byte, boxPairingClientNonceLen)
	if _, err := rand.Read(clientNonce); err != nil {
		return nil, fmt.Errorf("pairing nonce: %w", err)
	}
	commit := sha256.Sum256(clientNonce)
	body, err := json.Marshal(boxPairAsk{
		App:        boxPairingApp,
		AppVersion: build.Version,
		Instance:   instance,
		Name:       "OpenCCU-Loom Remote (" + instance + ")",
		Addons:     []string{boxPairingAddon},
		Commit:     hex.EncodeToString(commit[:]),
	})
	if err != nil {
		return nil, fmt.Errorf("encode pairing request: %w", err)
	}
	reqCtx, cancel := context.WithTimeout(ctx, boxPairingCallTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, box.String()+boxPairingPath, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	starter := &http.Client{Transport: boxPairingTransport(nil)}
	defer starter.CloseIdleConnections()
	resp, err := starter.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	seen := ""
	if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
		sum := sha256.Sum256(resp.TLS.PeerCertificates[0].Raw)
		seen = hex.EncodeToString(sum[:])
	}
	var ans boxPairAnswer
	if err := decodeBoxAnswer(resp, &ans); err != nil {
		return nil, err
	}
	if !strings.EqualFold(ans.Fingerprint, seen) {
		return nil, fmt.Errorf("%w (box %q, seen %q)", errPairFingerprint, ans.Fingerprint, seen)
	}
	nonce, err := hex.DecodeString(ans.Nonce)
	if err != nil || len(nonce) == 0 || ans.ID == "" || ans.Poll == "" {
		return nil, errors.New("pairing answer without id, poll secret or hex nonce")
	}
	fp, err := hex.DecodeString(seen)
	if err != nil {
		return nil, fmt.Errorf("pairing fingerprint: %w", err)
	}
	var pin []byte
	if len(fp) > 0 {
		pin = fp
	}
	interval := time.Duration(ans.Interval) * time.Second
	if interval <= 0 {
		interval = 2 * time.Second
	}
	return &boxPairing{
		box:         box,
		id:          ans.ID,
		poll:        ans.Poll,
		clientNonce: clientNonce,
		code:        pairingcode.Code(nonce, clientNonce, fp),
		fingerprint: seen,
		expires:     time.Now().Add(time.Duration(ans.ExpiresIn) * time.Second),
		interval:    interval,
		client:      &http.Client{Transport: boxPairingTransport(pin)},
	}, nil
}

// pollOnce asks for the pairing's state, waiting on the box up to wait.
// The first poll reveals the secret nonce; the box shows the request to
// its administrator only after that.
func (bp *boxPairing) pollOnce(ctx context.Context, wait time.Duration) (boxPairResult, error) {
	q := url.Values{"client_nonce": {hex.EncodeToString(bp.clientNonce)}}
	if secs := int(wait / time.Second); secs > 0 {
		q.Set("wait", strconv.Itoa(min(secs, 30)))
	}
	reqCtx, cancel := context.WithTimeout(ctx, wait+boxPairingCallTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet,
		bp.box.String()+boxPairingPath+"/"+url.PathEscape(bp.id)+"?"+q.Encode(), http.NoBody)
	if err != nil {
		return boxPairResult{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Pairing "+bp.poll)
	resp, err := bp.client.Do(req)
	if err != nil {
		return boxPairResult{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	var res boxPairResult
	if err := decodeBoxAnswer(resp, &res); err != nil {
		return boxPairResult{}, err
	}
	if res.State == boxPairApproved {
		if res.Token == "" {
			return boxPairResult{}, errors.New("the box approved the pairing without a token")
		}
		if !slices.Contains(res.Addons, boxPairingAddon) && !slices.Contains(res.Scopes, "addon:"+boxPairingAddon) {
			return boxPairResult{}, errPairNoAddonScope
		}
	}
	return res, nil
}

// withdraw gives the pairing up on the box.
func (bp *boxPairing) withdraw(ctx context.Context) error {
	reqCtx, cancel := context.WithTimeout(ctx, boxPairingCallTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodDelete,
		bp.box.String()+boxPairingPath+"/"+url.PathEscape(bp.id), strings.NewReader("{}"))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Pairing "+bp.poll)
	resp, err := bp.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= http.StatusBadRequest {
		return decodeBoxAnswer(resp, &struct{}{})
	}
	return nil
}

// decodeBoxAnswer decodes a 2xx JSON body into into, and turns any other
// status into a *boxPairingError carrying the box's error code. into is
// any because it is the decode target of wire JSON (a pointer to one of
// the answer structs above).
func decodeBoxAnswer(resp *http.Response, into any) error {
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBoxPairingBody))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var e struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(raw, &e)
		pe := &boxPairingError{Status: resp.StatusCode, Code: e.Error, Message: e.Message}
		if secs, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && secs > 0 {
			pe.RetryAfter = time.Duration(secs) * time.Second
		}
		return pe
	}
	if err := json.Unmarshal(raw, into); err != nil {
		return fmt.Errorf("decode pairing answer: %w", err)
	}
	return nil
}

// Pairing states the proxy reports to the browser. The box's own states
// pass through; "idle", "paired", "failed" and "options" are the proxy's.
const (
	pairStateIdle    = "idle"
	pairStatePaired  = "paired"
	pairStateFailed  = "failed"
	pairStateOptions = "options"
)

// pairSession is one pairing attempt of an instance. Its fields after
// creation are guarded by the owning instanceProxy's mu.
type pairSession struct {
	bp      *boxPairing
	cancel  context.CancelFunc
	state   string
	errCode string
	errMsg  string
}

// pairView is what the browser learns about an instance's pairing. It
// never carries the token or the poll secret.
type pairView struct {
	Instance  string `json:"instance"`
	State     string `json:"state"`
	Paired    bool   `json:"paired"`
	Code      string `json:"code,omitempty"`
	ExpiresIn int    `json:"expires_in,omitempty"`
	BoxURL    string `json:"box_url,omitempty"`
	Error     string `json:"error,omitempty"`
	Message   string `json:"message,omitempty"`
}

// pairView reports the instance's current pairing state.
func (p *instanceProxy) pairView() pairView {
	v := pairView{Instance: p.inst.Name, State: pairStateIdle}
	if p.inst.Token != "" {
		v.State = pairStateOptions
		return v
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	v.Paired = p.paired.Token != ""
	if v.Paired {
		v.State = pairStatePaired
	}
	sess := p.session
	if sess == nil || sess.state == pairStatePaired {
		return v
	}
	v.State = sess.state
	v.Error, v.Message = sess.errCode, sess.errMsg
	if sess.state == boxPairPending && sess.bp != nil {
		v.Code = sess.bp.code
		v.ExpiresIn = max(0, int(time.Until(sess.bp.expires)/time.Second))
		v.BoxURL = sess.bp.box.String() + "/"
	}
	return v
}

// lastPairError is the box's error code of the latest failed attempt.
func (p *instanceProxy) lastPairError() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.session == nil || p.session.state != pairStateFailed {
		return ""
	}
	return p.session.errCode
}

// startPairing asks the box for a token unless an attempt is pending, and
// waits for the administrator in the background until the pairing expires,
// it is withdrawn, or done closes (the server's lifetime). ctx bounds only
// the request to the box.
func (p *instanceProxy) startPairing(ctx context.Context, done <-chan struct{}) pairView {
	if p.inst.Token != "" {
		return p.pairView()
	}
	p.pairOps.Lock()
	defer p.pairOps.Unlock()
	p.mu.RLock()
	pending := p.session != nil && p.session.state == boxPairPending
	p.mu.RUnlock()
	if pending {
		return p.pairView()
	}
	bp, err := startBoxPairing(ctx, boxOrigin(p.target), p.inst.Name)
	if err != nil {
		p.log.Warn("box pairing could not start", "error", err)
		code, msg := pairErrorCode(err)
		p.mu.Lock()
		p.session = &pairSession{state: pairStateFailed, errCode: code, errMsg: msg}
		p.mu.Unlock()
		return p.pairView()
	}
	// The wait outlives the browser request that started it.
	sessCtx, cancel := context.WithDeadline(context.WithoutCancel(ctx), bp.expires.Add(5*time.Second))
	sess := &pairSession{bp: bp, cancel: cancel, state: boxPairPending}
	p.mu.Lock()
	p.session = sess
	p.mu.Unlock()
	p.log.Info("box pairing started; approve the code on the box", "code", bp.code)
	go func() {
		select {
		case <-done:
			cancel()
		case <-sessCtx.Done():
		}
	}()
	go p.runPairing(sessCtx, sess)
	return p.pairView()
}

// runPairing long-polls the box until the attempt ends. Its lifetime is
// sessCtx: the server's lifetime, the pairing's expiry, or a withdrawal.
func (p *instanceProxy) runPairing(ctx context.Context, sess *pairSession) {
	defer sess.cancel()
	for {
		if ctx.Err() != nil {
			p.finishPairing(sess, boxPairExpired, "", "")
			return
		}
		started := time.Now()
		res, err := sess.bp.pollOnce(ctx, boxPairingWait)
		if err != nil {
			var pe *boxPairingError
			switch {
			case ctx.Err() != nil:
				p.finishPairing(sess, boxPairExpired, "", "")
				return
			case errors.As(err, &pe) && pe.Code == "slow_down":
				sleepCtx(ctx, max(pe.RetryAfter, sess.bp.interval))
				continue
			case errors.As(err, &pe) && pe.Status == http.StatusNotFound:
				// The box forgot the request: it expired or was answered.
				p.finishPairing(sess, boxPairExpired, "", "")
				return
			case errors.As(err, &pe), errors.Is(err, errPairNoAddonScope):
				code, msg := pairErrorCode(err)
				p.finishPairing(sess, pairStateFailed, code, msg)
				return
			default:
				// A transient network fault: try again until the expiry.
				p.log.Debug("box pairing poll failed", "error", err)
				sleepCtx(ctx, sess.bp.interval)
				continue
			}
		}
		switch res.State {
		case boxPairApproved:
			cred := pairedCredential{
				URL:         p.normURL,
				Token:       res.Token,
				Fingerprint: sess.bp.fingerprint,
				TokenName:   res.Name,
				PairedAt:    time.Now().UTC(),
			}
			if err := p.setPaired(cred); err != nil {
				p.log.Error("storing the paired token failed", "error", err)
				p.finishPairing(sess, pairStateFailed, "state-write", err.Error())
				return
			}
			p.log.Info("paired with the box", "token", res.Name)
			p.finishPairing(sess, pairStatePaired, "", "")
			return
		case boxPairRejected, boxPairExpired:
			p.finishPairing(sess, res.State, "", "")
			return
		default:
			// Pending: a box that answers before the long-poll window is
			// not hammered.
			if time.Since(started) < sess.bp.interval {
				sleepCtx(ctx, sess.bp.interval)
			}
		}
	}
}

// finishPairing records the outcome, unless a newer attempt replaced sess
// or a withdrawal already ended it.
func (p *instanceProxy) finishPairing(sess *pairSession, state, code, msg string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.session != sess || sess.state != boxPairPending {
		return
	}
	sess.state, sess.errCode, sess.errMsg = state, code, msg
}

// cancelPairing withdraws a pending attempt on the box and forgets the
// paired token: after it, the instance is unpaired.
func (p *instanceProxy) cancelPairing(ctx context.Context) pairView {
	p.pairOps.Lock()
	defer p.pairOps.Unlock()
	p.mu.Lock()
	sess := p.session
	p.session = nil
	p.mu.Unlock()
	if sess != nil && sess.bp != nil && sess.state == boxPairPending {
		sess.cancel()
		if err := sess.bp.withdraw(ctx); err != nil {
			p.log.Debug("withdrawing the box pairing failed", "error", err)
		}
	}
	p.forgetPaired("unpaired from the panel")
	return p.pairView()
}

// pairErrorCode turns a pairing failure into the code and message the
// browser shows. The box's own codes (not-local, pairing-off, limit, …)
// pass through; the proxy adds fingerprint, unsupported and unreachable.
func pairErrorCode(err error) (code, msg string) {
	var pe *boxPairingError
	switch {
	case errors.Is(err, errPairFingerprint):
		return "fingerprint", err.Error()
	case errors.Is(err, errPairNoAddonScope):
		return "no-addon-scope", err.Error()
	case errors.As(err, &pe) && (pe.Status == http.StatusNotFound || pe.Status == http.StatusNotImplemented ||
		pe.Status == http.StatusMethodNotAllowed):
		return "unsupported", "the target offers no openccu-lite pairing"
	case errors.As(err, &pe) && pe.Code != "":
		return pe.Code, pe.Message
	case errors.As(err, &pe):
		return "failed", pe.Error()
	default:
		return "unreachable", err.Error()
	}
}

// sleepCtx waits d or until ctx ends.
func sleepCtx(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
