// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package remoteproxy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/pairingcode"
)

// boxTFake is an openccu-lite box in front of an OpenCCU-Loom add-on, as
// far as the remote proxy meets it: the pairing API at the box root and
// the lighttpd gate over /addons/loom/, which admits a Bearer olt_ token
// carrying the add-on's scope, answers 401 for an unknown token, 403 for
// one that does not open the add-on, and sends a credential-less browser
// navigation to /login?return=.
type boxTFake struct {
	srv *httptest.Server
	fp  []byte

	mu        sync.Mutex
	asks      []boxPairAsk
	commit    []byte
	nonce     []byte
	code      string // the code the box derives once the nonce is revealed
	approved  bool
	rejected  bool
	refuse    string // a pairing-off / not-local refusal of the ask
	tokens    map[string]bool
	forbidden bool     // tokens are valid but do not open the add-on
	upstream  []string // Authorization headers the add-on saw
	paths     []string // paths the add-on saw (after the gate's prefix)
	approveCh chan struct{}
}

const boxTToken = "olt_paired-secret"

func newBoxTFake(t *testing.T) *boxTFake {
	t.Helper()
	f := &boxTFake{tokens: map[string]bool{}, approveCh: make(chan struct{})}
	f.srv = httptest.NewTLSServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	sum := sha256.Sum256(f.srv.Certificate().Raw)
	f.fp = sum[:]
	return f
}

func (f *boxTFake) instanceURL() string { return f.srv.URL + "/addons/loom" }

func (f *boxTFake) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (f *boxTFake) serve(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && r.URL.Path == boxPairingPath:
		f.serveAsk(w, r)
	case strings.HasPrefix(r.URL.Path, boxPairingPath+"/"):
		f.servePoll(w, r)
	case r.URL.Path == "/addons/loom" || strings.HasPrefix(r.URL.Path, "/addons/loom/"):
		f.serveGate(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (f *boxTFake) serveAsk(w http.ResponseWriter, r *http.Request) {
	var ask boxPairAsk
	_ = json.NewDecoder(r.Body).Decode(&ask)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asks = append(f.asks, ask)
	if f.refuse != "" {
		f.writeJSON(w, http.StatusForbidden, map[string]string{"error": f.refuse, "message": "refused"})
		return
	}
	f.commit, _ = hex.DecodeString(ask.Commit)
	f.nonce = []byte("box-nonce-0123456789")
	f.writeJSON(w, http.StatusAccepted, boxPairAnswer{
		ID: "req1", Poll: "poll-secret", Nonce: hex.EncodeToString(f.nonce),
		ExpiresIn: 300, Interval: 1, Fingerprint: hex.EncodeToString(f.fp),
	})
}

func (f *boxTFake) servePoll(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Pairing poll-secret" {
		f.writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}
	if r.Method == http.MethodDelete {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	cn, _ := hex.DecodeString(r.URL.Query().Get("client_nonce"))
	sum := sha256.Sum256(cn)
	f.mu.Lock()
	if !bytes.Equal(sum[:], f.commit) {
		f.mu.Unlock()
		f.writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "invalid"})
		return
	}
	f.code = pairingcode.Code(f.nonce, cn, f.fp)
	ch := f.approveCh
	f.mu.Unlock()
	select {
	case <-ch:
	case <-time.After(200 * time.Millisecond):
	case <-r.Context().Done():
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case f.approved:
		f.tokens[boxTToken] = true
		f.writeJSON(w, http.StatusOK, boxPairResult{
			State: boxPairApproved, Token: boxTToken, Name: "remote",
			Scopes: []string{"addon:openccu-loom"}, Addons: []string{"openccu-loom"},
		})
	case f.rejected:
		f.writeJSON(w, http.StatusOK, boxPairResult{State: boxPairRejected})
	default:
		f.writeJSON(w, http.StatusOK, boxPairResult{State: boxPairPending})
	}
}

func (f *boxTFake) serveGate(w http.ResponseWriter, r *http.Request) {
	authz := r.Header.Get("Authorization")
	f.mu.Lock()
	known := f.tokens[strings.TrimPrefix(authz, "Bearer ")]
	forbidden := f.forbidden
	f.mu.Unlock()
	switch {
	case strings.HasPrefix(authz, "Bearer olt_") && !known:
		f.writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
		return
	case strings.HasPrefix(authz, "Bearer olt_") && forbidden:
		f.writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	case !strings.HasPrefix(authz, "Bearer olt_"):
		if strings.Contains(r.Header.Get("Accept"), "text/html") {
			w.Header().Set("Location", "/login?return="+r.URL.Path)
			w.WriteHeader(http.StatusFound)
			return
		}
		f.writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
		return
	}
	f.mu.Lock()
	f.upstream = append(f.upstream, authz)
	f.paths = append(f.paths, strings.TrimPrefix(r.URL.Path, "/addons/loom"))
	f.mu.Unlock()
	if strings.HasSuffix(r.URL.Path, "/api/v1/health") {
		f.writeJSON(w, http.StatusOK, map[string]string{"status": "healthy"})
		return
	}
	_, _ = io.WriteString(w, "loom spa")
}

func (f *boxTFake) approve() {
	f.mu.Lock()
	f.approved = true
	close(f.approveCh)
	f.mu.Unlock()
}

func (f *boxTFake) boxCode() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.code
}

// boxTServer builds the proxy through New, the constructor the binary uses.
func boxTServer(t *testing.T, opts Options) *Server {
	t.Helper()
	if err := opts.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	srv, err := New(opts, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv.done = ctx.Done()
	return srv
}

// boxTPairCall issues a same-origin pairing call as the panel script does.
func boxTPairCall(t *testing.T, h http.Handler, method, path string, mutate func(*http.Request)) (*httptest.ResponseRecorder, pairView) {
	t.Helper()
	var body io.Reader = http.NoBody
	if method != http.MethodGet {
		body = strings.NewReader("{}")
	}
	req := httptest.NewRequest(method, path, body)
	req.Host = "addon.internal:8234"
	req.Header.Set("X-Forwarded-Host", "ha.example:8123")
	req.Header.Set("Accept", "application/json")
	if method != http.MethodGet {
		req.Header.Set("Origin", "https://ha.example:8123")
		req.Header.Set("Content-Type", "application/json")
	}
	if mutate != nil {
		mutate(req)
	}
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)
	var v pairView
	if strings.HasPrefix(rw.Header().Get("Content-Type"), "application/json") {
		if err := json.Unmarshal(rw.Body.Bytes(), &v); err != nil {
			t.Fatalf("%s %s: decode %q: %v", method, path, rw.Body.String(), err)
		}
	}
	return rw, v
}

// boxTPairUntil polls the pairing state until want or the deadline.
func boxTPairUntil(t *testing.T, h http.Handler, path, want string) pairView {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, v := boxTPairCall(t, h, http.MethodGet, path, nil)
		if v.State == want {
			return v
		}
		if time.Now().After(deadline) {
			t.Fatalf("pairing state = %+v, want %q", v, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func boxTSeedState(t *testing.T, path, name string, cred pairedCredential) {
	t.Helper()
	raw, err := json.Marshal(pairStateFile{Version: pairStateVersion, Instances: map[string]pairedCredential{name: cred}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func boxTStateHas(t *testing.T, path, name string) (pairedCredential, bool) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	var f pairStateFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("decode state: %v", err)
	}
	c, ok := f.Instances[name]
	return c, ok
}

func TestBoxPairingStoresTokenAndProxiedRequestsCarryIt(t *testing.T) {
	box := newBoxTFake(t)
	state := filepath.Join(t.TempDir(), "paired.json")
	// No tls_insecure: before the pairing the box's certificate is not
	// trusted; after it the pinned certificate is.
	srv := boxTServer(t, Options{Instances: []Instance{{Name: "box", URL: box.instanceURL()}}, StateFile: state})
	h := srv.Handler()

	rw, v := boxTPairCall(t, h, http.MethodPost, "/-/pair/box", nil)
	if rw.Code != http.StatusOK || v.State != boxPairPending || len(v.Code) != 6 {
		t.Fatalf("POST = %d %+v, want 200 pending with a code", rw.Code, v)
	}
	if v.BoxURL != box.srv.URL+"/" {
		t.Errorf("box_url = %q, want %q", v.BoxURL, box.srv.URL+"/")
	}
	box.mu.Lock()
	ask := box.asks[0]
	box.mu.Unlock()
	if ask.App != boxPairingApp || len(ask.Addons) != 1 || ask.Addons[0] != "openccu-loom" || ask.Instance != "box" {
		t.Errorf("ask = %+v, want app %q asking for the openccu-loom add-on", ask, boxPairingApp)
	}
	deadline := time.Now().Add(5 * time.Second)
	for box.boxCode() == "" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := box.boxCode(); got != v.Code {
		t.Fatalf("the panel shows code %q, the box derived %q", v.Code, got)
	}

	box.approve()
	boxTPairUntil(t, h, "/-/pair/box", pairStatePaired)

	rw, _ = boxTPairCall(t, h, http.MethodGet, "/-/pair/box", nil)
	if strings.Contains(rw.Body.String(), boxTToken) {
		t.Fatalf("pairing state leaks the token to the browser: %s", rw.Body.String())
	}
	cred, ok := boxTStateHas(t, state, "box")
	if !ok || cred.Token != boxTToken || cred.Fingerprint != hex.EncodeToString(box.fp) {
		t.Fatalf("state file entry = %+v (%v), want the token and the box fingerprint", cred, ok)
	}
	if fi, err := os.Stat(state); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("state file mode = %v (%v), want 0600", fi.Mode().Perm(), err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", http.NoBody)
	prw := httptest.NewRecorder()
	h.ServeHTTP(prw, req)
	if prw.Code != http.StatusOK {
		t.Fatalf("proxied GET = %d %q, want 200 through the gate", prw.Code, prw.Body.String())
	}
	box.mu.Lock()
	defer box.mu.Unlock()
	if len(box.upstream) == 0 || box.upstream[len(box.upstream)-1] != "Bearer "+boxTToken {
		t.Fatalf("add-on saw Authorization %v, want the paired Bearer token", box.upstream)
	}
	if got := box.paths[len(box.paths)-1]; got != "/api/v1/health" {
		t.Errorf("add-on saw path %q, want /api/v1/health", got)
	}
}

func TestBoxPairingPairedTokenSurvivesRestart(t *testing.T) {
	box := newBoxTFake(t)
	box.tokens[boxTToken] = true
	state := filepath.Join(t.TempDir(), "paired.json")
	boxTSeedState(t, state, "box", pairedCredential{
		URL: normaliseInstanceURL(box.instanceURL()), Token: boxTToken, Fingerprint: hex.EncodeToString(box.fp),
	})
	srv := boxTServer(t, Options{Instances: []Instance{{Name: "box", URL: box.instanceURL()}}, StateFile: state})
	st := srv.poller.probe(context.Background(), srv.instances[0])
	if st.Status != "healthy" {
		t.Fatalf("probe status = %q, want healthy through the pinned, paired transport", st.Status)
	}
}

func TestPollerDropsPairedTokenOnUnauthorized(t *testing.T) {
	box := newBoxTFake(t) // the token is not known to the box: revoked
	state := filepath.Join(t.TempDir(), "paired.json")
	boxTSeedState(t, state, "box", pairedCredential{
		URL: normaliseInstanceURL(box.instanceURL()), Token: boxTToken, Fingerprint: hex.EncodeToString(box.fp),
	})
	srv := boxTServer(t, Options{Instances: []Instance{{Name: "box", URL: box.instanceURL()}}, StateFile: state})
	ip := srv.instances[0]
	if ip.authSource() != authSourcePaired {
		t.Fatalf("auth source = %q before the probe, want paired", ip.authSource())
	}
	st := srv.poller.probe(context.Background(), ip)
	if st.Status != statusUnauthorized {
		t.Errorf("probe status = %q, want %q", st.Status, statusUnauthorized)
	}
	if ip.authSource() != authSourceNone {
		t.Errorf("auth source = %q after a 401, want the paired token dropped", ip.authSource())
	}
	if _, ok := boxTStateHas(t, state, "box"); ok {
		t.Error("the state file still holds the token the box refused with 401")
	}
}

func TestPollerKeepsPairedTokenOnForbidden(t *testing.T) {
	box := newBoxTFake(t)
	box.tokens[boxTToken] = true
	box.forbidden = true
	state := filepath.Join(t.TempDir(), "paired.json")
	boxTSeedState(t, state, "box", pairedCredential{
		URL: normaliseInstanceURL(box.instanceURL()), Token: boxTToken, Fingerprint: hex.EncodeToString(box.fp),
	})
	srv := boxTServer(t, Options{Instances: []Instance{{Name: "box", URL: box.instanceURL()}}, StateFile: state})
	ip := srv.instances[0]
	st := srv.poller.probe(context.Background(), ip)
	if st.Status != statusUnauthorized {
		t.Errorf("probe status = %q, want %q", st.Status, statusUnauthorized)
	}
	if ip.authSource() != authSourcePaired {
		t.Errorf("auth source = %q after a 403, want the paired token kept", ip.authSource())
	}
	if _, ok := boxTStateHas(t, state, "box"); !ok {
		t.Error("a 403 dropped the paired token from the state file")
	}
}

func TestBoxLoginRedirectBecomesExplanationPage(t *testing.T) {
	box := newBoxTFake(t)
	srv := boxTServer(t, Options{Instances: []Instance{
		{Name: "box", URL: box.instanceURL(), TLSInsecure: true},
		{Name: "other", URL: "http://127.0.0.1:1"},
	}})
	h := srv.Handler()

	req := httptest.NewRequest(http.MethodGet, "/i/box/", http.NoBody)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set(ingressPathHeader, "/api/hassio_ingress/tok")
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)
	if rw.Code != http.StatusUnauthorized {
		t.Fatalf("navigation = %d (Location %q), want 401 with the explanation", rw.Code, rw.Header().Get("Location"))
	}
	if loc := rw.Header().Get("Location"); loc != "" {
		t.Errorf("explanation still redirects to %q", loc)
	}
	body := rw.Body.String()
	if !strings.Contains(body, `href="/api/hassio_ingress/tok/-/pair/box"`) {
		t.Errorf("explanation lacks the pairing link:\n%s", body)
	}

	// A script call is no navigation: the gate's JSON 401 passes through.
	req = httptest.NewRequest(http.MethodGet, "/i/box/api/v1/info", http.NoBody)
	req.Header.Set("Accept", "application/json")
	rw = httptest.NewRecorder()
	h.ServeHTTP(rw, req)
	if rw.Code != http.StatusUnauthorized || !strings.Contains(rw.Body.String(), "unauthenticated") {
		t.Errorf("API call = %d %q, want the gate's JSON 401 untouched", rw.Code, rw.Body.String())
	}
}

func TestBoxForbiddenNavigationExplainsRepair(t *testing.T) {
	box := newBoxTFake(t)
	box.tokens[boxTToken] = true
	box.forbidden = true
	state := filepath.Join(t.TempDir(), "paired.json")
	boxTSeedState(t, state, "box", pairedCredential{
		URL: normaliseInstanceURL(box.instanceURL()), Token: boxTToken, Fingerprint: hex.EncodeToString(box.fp),
	})
	srv := boxTServer(t, Options{Instances: []Instance{{Name: "box", URL: box.instanceURL()}}, StateFile: state})
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.Header.Set("Accept", "text/html")
	rw := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rw, req)
	if rw.Code != http.StatusForbidden || !strings.Contains(rw.Body.String(), localeEN.WallPaired) {
		t.Fatalf("navigation = %d, want 403 with the re-pair explanation:\n%s", rw.Code, rw.Body.String())
	}
}

func TestBoxPairingMutationsRefuseCrossSiteCalls(t *testing.T) {
	box := newBoxTFake(t)
	srv := boxTServer(t, Options{Instances: []Instance{{Name: "box", URL: box.instanceURL()}}})
	h := srv.Handler()
	cases := []struct {
		name   string
		mutate func(*http.Request)
		want   int
	}{
		{"foreign origin", func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") }, http.StatusForbidden},
		{"no origin", func(r *http.Request) { r.Header.Del("Origin") }, http.StatusForbidden},
		{"form body", func(r *http.Request) { r.Header.Set("Content-Type", "application/x-www-form-urlencoded") }, http.StatusUnsupportedMediaType},
		{"origin matches Host but not X-Forwarded-Host", func(r *http.Request) {
			r.Header.Set("Origin", "http://addon.internal:8234")
		}, http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rw, _ := boxTPairCall(t, h, http.MethodPost, "/-/pair/box", tc.mutate)
			if rw.Code != tc.want {
				t.Errorf("POST = %d, want %d", rw.Code, tc.want)
			}
		})
	}
	rw, _ := boxTPairCall(t, h, http.MethodDelete, "/-/pair/box", func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") })
	if rw.Code != http.StatusForbidden {
		t.Errorf("cross-site DELETE = %d, want 403", rw.Code)
	}
	box.mu.Lock()
	defer box.mu.Unlock()
	if len(box.asks) != 0 {
		t.Errorf("a refused call still reached the box: %d asks", len(box.asks))
	}
}

func TestOptionsTokenWinsOverPairedToken(t *testing.T) {
	box := newBoxTFake(t)
	box.tokens["olt_manual-full"] = true
	state := filepath.Join(t.TempDir(), "paired.json")
	boxTSeedState(t, state, "box", pairedCredential{
		URL: normaliseInstanceURL(box.instanceURL()), Token: boxTToken, Fingerprint: hex.EncodeToString(box.fp),
	})
	srv := boxTServer(t, Options{Instances: []Instance{
		{Name: "box", URL: box.instanceURL(), Token: "olt_manual-full", TLSInsecure: true},
	}, StateFile: state})
	h := srv.Handler()
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, httptest.NewRequest(http.MethodGet, "/api/v1/health", http.NoBody))
	box.mu.Lock()
	seen := append([]string(nil), box.upstream...)
	box.mu.Unlock()
	if len(seen) != 1 || seen[0] != "Bearer olt_manual-full" {
		t.Fatalf("add-on saw %v, want the options token", seen)
	}
	prw, v := boxTPairCall(t, h, http.MethodPost, "/-/pair/box", nil)
	if prw.Code != http.StatusConflict || v.State != pairStateOptions {
		t.Errorf("POST with an options token = %d %+v, want 409 options", prw.Code, v)
	}
}

func TestPairedTokenDroppedWhenInstanceURLChanges(t *testing.T) {
	box := newBoxTFake(t)
	state := filepath.Join(t.TempDir(), "paired.json")
	boxTSeedState(t, state, "box", pairedCredential{
		URL: normaliseInstanceURL(box.srv.URL + "/addons/other"), Token: boxTToken,
	})
	srv := boxTServer(t, Options{Instances: []Instance{{Name: "box", URL: box.instanceURL()}}, StateFile: state})
	if got := srv.instances[0].authSource(); got != authSourceNone {
		t.Errorf("auth source = %q, want the token of the old URL unused", got)
	}
	if _, ok := boxTStateHas(t, state, "box"); ok {
		t.Error("the token of the old URL is still in the state file")
	}
}

func TestBoxPairingNotLocalExplainsManualToken(t *testing.T) {
	box := newBoxTFake(t)
	box.refuse = "not-local"
	srv := boxTServer(t, Options{Instances: []Instance{{Name: "box", URL: box.instanceURL(), TLSInsecure: true}}})
	h := srv.Handler()
	_, v := boxTPairCall(t, h, http.MethodPost, "/-/pair/box", nil)
	if v.State != pairStateFailed || v.Error != "not-local" {
		t.Fatalf("POST = %+v, want failed not-local", v)
	}
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.Header.Set("Accept", "text/html")
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)
	body := rw.Body.String()
	if !strings.Contains(body, "local networks") || strings.Contains(body, "/-/pair/box") {
		t.Errorf("explanation after not-local should point at a manual token without a pair link:\n%s", body)
	}
}

func TestBoxPairingWithdrawForgetsPairing(t *testing.T) {
	box := newBoxTFake(t)
	srv := boxTServer(t, Options{Instances: []Instance{{Name: "box", URL: box.instanceURL()}}})
	h := srv.Handler()
	if _, v := boxTPairCall(t, h, http.MethodPost, "/-/pair/box", nil); v.State != boxPairPending {
		t.Fatalf("POST = %+v, want pending", v)
	}
	_, v := boxTPairCall(t, h, http.MethodDelete, "/-/pair/box", nil)
	if v.State != pairStateIdle || v.Code != "" {
		t.Fatalf("DELETE = %+v, want idle without a code", v)
	}
}

func TestPairRouteIsReservedInBothModes(t *testing.T) {
	box := newBoxTFake(t)
	single := boxTServer(t, Options{Instances: []Instance{{Name: "box", URL: box.instanceURL(), TLSInsecure: true}}}).Handler()
	multi := boxTServer(t, Options{Instances: []Instance{
		{Name: "box", URL: box.instanceURL(), TLSInsecure: true}, {Name: "b", URL: "http://127.0.0.1:1"},
	}}).Handler()
	for name, h := range map[string]http.Handler{"single": single, "multi": multi} {
		req := httptest.NewRequest(http.MethodGet, "/-/pair/box", http.NoBody)
		req.Header.Set("Accept", "text/html")
		rw := httptest.NewRecorder()
		h.ServeHTTP(rw, req)
		if rw.Code != http.StatusOK || !strings.Contains(rw.Body.String(), localeEN.PairTitle) {
			t.Errorf("%s: GET /-/pair/box = %d, want the pairing page", name, rw.Code)
		}
		rw, _ = boxTPairCall(t, h, http.MethodGet, "/-/pair/nope", nil)
		if rw.Code != http.StatusNotFound {
			t.Errorf("%s: unknown instance = %d, want 404", name, rw.Code)
		}
	}
	box.mu.Lock()
	defer box.mu.Unlock()
	if len(box.paths) != 0 {
		t.Errorf("a pairing route was proxied upstream: %v", box.paths)
	}
}

func TestOverviewOffersPairingOnlyWithoutOptionsToken(t *testing.T) {
	srv := boxTServer(t, Options{Instances: []Instance{
		{Name: "lite", URL: "https://127.0.0.1:1/addons/loom"},
		{Name: "manual", URL: "http://127.0.0.1:2", Token: "t"},
	}})
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.Header.Set("Accept-Language", "de")
	rw := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rw, req)
	body := rw.Body.String()
	if !strings.Contains(body, `<div class="pair" data-endpoint="-/pair/lite">`) {
		t.Errorf("overview lacks the visible pairing box of lite:\n%s", body)
	}
	if !strings.Contains(body, `<div class="pair" data-endpoint="-/pair/manual" hidden>`) {
		t.Errorf("overview offers pairing for an instance with an options token:\n%s", body)
	}
	if !strings.Contains(body, localeDE.PairButton) {
		t.Errorf("overview lacks the German pair button %q", localeDE.PairButton)
	}
}
