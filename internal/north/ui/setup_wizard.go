// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ui

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// SetupWizardDeps bundles the three SQLite-backed stores the wizard
// writes to during finalization, plus the in-memory session store that
// carries accumulated state between the four POST steps.
type SetupWizardDeps struct {
	// Users is the SQLite-backed user store. A single admin row is
	// written during finalization (step 4).
	Users *sqlite.UserStore
	// Centrals is the SQLite-backed central store. One row is written
	// when the user does not skip step 3.
	Centrals *sqlite.CentralsStore
	// Sections is the SQLite-backed config-section store. The "locale"
	// section and, optionally, "north.mqtt" are written at finalization.
	Sections *sqlite.ConfigSectionStore
	// Sessions is the in-memory wizard-session store.
	Sessions *SetupSessionStore
}

// setupWizardPageData is the template envelope for all wizard steps.
type setupWizardPageData struct {
	Step  int    // current step number (1..4)
	Total int    // always 4
	Error string // non-empty when a previous POST had a validation problem
	State SetupState
}

// handleSetupWizard serves GET /setup. It reads (or creates) the wizard
// session cookie and renders the template for the current step.
func handleSetupWizard(d Deps, tpl *templateSet, wd SetupWizardDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if wd.Users == nil || wd.Sessions == nil {
			// Fall back to the legacy single-step handler when deps are absent.
			handleSetup(d, tpl)(w, r)
			return
		}
		// Single-shot guarantee: redirect to /login when any user already exists.
		if count, err := wd.Users.Count(r.Context()); err == nil && count > 0 {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		sess := resolveOrIssueSession(w, r, wd.Sessions)
		errMsg := r.URL.Query().Get("wzerr")
		render(tpl, w, r, templateForStep(sess.State.Step), pageData{
			Title: "Setup", Lang: d.Lang,
			Data: setupWizardPageData{
				Step:  sess.State.Step,
				Total: 4,
				Error: errMsg,
				State: sess.State,
			},
		})
	}
}

// handleSetupAdmin handles POST /setup/admin (wizard step 1).
// Validates username (non-empty) and password (≥8 chars, matches
// confirm), then advances the session to step 2 and redirects to /setup.
func handleSetupAdmin(wd SetupWizardDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if wd.Users == nil || wd.Sessions == nil {
			http.Error(w, "setup not configured", http.StatusServiceUnavailable)
			return
		}
		if count, err := wd.Users.Count(r.Context()); err == nil && count > 0 {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		user := strings.TrimSpace(r.FormValue("username"))
		pass := r.FormValue("password")
		confirm := r.FormValue("confirm")
		if user == "" || len(pass) < 8 || pass != confirm {
			http.Redirect(w, r, "/setup?wzerr=admin", http.StatusSeeOther)
			return
		}
		sess := resolveOrIssueSession(w, r, wd.Sessions)
		state := sess.State
		state.AdminUsername = user
		state.AdminPassword = pass
		state.Step = 2
		wd.Sessions.Save(sess.ID, state)
		slog.InfoContext(r.Context(), "setup.wizard.step1.ok",
			slog.String("subject", user))
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
	}
}

// handleSetupLocale handles POST /setup/locale (wizard step 2).
// Validates locale (de|en) and theme (light|dark|system), saves, then
// advances to step 3.
func handleSetupLocale(wd SetupWizardDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if wd.Sessions == nil {
			http.Error(w, "setup not configured", http.StatusServiceUnavailable)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		locale := r.FormValue("locale")
		theme := r.FormValue("theme")
		if locale != "de" && locale != "en" {
			http.Redirect(w, r, "/setup?wzerr=locale", http.StatusSeeOther)
			return
		}
		if theme != "light" && theme != "dark" && theme != "system" {
			http.Redirect(w, r, "/setup?wzerr=theme", http.StatusSeeOther)
			return
		}
		sess := resolveOrIssueSession(w, r, wd.Sessions)
		state := sess.State
		state.Locale = locale
		state.Theme = theme
		state.Step = 3
		wd.Sessions.Save(sess.ID, state)
		slog.InfoContext(r.Context(), "setup.wizard.step2.ok",
			slog.String("locale", locale), slog.String("theme", theme))
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
	}
}

// handleSetupCCU handles POST /setup/ccu (wizard step 3).
// When the form contains skip=1 the step is marked as skipped. Otherwise
// name, host, and at least one interface are required.
func handleSetupCCU(wd SetupWizardDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if wd.Sessions == nil {
			http.Error(w, "setup not configured", http.StatusServiceUnavailable)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		sess := resolveOrIssueSession(w, r, wd.Sessions)
		state := sess.State
		if r.FormValue("skip") == "1" {
			state.SkipCCU = true
			state.Step = 4
			wd.Sessions.Save(sess.ID, state)
			http.Redirect(w, r, "/setup", http.StatusSeeOther)
			return
		}
		name := strings.TrimSpace(r.FormValue("ccu_name"))
		host := strings.TrimSpace(r.FormValue("ccu_host"))
		ifaces := r.Form["ccu_interfaces"]
		if name == "" || host == "" || len(ifaces) == 0 {
			http.Redirect(w, r, "/setup?wzerr=ccu", http.StatusSeeOther)
			return
		}
		state.CCUName = name
		state.CCUHost = host
		state.CCUUsername = strings.TrimSpace(r.FormValue("ccu_username"))
		state.CCUPassword = r.FormValue("ccu_password")
		state.CCUInterfaces = ifaces
		state.SkipCCU = false
		state.Step = 4
		wd.Sessions.Save(sess.ID, state)
		slog.InfoContext(r.Context(), "setup.wizard.step3.ok",
			slog.String("ccu_name", name), slog.String("ccu_host", host))
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
	}
}

// handleSetupMQTT handles POST /setup/mqtt (wizard step 4).
// After validating MQTT fields (or honouring a skip), it runs
// finalization: writes the admin user, locale section, optional CCU
// row, and optional MQTT section, then drops the wizard session and
// redirects to /login?setup_done=1.
func handleSetupMQTT(d Deps, wd SetupWizardDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if wd.Users == nil || wd.Sessions == nil {
			http.Error(w, "setup not configured", http.StatusServiceUnavailable)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		sess := resolveOrIssueSession(w, r, wd.Sessions)
		state := sess.State
		if r.FormValue("skip") != "1" {
			enabled := r.FormValue("mqtt_enabled") == "1"
			broker := strings.TrimSpace(r.FormValue("mqtt_broker_url"))
			if enabled && broker == "" {
				http.Redirect(w, r, "/setup?wzerr=mqtt", http.StatusSeeOther)
				return
			}
			state.MQTTEnabled = enabled
			state.MQTTBrokerURL = broker
			state.MQTTUsername = strings.TrimSpace(r.FormValue("mqtt_username"))
			state.MQTTPassword = r.FormValue("mqtt_password")
			state.SkipMQTT = false
		} else {
			state.SkipMQTT = true
		}
		// Finalize — write all collected data to the SQLite stores.
		if err := finalizeSetup(r, d, wd, state); err != nil {
			slog.ErrorContext(r.Context(), "setup.wizard.finalize.fail",
				slog.String("err", err.Error()))
			http.Error(w, "setup finalization failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		wd.Sessions.Drop(sess.ID)
		slog.InfoContext(r.Context(), "setup.wizard.complete",
			slog.String("subject", state.AdminUsername))
		http.Redirect(w, r, "/login?setup_done=1", http.StatusSeeOther)
	}
}

// finalizeSetup commits all accumulated wizard state to the SQLite stores.
// The order is: user first (required), locale section, CCU row (if not
// skipped), MQTT section (if not skipped).
func finalizeSetup(r *http.Request, _ Deps, wd SetupWizardDeps, state SetupState) error {
	ctx := r.Context()
	actor := state.AdminUsername

	// 1. Create the admin user.
	if err := wd.Users.Put(ctx, actor, state.AdminPassword, auth.RoleAdmin); err != nil {
		return err
	}

	// 2. Persist the locale/theme section.
	localeSec, err := json.Marshal(map[string]string{
		"locale": state.Locale,
		"theme":  state.Theme,
	})
	if err != nil {
		return err
	}
	if _, err := wd.Sections.Put(ctx, "locale", localeSec, actor); err != nil {
		return err
	}

	// 3. Persist the first CCU if not skipped.
	if !state.SkipCCU && wd.Centrals != nil {
		ifaces := make([]config.InterfaceSpec, 0, len(state.CCUInterfaces))
		for _, name := range state.CCUInterfaces {
			if name = strings.TrimSpace(name); name != "" {
				ifaces = append(ifaces, config.InterfaceSpec{Name: name})
			}
		}
		row := sqlite.CentralRow{
			Name:       state.CCUName,
			Host:       state.CCUHost,
			Username:   state.CCUUsername,
			Interfaces: ifaces,
			Enabled:    true,
		}
		if state.CCUPassword != "" {
			row.PasswordPlain = state.CCUPassword
		}
		if err := wd.Centrals.Put(ctx, row); err != nil {
			return err
		}
	}

	// 4. Persist MQTT if enabled and not skipped.
	if !state.SkipMQTT && state.MQTTEnabled && wd.Sections != nil {
		mqttSec, err := json.Marshal(map[string]string{
			"broker_url": state.MQTTBrokerURL,
			"username":   state.MQTTUsername,
			"password":   state.MQTTPassword,
		})
		if err != nil {
			return err
		}
		if _, err := wd.Sections.Put(ctx, "north.mqtt", mqttSec, actor); err != nil {
			return err
		}
	}

	return nil
}

// resolveOrIssueSession reads the wizard cookie from the request. If no
// valid session exists it issues a new one and writes the Set-Cookie
// header. The returned pointer is always non-nil.
func resolveOrIssueSession(w http.ResponseWriter, r *http.Request, store *SetupSessionStore) *SetupSession {
	if c, err := r.Cookie(setupCookieName); err == nil {
		if sess := store.Lookup(c.Value); sess != nil {
			return sess
		}
	}
	sess := store.Issue()
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure omitted intentionally: setup wizard runs over HTTP on the local network before TLS is configured; see #20
		Name:     setupCookieName,
		Value:    sess.ID,
		Path:     "/setup",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	return sess
}

// templateForStep maps a wizard step number to the template filename.
func templateForStep(step int) string {
	switch step {
	case 2:
		return "setup_locale.html"
	case 3:
		return "setup_ccu.html"
	case 4:
		return "setup_mqtt.html"
	default:
		return "setup_admin.html"
	}
}
