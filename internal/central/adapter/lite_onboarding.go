// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/build"
	"github.com/SukramJ/openccu-loom/internal/client/transport/occulited"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/i18n"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// liteSessionTTL bounds how long a pairing session — and the token an
// approved one holds — lives in the daemon.
const liteSessionTTL = 10 * time.Minute

// litePollWaitMax caps one status long-poll.
const litePollWaitMax = 25 * time.Second

// liteAccessLevels are the access presets the wizard offers, per area.
var liteAccessLevels = map[string]occulited.PairingAccess{
	"full":    {Devices: "administer", Names: "configure", System: "configure"},
	"control": {Devices: "operate", Names: "read", System: "read"},
	"read":    {Devices: "read", Names: "read", System: "read"},
}

// LiteOnboarding identifies a system before it is added and pairs the
// daemon with an openccu-lite box. The token a pairing yields stays in
// the daemon's memory until a central takes it (once), or the session
// expires; it is never handed to a client.
type LiteOnboarding struct {
	instance string
	catalogs *i18n.Catalogs
	locale   string
	logger   *slog.Logger

	mu       sync.Mutex
	sessions map[string]*liteSession
}

type liteSession struct {
	client      *occulited.Client
	pairing     *occulited.Pairing
	fingerprint string
	created     time.Time
	state       string
	token       string
	scopes      []string
}

// NewLiteOnboarding builds the domain. instance names this daemon on the
// box's approval screen; catalogs and locale localise the purpose text.
func NewLiteOnboarding(instance string, catalogs *i18n.Catalogs, locale string, logger *slog.Logger) *LiteOnboarding {
	if logger == nil {
		logger = slog.Default()
	}
	return &LiteOnboarding{
		instance: instance, catalogs: catalogs, locale: locale, logger: logger,
		sessions: map[string]*liteSession{},
	}
}

func liteBaseURL(host string, port int, tls bool) string {
	scheme, def := "http", hmenum.DefaultJSONRPCPort
	if tls {
		scheme, def = "https", hmenum.DefaultJSONRPCTLSPort
	}
	if port <= 0 {
		port = def
	}
	// JoinHostPort brackets an IPv6 literal itself; one given in its URL
	// form ("[::1]", which the host rule accepts) would end up bracketed
	// twice.
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	return scheme + "://" + net.JoinHostPort(host, strconv.Itoa(port))
}

// validateLiteAddress applies the centrals[].host rule and the TCP port
// range to an address the onboarding is about to contact. The host goes
// into the request URL, so a scheme, path, fragment or credentials in it
// would reshape that URL rather than name a system.
func validateLiteAddress(host string, port int) error {
	if host == "" {
		return fmt.Errorf("%w: host is required", hmerr.ErrValidation)
	}
	if err := config.ValidateCentralHost(host); err != nil {
		return fmt.Errorf("%w: %w", hmerr.ErrValidation, err)
	}
	if port < 0 || port > 65535 {
		return fmt.Errorf("%w: port %d is out of range", hmerr.ErrValidation, port)
	}
	return nil
}

// Probe identifies the system at an address. Over HTTPS a certificate no
// trusted authority signed is not a failure here: the probe reads the
// open version document under a pin of the certificate the server
// presented and reports that fingerprint, which the operator then
// confirms by pinning it. Nothing but that open document is requested and
// no credential is sent.
func (o *LiteOnboarding) Probe(ctx context.Context, in hmapi.CentralProbeRequest) (hmapi.CentralProbeResult, error) {
	if err := validateLiteAddress(in.Host, in.Port); err != nil {
		return hmapi.CentralProbeResult{}, err
	}
	cfg := occulited.Config{
		BaseURL: liteBaseURL(in.Host, in.Port, in.TLS), InsecureSkipVerify: in.TLSInsecureSkipVerify,
		Logger: o.logger, Timeout: 10 * time.Second,
	}
	det, err := detectOnce(ctx, cfg)
	if fp, ok := occulited.FingerprintOf(err); ok && in.TLS {
		cfg.TLSFingerprint = fp
		cfg.InsecureSkipVerify = false
		det, err = detectOnce(ctx, cfg)
		if det.TLSFingerprint == "" {
			det.TLSFingerprint = fp
		}
	}
	if err != nil && !errors.Is(err, occulited.ErrUnsupportedMajor) {
		return hmapi.CentralProbeResult{}, err
	}
	out := hmapi.CentralProbeResult{SystemType: "unknown", TLSFingerprint: det.TLSFingerprint}
	switch det.Kind {
	case occulited.DetectLite:
		out.SystemType, out.Ready = string(hmenum.SystemTypeOpenCCULite), err == nil
		info := &hmapi.LiteProbeInfo{APIMajors: det.Majors, PairingAvailable: det.Pairing}
		if v := det.Version; v != nil {
			info.Implementation = v.Implementation
			if v.HMIP != nil {
				info.HMIPKeyMode = &hmapi.HMIPKeyMode{
					KeyserverMode: v.HMIP.KeyserverMode, DeviceKeys: v.HMIP.DeviceKeys, OfflinePairing: v.HMIP.OfflinePairing,
				}
			}
		}
		out.Lite = info
	case occulited.DetectLiteNotReady:
		out.SystemType = string(hmenum.SystemTypeOpenCCULite)
	case occulited.DetectCCU:
		out.SystemType, out.Ready = string(hmenum.SystemTypeCCU), true
	case occulited.DetectUnknown:
	}
	return out, nil
}

func detectOnce(ctx context.Context, cfg occulited.Config) (occulited.Detection, error) {
	c, err := occulited.New(cfg)
	if err != nil {
		return occulited.Detection{}, err
	}
	return c.Detect(ctx)
}

// StartPairing asks the box's administrator to approve this daemon. The
// answer carries the code the administrator enters on the box.
func (o *LiteOnboarding) StartPairing(ctx context.Context, in hmapi.CentralPairingRequest) (hmapi.CentralPairingStarted, error) {
	if err := validateLiteAddress(in.Host, in.Port); err != nil {
		return hmapi.CentralPairingStarted{}, err
	}
	accessName := in.Access
	if accessName == "" {
		accessName = "full"
	}
	access, ok := liteAccessLevels[accessName]
	if !ok {
		return hmapi.CentralPairingStarted{}, fmt.Errorf("%w: access must be full, control or read", hmerr.ErrValidation)
	}
	client, err := occulited.New(occulited.Config{
		BaseURL: liteBaseURL(in.Host, in.Port, in.TLS), TLSFingerprint: in.TLSFingerprint, Logger: o.logger,
	})
	if err != nil {
		return hmapi.CentralPairingStarted{}, fmt.Errorf("%w: %w", hmerr.ErrValidation, err)
	}
	p, err := client.StartPairing(ctx, occulited.PairingRequest{
		App: "openccu-loom", AppVersion: build.Version, Instance: o.instance,
		Name: "OpenCCU-Loom (" + o.instance + ")", Access: access, Purpose: o.purposes(access),
	})
	if err != nil {
		return hmapi.CentralPairingStarted{}, pairingRefusal(err, in.TLSFingerprint != "")
	}
	id, err := newSessionID()
	if err != nil {
		return hmapi.CentralPairingStarted{}, err
	}
	o.mu.Lock()
	o.purgeLocked()
	o.sessions[id] = &liteSession{client: client, pairing: p, fingerprint: p.Fingerprint, created: time.Now(), state: "pending"}
	o.mu.Unlock()
	return hmapi.CentralPairingStarted{
		PairingID: id, Code: p.Code, Fingerprint: p.Fingerprint, ExpiresIn: int(p.ExpiresIn / time.Second),
	}, nil
}

// PairingStatus reports a pairing's state, waiting up to wait for the box
// to answer. An approved pairing's token is taken into the session here
// and kept for [LiteOnboarding.TakePairing].
func (o *LiteOnboarding) PairingStatus(ctx context.Context, id string, wait time.Duration) (hmapi.CentralPairingStatus, error) {
	o.mu.Lock()
	o.purgeLocked()
	s, ok := o.sessions[id]
	var state string
	var scopes []string
	if ok {
		state, scopes = s.state, s.scopes
	}
	o.mu.Unlock()
	if !ok {
		return hmapi.CentralPairingStatus{}, hmerr.ErrPairingNotFound
	}
	if state != "pending" {
		return hmapi.CentralPairingStatus{State: state, Scopes: scopes}, nil
	}
	res, err := s.client.PollPairing(ctx, s.pairing, min(wait, litePollWaitMax))
	if err != nil {
		var apiErr *occulited.APIError
		if errors.As(err, &apiErr) && apiErr.Code == occulited.CodeSlowDown {
			return hmapi.CentralPairingStatus{State: "pending"}, nil
		}
		return hmapi.CentralPairingStatus{State: "error", Error: err.Error()}, nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	switch res.State {
	case "approved":
		s.state, s.token, s.scopes = "approved", res.Token, res.Scopes
	case "rejected", "expired":
		s.state = res.State
	}
	return hmapi.CentralPairingStatus{State: s.state, Scopes: s.scopes}, nil
}

// CancelPairing withdraws a pairing on the box and forgets it.
func (o *LiteOnboarding) CancelPairing(ctx context.Context, id string) error {
	o.mu.Lock()
	s, ok := o.sessions[id]
	pending := ok && s.state == "pending"
	delete(o.sessions, id)
	o.mu.Unlock()
	if !ok {
		return hmerr.ErrPairingNotFound
	}
	if pending {
		return s.client.CancelPairing(ctx, s.pairing)
	}
	return nil
}

// PairingToken hands an approved pairing's token and pinned fingerprint
// to the central being created or updated. The session stays until
// [LiteOnboarding.ForgetPairing], so a write that fails validation can be
// corrected without pairing again.
func (o *LiteOnboarding) PairingToken(id string) (token, fingerprint string, err error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.purgeLocked()
	s, ok := o.sessions[id]
	if !ok {
		return "", "", hmerr.ErrPairingNotFound
	}
	if s.state != "approved" || s.token == "" {
		return "", "", hmerr.ErrPairingNotApproved
	}
	return s.token, s.fingerprint, nil
}

// ForgetPairing drops a pairing session and the token it holds; the
// central that took the token has persisted it.
func (o *LiteOnboarding) ForgetPairing(id string) {
	o.mu.Lock()
	delete(o.sessions, id)
	o.mu.Unlock()
}

// purgeLocked drops sessions older than the TTL. Callers hold o.mu.
func (o *LiteOnboarding) purgeLocked() {
	cutoff := time.Now().Add(-liteSessionTTL)
	for id, s := range o.sessions {
		if s.created.Before(cutoff) {
			delete(o.sessions, id)
		}
	}
}

// purposes tells the box's administrator, per requested area, what the
// access is for, in the daemon's language.
func (o *LiteOnboarding) purposes(a occulited.PairingAccess) map[string]string {
	t := func(key string) string {
		if o.catalogs == nil {
			return key
		}
		return o.catalogs.T(o.locale, key)
	}
	out := map[string]string{}
	if a.Devices != "" {
		out["devices"] = t("onboarding.pairing.purpose.devices")
	}
	if a.Names != "" {
		out["names"] = t("onboarding.pairing.purpose.names")
	}
	if a.System != "" {
		out["system"] = t("onboarding.pairing.purpose.system")
	}
	return out
}

func newSessionID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("onboarding: session id: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// pairingRefusal names what the operator can do about a pairing the box
// refused; any other error is returned as it is.
func pairingRefusal(err error, pinned bool) error {
	var apiErr *occulited.APIError
	fp, tlsRefused := occulited.FingerprintOf(err)
	switch {
	case tlsRefused && !pinned:
		return fmt.Errorf("%w: the box's certificate is not signed by a trusted authority (fingerprint %s); probe the box and pin its fingerprint", hmerr.ErrValidation, fp)
	case tlsRefused:
		return fmt.Errorf("%w: the box presented a certificate other than the pinned one (fingerprint %s); probe the box again and compare the fingerprint with the box", hmerr.ErrValidation, fp)
	case errors.Is(err, occulited.ErrPairingFingerprintMismatch):
		return fmt.Errorf("%w: the certificate the box reports is not the one this connection saw — possible interception; check the fingerprint", hmerr.ErrValidation)
	case errors.As(err, &apiErr) && apiErr.Code == occulited.CodeNotLocal:
		return fmt.Errorf("%w: the box accepts pairing requests only from its local network; pair from there or paste an API token", hmerr.ErrPermissionDenied)
	case errors.As(err, &apiErr) && apiErr.Code == occulited.CodePairingOff:
		return fmt.Errorf("%w: client pairing is switched off on the box; switch it on there or paste an API token", hmerr.ErrPermissionDenied)
	}
	return err
}
