// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/i18n"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// openMu serialises sqlite.Open calls in this test file to avoid the
// package-level goose race (same pattern as the sqlite package's own
// testhelper_test.go).
var setupTestOpenMu sync.Mutex

// openWizardDB opens a migrated SQLite database in t's temp dir.
func openWizardDB(t *testing.T) *sqlite.UserStore {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "wizard.db")
	setupTestOpenMu.Lock()
	db, err := sqlite.Open(context.Background(), dsn)
	setupTestOpenMu.Unlock()
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return sqlite.NewUserStore(db)
}

// openWizardStores opens a migrated SQLite database and returns all three stores.
func openWizardStores(t *testing.T) (*sqlite.UserStore, *sqlite.CentralsStore, *sqlite.ConfigSectionStore) {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "wizard_full.db")
	setupTestOpenMu.Lock()
	db, err := sqlite.Open(context.Background(), dsn)
	setupTestOpenMu.Unlock()
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return sqlite.NewUserStore(db), sqlite.NewCentralsStore(db), sqlite.NewConfigSectionStore(db)
}

// newWizardRouter builds a chi router wired for the multi-step wizard.
func newWizardRouter(t *testing.T, wd SetupWizardDeps) http.Handler {
	t.Helper()
	cats, err := i18n.NewCatalogs()
	if err != nil {
		t.Fatalf("catalogs: %v", err)
	}
	return NewRouter(Deps{
		Lang:     "en",
		Catalogs: cats,
		Setup:    &wd,
	})
}

// wizardCookie extracts the setup session cookie from a response (if any).
func wizardCookie(rr *httptest.ResponseRecorder) string {
	for _, line := range rr.Header().Values("Set-Cookie") {
		if after, ok := strings.CutPrefix(line, setupCookieName+"="); ok {
			val := after
			val = strings.SplitN(val, ";", 2)[0]
			return val
		}
	}
	return ""
}

// postForm issues a POST with URL-encoded form data, attaching the
// given cookie when non-empty, and returns the response recorder.
func postForm(t *testing.T, h http.Handler, path, body, cookieVal string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cookieVal != "" {
		req.AddCookie(&http.Cookie{Name: setupCookieName, Value: cookieVal})
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// getPage issues a GET, attaching the given cookie when non-empty.
func getPage(t *testing.T, h http.Handler, path, cookieVal string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, http.NoBody)
	if cookieVal != "" {
		req.AddCookie(&http.Cookie{Name: setupCookieName, Value: cookieVal})
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// ---------------------------------------------------------------------------
// Step 1 — admin happy path
// ---------------------------------------------------------------------------

func TestWizardStep1HappyPathAdvancesToStep2(t *testing.T) {
	users := openWizardDB(t)
	sessions := NewSetupSessionStore()
	wd := SetupWizardDeps{Users: users, Sessions: sessions}
	h := newWizardRouter(t, wd)

	// GET /setup — should render step 1 and set a session cookie.
	rr := getPage(t, h, "/setup", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /setup: status=%d body=%s", rr.Code, rr.Body.String())
	}
	cookieVal := wizardCookie(rr)
	if cookieVal == "" {
		t.Fatal("expected setup session cookie after GET /setup")
	}

	// POST /setup/admin with valid data.
	rr2 := postForm(t, h, "/setup/admin",
		"username=admin&password=supersecret&confirm=supersecret", cookieVal)
	if rr2.Code != http.StatusSeeOther {
		t.Fatalf("POST /setup/admin: status=%d body=%s", rr2.Code, rr2.Body.String())
	}
	if rr2.Header().Get("Location") != "/setup" {
		t.Fatalf("expected redirect to /setup, got %q", rr2.Header().Get("Location"))
	}

	// Session should now be at step 2.
	sess := sessions.Lookup(cookieVal)
	if sess == nil {
		t.Fatal("session must still exist after step 1")
	}
	if sess.State.Step != 2 {
		t.Fatalf("expected step=2, got %d", sess.State.Step)
	}
	if sess.State.AdminUsername != "admin" {
		t.Fatalf("expected AdminUsername=admin, got %q", sess.State.AdminUsername)
	}

	// GET /setup with existing cookie should now render step 2.
	rr3 := getPage(t, h, "/setup", cookieVal)
	if rr3.Code != http.StatusOK {
		t.Fatalf("GET /setup step2: status=%d", rr3.Code)
	}
	if !strings.Contains(rr3.Body.String(), "setup/locale") {
		t.Fatalf("expected locale form action in body, got:\n%s", rr3.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Step 1 — password mismatch
// ---------------------------------------------------------------------------

func TestWizardStep1PasswordMismatchRedirectsWithError(t *testing.T) {
	users := openWizardDB(t)
	sessions := NewSetupSessionStore()
	wd := SetupWizardDeps{Users: users, Sessions: sessions}
	h := newWizardRouter(t, wd)

	// Issue a session first.
	rr := getPage(t, h, "/setup", "")
	cookieVal := wizardCookie(rr)

	// POST with mismatching passwords.
	rr2 := postForm(t, h, "/setup/admin",
		"username=admin&password=secret99&confirm=different", cookieVal)
	if rr2.Code != http.StatusSeeOther {
		t.Fatalf("status=%d", rr2.Code)
	}
	loc := rr2.Header().Get("Location")
	if !strings.Contains(loc, "wzerr=admin") {
		t.Fatalf("expected wzerr=admin in redirect, got %q", loc)
	}

	// Session must still be at step 1.
	if cookieVal != "" {
		sess := sessions.Lookup(cookieVal)
		if sess != nil && sess.State.Step != 1 {
			t.Fatalf("step must remain 1 on error, got %d", sess.State.Step)
		}
	}
}

// ---------------------------------------------------------------------------
// Step 1 — password too short (< 8 chars)
// ---------------------------------------------------------------------------

func TestWizardStep1ShortPasswordFails(t *testing.T) {
	users := openWizardDB(t)
	sessions := NewSetupSessionStore()
	wd := SetupWizardDeps{Users: users, Sessions: sessions}
	h := newWizardRouter(t, wd)

	rr := getPage(t, h, "/setup", "")
	cookieVal := wizardCookie(rr)

	rr2 := postForm(t, h, "/setup/admin",
		"username=admin&password=short&confirm=short", cookieVal)
	if rr2.Code != http.StatusSeeOther {
		t.Fatalf("status=%d", rr2.Code)
	}
	if !strings.Contains(rr2.Header().Get("Location"), "wzerr=admin") {
		t.Fatalf("expected wzerr=admin, got %q", rr2.Header().Get("Location"))
	}
}

// ---------------------------------------------------------------------------
// Reentry guard: if users.Count() > 0, GET /setup → /login
// ---------------------------------------------------------------------------

func TestWizardReentryRedirectsToLoginWhenUserExists(t *testing.T) {
	users := openWizardDB(t)
	// Pre-populate a user to simulate an already-configured daemon.
	if err := users.Put(context.Background(), "existing", "password1", "admin"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	sessions := NewSetupSessionStore()
	wd := SetupWizardDeps{Users: users, Sessions: sessions}
	h := newWizardRouter(t, wd)

	rr := getPage(t, h, "/setup", "")
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", rr.Code)
	}
	if rr.Header().Get("Location") != "/login" {
		t.Fatalf("expected redirect to /login, got %q", rr.Header().Get("Location"))
	}
}

// ---------------------------------------------------------------------------
// Full 4-step happy path + finalization
// ---------------------------------------------------------------------------

func TestWizardFullHappyPathFinalizes(t *testing.T) {
	users, centrals, sections := openWizardStores(t)
	sessions := NewSetupSessionStore()
	wd := SetupWizardDeps{
		Users:    users,
		Centrals: centrals,
		Sections: sections,
		Sessions: sessions,
	}
	h := newWizardRouter(t, wd)

	// Step 1 — GET to obtain cookie, then POST admin.
	rr := getPage(t, h, "/setup", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /setup: %d", rr.Code)
	}
	cookieVal := wizardCookie(rr)
	rr1 := postForm(t, h, "/setup/admin",
		"username=root&password=password1&confirm=password1", cookieVal)
	if rr1.Code != http.StatusSeeOther {
		t.Fatalf("step1 POST: %d body=%s", rr1.Code, rr1.Body.String())
	}

	// Step 2 — POST locale.
	rr2 := postForm(t, h, "/setup/locale",
		"locale=en&theme=light", cookieVal)
	if rr2.Code != http.StatusSeeOther {
		t.Fatalf("step2 POST: %d body=%s", rr2.Code, rr2.Body.String())
	}

	// Step 3 — POST CCU.
	rr3 := postForm(t, h, "/setup/ccu",
		"ccu_name=MyCCU&ccu_host=192.168.0.10&ccu_interfaces=HmIP-RF&ccu_interfaces=BidCos-RF",
		cookieVal)
	if rr3.Code != http.StatusSeeOther {
		t.Fatalf("step3 POST: %d body=%s", rr3.Code, rr3.Body.String())
	}

	// Step 4 — POST MQTT (skip).
	rr4 := postForm(t, h, "/setup/mqtt", "skip=1", cookieVal)
	if rr4.Code != http.StatusSeeOther {
		t.Fatalf("step4 POST: %d body=%s", rr4.Code, rr4.Body.String())
	}
	loc := rr4.Header().Get("Location")
	if loc != "/login?setup_done=1" {
		t.Fatalf("expected /login?setup_done=1, got %q", loc)
	}

	// Verify: exactly one user was created.
	count, err := users.Count(context.Background())
	if err != nil {
		t.Fatalf("users.Count: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 user, got %d", count)
	}

	// Verify: user can authenticate.
	if _, err := users.AuthenticateBasic(context.Background(), "root", "password1"); err != nil {
		t.Fatalf("root must be authenticatable: %v", err)
	}

	// Verify: centrals row exists.
	centralCount, err := centrals.Count(context.Background())
	if err != nil {
		t.Fatalf("centrals.Count: %v", err)
	}
	if centralCount != 1 {
		t.Fatalf("expected 1 central, got %d", centralCount)
	}
	row, err := centrals.Get(context.Background(), "MyCCU")
	if err != nil {
		t.Fatalf("centrals.Get: %v", err)
	}
	if row.Host != "192.168.0.10" {
		t.Fatalf("expected host=192.168.0.10, got %q", row.Host)
	}
	if len(row.Interfaces) != 2 {
		t.Fatalf("expected 2 interfaces, got %d", len(row.Interfaces))
	}

	// Verify: locale section was written.
	localeSec, err := sections.Get(context.Background(), "locale")
	if err != nil {
		t.Fatalf("sections.Get locale: %v", err)
	}
	if !strings.Contains(string(localeSec.ValueJSON), `"en"`) {
		t.Fatalf("locale section does not contain expected locale: %s", localeSec.ValueJSON)
	}

	// Verify: wizard session was dropped after finalization.
	if sessions.Lookup(cookieVal) != nil {
		t.Fatal("session must be dropped after finalization")
	}
}

// ---------------------------------------------------------------------------
// Full path with MQTT enabled
// ---------------------------------------------------------------------------

func TestWizardFullPathWithMQTTWritesMQTTSection(t *testing.T) {
	users, centrals, sections := openWizardStores(t)
	sessions := NewSetupSessionStore()
	wd := SetupWizardDeps{
		Users:    users,
		Centrals: centrals,
		Sections: sections,
		Sessions: sessions,
	}
	h := newWizardRouter(t, wd)

	rr := getPage(t, h, "/setup", "")
	cookieVal := wizardCookie(rr)

	postForm(t, h, "/setup/admin",
		"username=admin&password=password1&confirm=password1", cookieVal)
	postForm(t, h, "/setup/locale", "locale=de&theme=dark", cookieVal)
	postForm(t, h, "/setup/ccu", "skip=1", cookieVal)
	rr4 := postForm(t, h, "/setup/mqtt",
		"mqtt_enabled=1&mqtt_broker_url=mqtt%3A%2F%2Flocalhost%3A1883", cookieVal)
	if rr4.Code != http.StatusSeeOther {
		t.Fatalf("step4 POST: %d body=%s", rr4.Code, rr4.Body.String())
	}

	mqttSec, err := sections.Get(context.Background(), "north.mqtt")
	if err != nil {
		t.Fatalf("sections.Get north.mqtt: %v", err)
	}
	if !strings.Contains(string(mqttSec.ValueJSON), "mqtt://localhost:1883") {
		t.Fatalf("north.mqtt section missing broker URL: %s", mqttSec.ValueJSON)
	}
}

// ---------------------------------------------------------------------------
// login.html renders setup_done banner when ?setup_done=1
// ---------------------------------------------------------------------------

func TestLoginPageSetupDoneBanner(t *testing.T) {
	cats, _ := i18n.NewCatalogs()
	h := NewRouter(Deps{Lang: "en", Catalogs: cats})
	req := httptest.NewRequest(http.MethodGet, "/login?setup_done=1", http.NoBody)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Setup complete") {
		t.Fatalf("expected setup done banner in login page, body:\n%s", rr.Body.String())
	}
}

// ---------------------------------------------------------------------------
// SetupSessionStore unit tests
// ---------------------------------------------------------------------------

func TestSetupSessionStoreIssueAndLookup(t *testing.T) {
	s := NewSetupSessionStore()
	sess := s.Issue()
	if sess == nil || sess.ID == "" {
		t.Fatal("Issue must return a non-nil session with a non-empty ID")
	}
	got := s.Lookup(sess.ID)
	if got == nil {
		t.Fatal("Lookup must return the session after Issue")
	}
	if got.ID != sess.ID {
		t.Fatalf("ID mismatch: %q vs %q", got.ID, sess.ID)
	}
}

func TestSetupSessionStoreSave(t *testing.T) {
	s := NewSetupSessionStore()
	sess := s.Issue()
	state := SetupState{Step: 3, AdminUsername: "bob"}
	s.Save(sess.ID, state)
	got := s.Lookup(sess.ID)
	if got == nil {
		t.Fatal("session must exist after Save")
	}
	if got.State.Step != 3 || got.State.AdminUsername != "bob" {
		t.Fatalf("state not persisted: %+v", got.State)
	}
}

func TestSetupSessionStoreDrop(t *testing.T) {
	s := NewSetupSessionStore()
	sess := s.Issue()
	s.Drop(sess.ID)
	if s.Lookup(sess.ID) != nil {
		t.Fatal("session must be nil after Drop")
	}
}

func TestSetupSessionStoreLookupMissing(t *testing.T) {
	s := NewSetupSessionStore()
	if s.Lookup("no-such-token") != nil {
		t.Fatal("Lookup must return nil for unknown token")
	}
}

// ---------------------------------------------------------------------------
// Fix: wizard step progress placeholder substitution (0.14.1)
// ---------------------------------------------------------------------------

// TestWizardStep1RendersSubstitutedProgress verifies that GET /setup renders
// step 1 with the progress text "Step 1 of 4" and does NOT emit the raw
// placeholders "{current}" or "{total}". This exercises the variadic t func
// with (name, value) pairs wired through the template.
func TestWizardStep1RendersSubstitutedProgress(t *testing.T) {
	users := openWizardDB(t)
	sessions := NewSetupSessionStore()
	wd := SetupWizardDeps{Users: users, Sessions: sessions}
	h := newWizardRouter(t, wd)

	rr := getPage(t, h, "/setup", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /setup: status=%d body=%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Step 1 of 4") {
		t.Errorf("expected substituted progress text %q in body; got:\n%s", "Step 1 of 4", body)
	}
	if strings.Contains(body, "{current}") {
		t.Errorf("raw placeholder {current} must not appear in rendered body")
	}
	if strings.Contains(body, "{total}") {
		t.Errorf("raw placeholder {total} must not appear in rendered body")
	}
}

// TestTemplateFuncTSubstitutesPlaceholders is a focused unit test of the
// t FuncMap entry built by mustParseTemplates. It renders the setup_admin.html
// template with known Step/Total data and asserts the expected substitution.
// It also confirms that a key with no args (setup.step1.title) still returns
// the plain translated string without noise.
func TestTemplateFuncTSubstitutesPlaceholders(t *testing.T) {
	cats, err := i18n.NewCatalogs()
	if err != nil {
		t.Fatalf("i18n.NewCatalogs: %v", err)
	}
	tpl := mustParseTemplates(cats, "en")

	var buf strings.Builder
	data := pageData{
		Title: "Setup",
		Lang:  "en",
		Data: setupWizardPageData{
			Step:  2,
			Total: 4,
		},
	}
	if err := tpl.pages["setup_admin.html"].ExecuteTemplate(&buf, "layout", data); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	out := buf.String()

	// Placeholder substitution: step 2.
	if !strings.Contains(out, "Step 2 of 4") {
		t.Errorf("expected %q in rendered output; got:\n%s", "Step 2 of 4", out)
	}
	if strings.Contains(out, "{current}") {
		t.Errorf("raw placeholder {current} must not appear after substitution")
	}
	if strings.Contains(out, "{total}") {
		t.Errorf("raw placeholder {total} must not appear after substitution")
	}

	// No-args call: step title must appear as plain text.
	if !strings.Contains(out, "Create administrator account") {
		t.Errorf("expected step1.title translation in output")
	}
}
