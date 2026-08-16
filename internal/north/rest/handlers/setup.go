// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// SetupService backs the first-run onboarding endpoints. The SPA renders the
// multi-step wizard and POSTs the accumulated state in one atomic request;
// the daemon persists the admin user, locale preference, optional CCU, and
// optional MQTT section to SQLite. Nil stores mean "no durable backend" — the
// endpoints then report setup as not required and refuse to finalize.
type SetupService struct {
	Users *sqlite.UserStore
	// Centrals persists the wizard's optional CCU. It is the same service
	// POST /admin/centrals writes through, so the CCU the operator names in
	// the wizard is adopted live instead of only landing in the table — a
	// central that is merely persisted stays dark (and CCU-delegated login
	// stays impossible) until the next daemon restart.
	Centrals CentralAdminService
	Sections *sqlite.ConfigSectionStore
	// Required reports first-run state: true only when there is no way to
	// authenticate yet (no local admin, no YAML user, no CCU-delegated login,
	// no OIDC) AND the operator has not closed the surface. Mirrors the
	// daemon's firstRunNeedsSetup probe. When false the finalize endpoint is
	// closed so nobody can register a second admin.
	Required func(context.Context) bool
	// FirstRunAllowed reports the operator's `bootstrap.allow_first_run_setup`
	// toggle. It is the same input Required already folds in; it is carried
	// separately only so the refusal can say which of the two closed the
	// surface — a daemon that answered "setup already completed" on a
	// database with zero users would send the operator hunting for an
	// account that does not exist. Nil means allowed.
	FirstRunAllowed func() bool
}

// setupStatusResponse is the body of GET /api/v1/setup/status.
type setupStatusResponse struct {
	Required bool `json:"required"`
}

// setupRequest is the atomic onboarding payload. `ccu` and `mqtt` are
// optional — a nil object means the operator skipped that step.
type setupRequest struct {
	Admin  setupAdmin  `json:"admin"`
	Locale setupLocale `json:"locale"`
	CCU    *setupCCU   `json:"ccu,omitempty"`
	MQTT   *setupMQTT  `json:"mqtt,omitempty"`
}

type setupAdmin struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type setupLocale struct {
	Locale string `json:"locale"`
	Theme  string `json:"theme"`
}

type setupCCU struct {
	Name       string   `json:"name"`
	Host       string   `json:"host"`
	Username   string   `json:"username,omitempty"`
	Password   string   `json:"password,omitempty"`
	Interfaces []string `json:"interfaces"`
}

type setupMQTT struct {
	BrokerURL string `json:"broker_url"`
	Username  string `json:"username,omitempty"`
	Password  string `json:"password,omitempty"`
}

// minSetupPasswordLen mirrors the wizard's prior server-rendered validation.
const minSetupPasswordLen = 8

// SetupStatus reports whether first-run onboarding is still required. The SPA
// probes it on boot to decide between the setup wizard and the login screen.
// Leaks only a single boolean.
//
// A caller that already carries an authenticated identity never needs first-run
// onboarding — in particular the HA Ingress passthrough (ADR 0044) injects an
// admin identity before this handler runs, so reporting `required: true` there
// would trap an already-logged-in admin in the wizard (the very friction ADR
// 0044 set out to remove). When an identity is present we short-circuit to
// `required: false` regardless of whether a persistent auth source exists yet.
func SetupStatus(s *SetupService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := auth.IdentityFrom(r.Context()); ok {
			JSON(w, http.StatusOK, setupStatusResponse{Required: false})
			return
		}
		JSON(w, http.StatusOK, setupStatusResponse{Required: setupRequired(r.Context(), s)})
	}
}

// Setup finalizes first-run onboarding. Unauthenticated by necessity (no admin
// exists yet) but hard-gated on the first-run probe: once any authentication
// source exists it returns 409 so a second admin can never be registered this
// way, and an operator who closed the surface with
// `bootstrap.allow_first_run_setup: false` gets 403 regardless of the users
// table. On success the SPA returns to the login screen and the operator signs
// in with the just-created admin account.
func Setup(s *SetupService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s == nil || s.Users == nil || s.Sections == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Setup unavailable", "no durable store wired"))
			return
		}
		// Hardening gate: the operator disabled onboarding outright. Answered
		// before the first-run probe so the reason is accurate even on a
		// database with no users at all.
		if s.FirstRunAllowed != nil && !s.FirstRunAllowed() {
			problem.Write(w, http.StatusForbidden,
				problem.New(problem.TypeForbidden, r, "First-run setup disabled",
					"bootstrap.allow_first_run_setup is false"))
			return
		}
		// Single-shot guarantee: refuse once any auth source exists. This is the
		// security gate — the endpoint is otherwise open to anyone on the network.
		if !setupRequired(r.Context(), s) {
			problem.Write(w, http.StatusConflict,
				problem.New(problem.TypeConflict, r, "Setup already completed", "an authentication source already exists"))
			return
		}
		var req setupRequest
		if err := DecodeJSON(r, &req); err != nil {
			problem.Write(w, DecodeJSONStatus(err),
				problem.New(problem.TypeBadRequest, r, "Invalid JSON", err.Error()))
			return
		}
		if msg := validateSetup(&req); msg != "" {
			problem.Write(w, http.StatusUnprocessableEntity,
				problem.New(problem.TypeValidation, r, "Invalid setup payload", msg))
			return
		}
		if err := finalizeSetup(r.Context(), s, &req); err != nil {
			// A CCU password that cannot be encrypted at rest is refused by
			// the centrals store. That is the operator's configuration, not a
			// server fault, so it answers as a client error naming the knob.
			if writeCentralSecretRefusal(w, r, err) {
				slog.WarnContext(r.Context(), "setup.finalize.refused", slog.String("err", err.Error()))
				return
			}
			slog.ErrorContext(r.Context(), "setup.finalize.fail", slog.String("err", err.Error()))
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Setup finalization failed", err)
			return
		}
		slog.InfoContext(r.Context(), "setup.complete", slog.String("subject", req.Admin.Username))
		w.WriteHeader(http.StatusNoContent)
	}
}

// setupRequired is the nil-safe first-run probe. A SetupService without a
// Required function (or without stores) reports "not required" so the wizard
// never traps an operator on a backend that cannot persist anything.
func setupRequired(ctx context.Context, s *SetupService) bool {
	if s == nil || s.Users == nil || s.Required == nil {
		return false
	}
	return s.Required(ctx)
}

// validateSetup mirrors the prior wizard's per-step validation. Returns an
// empty string when the payload is valid, otherwise a human-readable reason.
func validateSetup(req *setupRequest) string {
	req.Admin.Username = strings.TrimSpace(req.Admin.Username)
	if req.Admin.Username == "" {
		return "admin.username must not be empty"
	}
	if len(req.Admin.Password) < minSetupPasswordLen {
		return "admin.password must be at least 8 characters"
	}
	if req.Locale.Locale != "de" && req.Locale.Locale != "en" {
		return "locale.locale must be one of: de, en"
	}
	switch req.Locale.Theme {
	case "light", "dark", "system":
	default:
		return "locale.theme must be one of: light, dark, system"
	}
	if req.CCU != nil {
		req.CCU.Name = strings.TrimSpace(req.CCU.Name)
		req.CCU.Host = strings.TrimSpace(req.CCU.Host)
		ifaces := make([]string, 0, len(req.CCU.Interfaces))
		for _, name := range req.CCU.Interfaces {
			if name = strings.TrimSpace(name); name != "" {
				ifaces = append(ifaces, name)
			}
		}
		req.CCU.Interfaces = ifaces
		if req.CCU.Name == "" || req.CCU.Host == "" || len(req.CCU.Interfaces) == 0 {
			return "ccu requires name, host, and at least one interface"
		}
		// The wizard prefills this from the CCU's SSDP friendly name, which
		// routinely contains spaces. The name ends up as a path segment of
		// the callback URL, so an unroutable one produces a first-run install
		// that never receives a single event.
		if err := hmtypes.ValidateCentralName(req.CCU.Name); err != nil {
			return "ccu." + err.Error()
		}
	}
	if req.MQTT != nil {
		req.MQTT.BrokerURL = strings.TrimSpace(req.MQTT.BrokerURL)
		if req.MQTT.BrokerURL == "" {
			return "mqtt.broker_url must not be empty"
		}
	}
	return ""
}

// finalizeSetup commits the validated onboarding payload to SQLite: admin
// user first (required), then the locale section, the optional MQTT section,
// and the optional CCU last. The CCU comes last because it is the only step
// with an effect beyond persistence — [CentralAdminService] also brings the
// central up live — so it is the only one that can fail for a reason the
// preceding steps do not share. Ordering it last keeps such a failure from
// swallowing settings the operator already got right. The persisted shape is
// unchanged.
func finalizeSetup(ctx context.Context, s *SetupService, req *setupRequest) error {
	actor := req.Admin.Username

	if err := s.Users.Put(ctx, actor, req.Admin.Password, auth.RoleAdmin); err != nil {
		return err
	}

	localeSec, err := json.Marshal(map[string]string{
		"locale": req.Locale.Locale,
		"theme":  req.Locale.Theme,
	})
	if err != nil {
		return err
	}
	if _, err := s.Sections.Put(ctx, "locale", localeSec, actor); err != nil {
		return err
	}

	if req.MQTT != nil {
		// The wizard sends the object only when the operator switched MQTT
		// on, so the section carries the switch: the persisted overlay is
		// sparse and `enabled` has no default, so a section without it
		// leaves the bridge off and the whole step inert.
		mqttSec, err := json.Marshal(map[string]any{
			"enabled":    true,
			"broker_url": req.MQTT.BrokerURL,
			"username":   req.MQTT.Username,
			"password":   req.MQTT.Password,
		})
		if err != nil {
			return err
		}
		if _, err := s.Sections.Put(ctx, "north.mqtt", mqttSec, actor); err != nil {
			return err
		}
	}

	if req.CCU != nil && s.Centrals != nil {
		ifaces := make([]config.InterfaceSpec, 0, len(req.CCU.Interfaces))
		for _, name := range req.CCU.Interfaces {
			ifaces = append(ifaces, config.InterfaceSpec{Name: name})
		}
		row := sqlite.CentralRow{
			Name:       req.CCU.Name,
			Host:       req.CCU.Host,
			Username:   req.CCU.Username,
			Interfaces: ifaces,
			Enabled:    true,
		}
		if req.CCU.Password != "" {
			row.PasswordPlain = req.CCU.Password
		}
		if err := s.Centrals.Put(ctx, row); err != nil {
			return err
		}
	}

	return nil
}
