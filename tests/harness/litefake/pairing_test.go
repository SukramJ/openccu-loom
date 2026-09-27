// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package litefake_test

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/tests/harness/litefake"
)

// clientNonce is a fixed 16-byte client nonce and its commit.
var clientNonce = []byte("0123456789abcdef")

func commitOf(nonce []byte) string {
	sum := sha256.Sum256(nonce)
	return hex.EncodeToString(sum[:])
}

// codeFromFormula computes the pairing code straight from the formula
// text, independently of the fake: the first four bytes of
// SHA-256(nonce ‖ client_nonce ‖ fingerprint), big-endian, mod 10^6,
// six digits.
func codeFromFormula(nonceHex string, client, fingerprint []byte) string {
	nonce, _ := hex.DecodeString(nonceHex)
	buf := append(append(append([]byte{}, nonce...), client...), fingerprint...)
	sum := sha256.Sum256(buf)
	n := binary.BigEndian.Uint32(sum[:4]) % 1000000
	s := strconv.FormatUint(uint64(n), 10)
	return strings.Repeat("0", 6-len(s)) + s
}

type pairingStarted struct {
	ID          string `json:"id"`
	Poll        string `json:"poll"`
	Nonce       string `json:"nonce"`
	ExpiresIn   int    `json:"expires_in"`
	Interval    int    `json:"interval"`
	Fingerprint string `json:"fingerprint"`
}

type pollState struct {
	State  string   `json:"state"`
	Token  string   `json:"token"`
	Name   string   `json:"name"`
	Scopes []string `json:"scopes"`
	Access struct {
		Devices string `json:"devices"`
	} `json:"access"`
}

func requestPairing(t *testing.T, f *litefake.Fake, app string) pairingStarted {
	t.Helper()
	body := `{"app":"` + app + `","app_version":"1.0","instance":"Wohnzimmer PC",` +
		`"access":{"devices":"operate","names":"read"},"purpose":{"devices":"switch lights"},` +
		`"commit":"` + commitOf(clientNonce) + `"}`
	resp, raw := send(t, f, http.MethodPost, "/api/auth/v1/pairing/request", "", body)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("pairing request: %d %s", resp.StatusCode, raw)
	}
	return decode[pairingStarted](t, string(raw))
}

func poll(t *testing.T, f *litefake.Fake, p pairingStarted, query string) (resp reply, raw []byte) {
	t.Helper()
	return send(t, f, http.MethodGet, "/api/auth/v1/pairing/request/"+p.ID+"?"+query, "", "",
		"Authorization", "Pairing "+p.Poll)
}

func fastPairing() litefake.Options {
	return litefake.Options{PairingPollInterval: 50 * time.Millisecond}
}

// TestPairingApproveHandsOutTokenOnce pins the happy path: 202 with the
// secrets, the reveal, the code both sides compute, the approved poll
// carrying the token once, the token's scopes, then 404 not_found.
func TestPairingApproveHandsOutTokenOnce(t *testing.T) {
	f := startFake(t, fastPairing())
	p := requestPairing(t, f, "loom")
	if len(p.ID) != 16 || len(p.Poll) != 48 || len(p.Nonce) != 32 || p.ExpiresIn != 300 || p.Fingerprint != "" {
		t.Errorf("answer %+v", p)
	}
	resp, raw := poll(t, f, p, "client_nonce="+hex.EncodeToString(clientNonce))
	if resp.StatusCode != http.StatusOK || decode[pollState](t, string(raw)).State != "pending" ||
		resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("first poll: %d %s", resp.StatusCode, raw)
	}
	want := codeFromFormula(p.Nonce, clientNonce, nil)
	pending := f.Pairings()
	if len(pending) != 1 || pending[0].Code != want || !pending[0].Revealed {
		t.Fatalf("admin view %+v, want code %s", pending, want)
	}
	if got := litefake.PairingCode(mustHex(t, p.Nonce), clientNonce, nil); got != want {
		t.Errorf("PairingCode %s, formula %s", got, want)
	}

	name, err := f.ApprovePairing(p.ID, want)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(60 * time.Millisecond)
	resp, raw = poll(t, f, p, "")
	st := decode[pollState](t, string(raw))
	if resp.StatusCode != http.StatusOK || st.State != "approved" || st.Name != name || !strings.HasPrefix(st.Token, "olt_") {
		t.Fatalf("approved poll: %d %s", resp.StatusCode, raw)
	}
	if strings.Join(st.Scopes, ",") != "rpc:read,rpc:operate,meta:read" || st.Access.Devices != "operate" {
		t.Errorf("scopes %v access %+v", st.Scopes, st.Access)
	}
	if name != "loom-wohnzimmer-pc" {
		t.Errorf("token name %q", name)
	}
	if resp, _ := get(t, f, "/api/rpc/v1/interfaces", st.Token); resp.StatusCode != http.StatusOK {
		t.Errorf("paired token refused: %d", resp.StatusCode)
	}
	if resp, _ := get(t, f, "/api/system/v1/status", st.Token); resp.StatusCode != http.StatusForbidden {
		t.Errorf("paired token beyond its access: %d", resp.StatusCode)
	}
	time.Sleep(60 * time.Millisecond)
	if resp, raw := poll(t, f, p, ""); resp.StatusCode != http.StatusNotFound || errorCode(t, raw) != "not_found" {
		t.Errorf("second approved poll: %d %s", resp.StatusCode, raw)
	}
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestPairingLongPollWakesOnApproval pins wait=: a pending long poll
// returns as soon as the request is approved.
func TestPairingLongPollWakesOnApproval(t *testing.T) {
	f := startFake(t, fastPairing())
	p := requestPairing(t, f, "loom")
	poll(t, f, p, "client_nonce="+hex.EncodeToString(clientNonce))
	go func() {
		time.Sleep(200 * time.Millisecond)
		_, _ = f.ApprovePairing(p.ID, codeFromFormula(p.Nonce, clientNonce, nil))
	}()
	start := time.Now()
	_, raw := poll(t, f, p, "wait=10")
	if st := decode[pollState](t, string(raw)).State; st != "approved" || time.Since(start) > 5*time.Second {
		t.Errorf("long poll: %s after %s", raw, time.Since(start))
	}
}

// TestPairingWrongCodeRejectsAndMutes pins the wrong-code path: the
// request turns rejected and the app is muted (429 limit, Retry-After).
func TestPairingWrongCodeRejectsAndMutes(t *testing.T) {
	f := startFake(t, fastPairing())
	p := requestPairing(t, f, "loom")
	if _, err := f.ApprovePairing(p.ID, "000000"); err == nil || !strings.Contains(err.Error(), "not-ready") {
		t.Errorf("approve before reveal: %v", err)
	}
	poll(t, f, p, "client_nonce="+hex.EncodeToString(clientNonce))
	wrong := "000000"
	if codeFromFormula(p.Nonce, clientNonce, nil) == wrong {
		wrong = "000001"
	}
	if _, err := f.ApprovePairing(p.ID, wrong); err == nil || !strings.Contains(err.Error(), "wrong-code") {
		t.Fatalf("wrong code: %v", err)
	}
	time.Sleep(60 * time.Millisecond)
	if _, raw := poll(t, f, p, ""); decode[pollState](t, string(raw)).State != "rejected" {
		t.Errorf("after wrong code: %s", raw)
	}
	body := `{"app":"loom","access":{"devices":"read"},"commit":"` + commitOf(clientNonce) + `"}`
	resp, raw := send(t, f, http.MethodPost, "/api/auth/v1/pairing/request", "", body)
	if resp.StatusCode != http.StatusTooManyRequests || errorCode(t, raw) != "limit" || resp.Header.Get("Retry-After") != "60" {
		t.Errorf("muted app: %d %s", resp.StatusCode, raw)
	}
}

// TestPairingRejectAndExpiry pins RejectPairing, the expired state and
// the request disappearing after its keep time.
func TestPairingRejectAndExpiry(t *testing.T) {
	opts := fastPairing()
	opts.PairingLifetime = 300 * time.Millisecond
	opts.PairingKeep = 300 * time.Millisecond
	f := startFake(t, opts)

	a := requestPairing(t, f, "app-a")
	poll(t, f, a, "client_nonce="+hex.EncodeToString(clientNonce))
	if err := f.RejectPairing(a.ID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(60 * time.Millisecond)
	if _, raw := poll(t, f, a, ""); decode[pollState](t, string(raw)).State != "rejected" {
		t.Errorf("rejected: %s", raw)
	}

	b := requestPairing(t, f, "app-b")
	poll(t, f, b, "client_nonce="+hex.EncodeToString(clientNonce))
	time.Sleep(350 * time.Millisecond)
	if _, raw := poll(t, f, b, ""); decode[pollState](t, string(raw)).State != "expired" {
		t.Errorf("expired: %s", raw)
	}
	time.Sleep(350 * time.Millisecond)
	if resp, _ := poll(t, f, b, ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("after keep time: %d", resp.StatusCode)
	}
}

// TestPairingRefusals pins the request and poll refusals.
func TestPairingRefusals(t *testing.T) {
	f := startFake(t, litefake.Options{})
	commit := commitOf(clientNonce)
	for body, want := range map[string]int{
		`{"app":"loom","access":{"devices":"read"},"commit":"` + commit + `","extra":1}`: http.StatusUnprocessableEntity,
		`{"app":"loom","access":{},"commit":"` + commit + `"}`:                           http.StatusUnprocessableEntity,
		`{"app":"-bad","access":{"devices":"read"},"commit":"` + commit + `"}`:           http.StatusUnprocessableEntity,
		`{"app":"loom","access":{"devices":"root"},"commit":"` + commit + `"}`:           http.StatusUnprocessableEntity,
		`{"app":"loom","access":{"devices":"read"},"commit":"abc"}`:                      http.StatusUnprocessableEntity,
	} {
		if resp, raw := send(t, f, http.MethodPost, "/api/auth/v1/pairing/request", "", body); resp.StatusCode != want {
			t.Errorf("%s: %d %s", body, resp.StatusCode, raw)
		}
	}

	p := requestPairing(t, f, "loom")
	resp, raw := send(t, f, http.MethodGet, "/api/auth/v1/pairing/request/"+p.ID, "", "", "Authorization", "Pairing wrong")
	if resp.StatusCode != http.StatusForbidden || errorCode(t, raw) != "forbidden" {
		t.Errorf("wrong poll secret: %d %s", resp.StatusCode, raw)
	}
	if resp, raw := poll(t, f, p, "client_nonce="+hex.EncodeToString([]byte("another-nonce-16b"))); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("nonce not matching commit: %d %s", resp.StatusCode, raw)
	}
	if resp, raw := poll(t, f, p, ""); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("first poll without reveal: %d %s", resp.StatusCode, raw)
	}
	poll(t, f, p, "client_nonce="+hex.EncodeToString(clientNonce))
	resp, raw = poll(t, f, p, "")
	if resp.StatusCode != http.StatusTooManyRequests || errorCode(t, raw) != "slow_down" || resp.Header.Get("Retry-After") != "2" {
		t.Errorf("fast poll: %d %s", resp.StatusCode, raw)
	}
	if resp, _ := send(t, f, http.MethodDelete, "/api/auth/v1/pairing/request/"+p.ID, "", "{}", "Authorization", "Pairing "+p.Poll); resp.StatusCode != http.StatusNoContent {
		t.Errorf("delete: %d", resp.StatusCode)
	}

	_, raw = get(t, f, "/api/meta/v1/version", "")
	if !strings.Contains(string(raw), `"pairing":true`) {
		t.Errorf("version does not advertise pairing: %s", raw)
	}
	f.SetPairingEnabled(false)
	body := `{"app":"other","access":{"devices":"read"},"commit":"` + commit + `"}`
	if resp, raw := send(t, f, http.MethodPost, "/api/auth/v1/pairing/request", "", body); resp.StatusCode != http.StatusForbidden || errorCode(t, raw) != "pairing-off" {
		t.Errorf("pairing off: %d %s", resp.StatusCode, raw)
	}
}

// TestPairingScopesNeverExceedCeiling pins the level table: the highest
// level of every area grants none of the scopes pairing may never grant.
func TestPairingScopesNeverExceedCeiling(t *testing.T) {
	scopes, err := litefake.PairingScopes(litefake.PairingAccess{Devices: "administer", Names: "configure", System: "configure"})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(scopes, ",")
	if got != "rpc:read,rpc:operate,rpc:configure,rpc:admin,meta:read,meta:write,system:read,logs:read,system:write" {
		t.Errorf("scopes %s", got)
	}
	for _, never := range []string{"*", "auth:admin", "radio:keys", "power", "backup", "addons:write", "led"} {
		for _, s := range scopes {
			if s == never {
				t.Errorf("pairing grants %s", never)
			}
		}
	}
}
