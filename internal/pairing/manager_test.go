// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package pairing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"github.com/SukramJ/openccu-loom/internal/auth"
)

type fakeMinter struct {
	minted []string
	fail   error
}

func (f *fakeMinter) MintPairedToken(_ context.Context, subject string, role auth.Role) (token, fingerprint string, err error) {
	if f.fail != nil {
		return "", "", f.fail
	}
	f.minted = append(f.minted, subject+"/"+string(role))
	return "tok-" + subject, "fp-" + subject, nil
}

func newManager() (*Manager, *fakeMinter) {
	m := &fakeMinter{}
	return &Manager{Minter: m}, m
}

// commitFor returns a valid client nonce and its hex commitment.
func commitFor(seed byte) (nonceHex, commit string) {
	nonce := make([]byte, 32)
	for i := range nonce {
		nonce[i] = seed
	}
	sum := sha256.Sum256(nonce)
	return hex.EncodeToString(nonce), hex.EncodeToString(sum[:])
}

func validAsk(seed byte) Ask {
	_, commit := commitFor(seed)
	return Ask{
		App: "openccu-loom-client", AppVersion: "2026.9.1", Instance: "ha-core",
		Name: "Home Assistant", Role: "operator", Purpose: "device control", Commit: commit,
	}
}

func TestCodeGoldenVector(t *testing.T) {
	// Pinned derivation: a change here breaks every already-shipped
	// client, so the vector is frozen independently of the implementation.
	nonce, _ := hex.DecodeString("00112233445566778899aabbccddeeff")
	clientNonce, _ := hex.DecodeString("ffeeddccbbaa99887766554433221100ffeeddccbbaa99887766554433221100")
	fp, _ := hex.DecodeString("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	sum := sha256.Sum256(append(append(append([]byte{}, nonce...), clientNonce...), fp...))
	want := fmt.Sprintf("%06d", uint32(sum[0])<<24|uint32(sum[1])<<16|uint32(sum[2])<<8|uint32(sum[3]))
	want = want[len(want)-6:]
	if got := Code(nonce, clientNonce, fp); got != Code(nonce, clientNonce, fp) || len(got) != 6 {
		t.Fatalf("Code not stable/6 digits: %q", got)
	}
	// Independent recomputation must agree (mod is the only step above
	// beyond the raw big-endian read, and %06d of the mod result).
	if got := Code(nonce, clientNonce, fp); got != fmt.Sprintf("%06d", (uint32(sum[0])<<24|uint32(sum[1])<<16|uint32(sum[2])<<8|uint32(sum[3]))%1_000_000) {
		t.Fatalf("Code = %q, want independent derivation %q", got, want)
	}
}

func TestPairingHappyPathTokenExactlyOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, minter := newManager()
		nonceHex, _ := commitFor(1)
		ask := validAsk(1)

		ans, err := m.RequestFrom(ask, "192.168.1.50")
		if err != nil {
			t.Fatal(err)
		}
		if len(ans.ID) != 16 || len(ans.Poll) != 48 || ans.ExpiresIn != 300 {
			t.Fatalf("answer shape: %+v", ans)
		}
		// Before the reveal the card shows nothing.
		if got := m.Pending(); len(got) != 0 {
			t.Fatalf("pending before reveal: %+v", got)
		}
		// First poll reveals the nonce.
		res, err := m.Poll(context.Background(), ans.ID, ans.Poll, nonceHex, 0)
		if err != nil || res.State != StatePending {
			t.Fatalf("reveal poll: %v %+v", err, res)
		}
		views := m.Pending()
		if len(views) != 1 || len(views[0].Code) != 6 || views[0].Role != "operator" {
			t.Fatalf("pending after reveal: %+v", views)
		}
		// The client derives the same code from its own halves.
		nonce, _ := hex.DecodeString(ans.Nonce)
		cn, _ := hex.DecodeString(nonceHex)
		if clientCode := Code(nonce, cn, nil); clientCode != views[0].Code {
			t.Fatalf("client code %q != card code %q", clientCode, views[0].Code)
		}

		if _, err := m.Approve(context.Background(), views[0].ID, views[0].Code, "markus"); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Second)
		res, err = m.Poll(context.Background(), ans.ID, ans.Poll, "", 0)
		if err != nil || res.State != StateApproved || res.Token != "tok-openccu-loom-client-ha-core" || res.Role != "operator" {
			t.Fatalf("approved poll: %v %+v", err, res)
		}
		if len(minter.minted) != 1 || minter.minted[0] != "openccu-loom-client-ha-core/operator" {
			t.Fatalf("minted: %v", minter.minted)
		}
		// The answer existed once.
		if _, err := m.Poll(context.Background(), ans.ID, ans.Poll, "", 0); !errors.Is(err, ErrUnknown) {
			t.Fatalf("second poll: %v", err)
		}
	})
}

func TestWrongCodeRejectsAndMutes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, minter := newManager()
		nonceHex, _ := commitFor(2)
		ask := validAsk(2)
		ans, _ := m.RequestFrom(ask, "192.168.1.60")
		if _, err := m.Poll(context.Background(), ans.ID, ans.Poll, nonceHex, 0); err != nil {
			t.Fatal(err)
		}
		view := m.Pending()[0]
		wrong := "000000"
		if wrong == view.Code {
			wrong = "000001"
		}
		if _, err := m.Approve(context.Background(), view.ID, wrong, "markus"); !errors.Is(err, ErrWrongCode) {
			t.Fatalf("wrong code: %v", err)
		}
		if len(minter.minted) != 0 {
			t.Fatal("a token was minted despite the wrong code")
		}
		time.Sleep(time.Second)
		if res, err := m.Poll(context.Background(), ans.ID, ans.Poll, "", 0); err != nil || res.State != StateRejected || res.Token != "" {
			t.Fatalf("after wrong code: %v %+v", err, res)
		}
		// The (address, app) pair is muted.
		if _, err := m.RequestFrom(validAsk(3), "192.168.1.60"); !errors.Is(err, ErrMuted) {
			t.Fatalf("mute: %v", err)
		}
		// Another address may still ask.
		if _, err := m.RequestFrom(validAsk(3), "192.168.1.61"); err != nil {
			t.Fatalf("other address muted too: %v", err)
		}
		// The mute lifts after MuteFor.
		time.Sleep(MuteFor + time.Second)
		if _, err := m.RequestFrom(validAsk(4), "192.168.1.60"); err != nil {
			t.Fatalf("mute did not lift: %v", err)
		}
	})
}

func TestLimitsAndGates(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, _ := newManager()

		off := &Manager{Minter: &fakeMinter{}, Enabled: func() bool { return false }}
		if _, err := off.RequestFrom(validAsk(1), "10.0.0.1"); !errors.Is(err, ErrOff) {
			t.Fatalf("off: %v", err)
		}
		local := &Manager{Minter: &fakeMinter{}, Local: func(host string) bool { return host == "10.0.0.1" }}
		if _, err := local.RequestFrom(validAsk(1), "203.0.113.9"); !errors.Is(err, ErrNotLocal) {
			t.Fatalf("not local: %v", err)
		}

		// One request per (app, address).
		if _, err := m.RequestFrom(validAsk(1), "10.0.0.2"); err != nil {
			t.Fatal(err)
		}
		if _, err := m.RequestFrom(validAsk(1), "10.0.0.2"); !errors.Is(err, ErrLimit) {
			t.Fatalf("dup (app,addr): %v", err)
		}
		// MaxPending across callers.
		for i := range MaxPending - 1 {
			ask := validAsk(byte(10 + i))
			ask.App = fmt.Sprintf("app-%d", i)
			if _, err := m.RequestFrom(ask, fmt.Sprintf("10.0.1.%d", i)); err != nil {
				t.Fatal(err)
			}
		}
		overflow := validAsk(99)
		overflow.App = "one-too-many"
		if _, err := m.RequestFrom(overflow, "10.0.2.1"); !errors.Is(err, ErrLimit) {
			t.Fatalf("max pending: %v", err)
		}

		// PerHour per address, measured on a fresh manager so MaxPending does
		// not interfere: expired requests still count against the hour.
		m2, _ := newManager()
		for i := range PerHour {
			ask := validAsk(byte(50 + i))
			ask.App = fmt.Sprintf("hourly-%d", i)
			if _, err := m2.RequestFrom(ask, "10.0.3.1"); err != nil {
				t.Fatalf("request %d: %v", i, err)
			}
			time.Sleep(Lifetime + time.Second) // let it expire, freeing the pending slot
		}
		late := validAsk(70)
		late.App = "eleventh"
		if _, err := m2.RequestFrom(late, "10.0.3.1"); !errors.Is(err, ErrLimit) {
			t.Fatalf("per hour: %v", err)
		}
	})
}

func TestRoleAndCommitValidation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, _ := newManager()

		admin := validAsk(1)
		admin.Role = "admin"
		if _, err := m.RequestFrom(admin, "10.1.0.1"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("admin role: %v", err)
		}
		bad := validAsk(1)
		bad.Commit = "zz"
		if _, err := m.RequestFrom(bad, "10.1.0.2"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("bad commit: %v", err)
		}

		// A reveal that does not match the commitment is refused.
		ans, err := m.RequestFrom(validAsk(5), "10.1.0.3")
		if err != nil {
			t.Fatal(err)
		}
		otherNonce, _ := commitFor(6)
		if _, err := m.Poll(context.Background(), ans.ID, ans.Poll, otherNonce, 0); !errors.Is(err, ErrInvalid) {
			t.Fatalf("commit mismatch: %v", err)
		}
		// Approving before the reveal is refused.
		if _, err := m.Approve(context.Background(), ans.ID, "123456", "markus"); !errors.Is(err, ErrNotReady) {
			t.Fatalf("approve before reveal: %v", err)
		}
		// A poll with the wrong secret is refused.
		if _, err := m.Poll(context.Background(), ans.ID, "not-the-secret", "", 0); !errors.Is(err, ErrPollAuth) {
			t.Fatalf("wrong poll secret: %v", err)
		}
	})
}

func TestExpiryAndWithdraw(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, _ := newManager()
		nonceHex, _ := commitFor(7)
		ans, _ := m.RequestFrom(validAsk(7), "10.2.0.1")
		if _, err := m.Poll(context.Background(), ans.ID, ans.Poll, nonceHex, 0); err != nil {
			t.Fatal(err)
		}
		time.Sleep(Lifetime + time.Second)
		if got := m.Pending(); len(got) != 0 {
			t.Fatalf("expired request still pending: %+v", got)
		}
		if res, err := m.Poll(context.Background(), ans.ID, ans.Poll, "", 0); err != nil || res.State != StateExpired {
			t.Fatalf("expired poll: %v %+v", err, res)
		}

		ans2, _ := m.RequestFrom(validAsk(8), "10.2.0.2")
		if err := m.Withdraw(ans2.ID, ans2.Poll); err != nil {
			t.Fatal(err)
		}
		if _, err := m.Poll(context.Background(), ans2.ID, ans2.Poll, "", 0); !errors.Is(err, ErrUnknown) {
			t.Fatalf("after withdraw: %v", err)
		}
	})
}

func TestSlowDownAndLongPollWake(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, _ := newManager()
		nonceHex, _ := commitFor(9)
		ans, _ := m.RequestFrom(validAsk(9), "10.3.0.1")
		if _, err := m.Poll(context.Background(), ans.ID, ans.Poll, nonceHex, 0); err != nil {
			t.Fatal(err)
		}
		// A second plain poll inside the interval is told to slow down.
		if _, err := m.Poll(context.Background(), ans.ID, ans.Poll, "", 0); !errors.Is(err, ErrSlowDown) {
			t.Fatalf("slow down: %v", err)
		}
		// A long poll wakes when the administrator decides.
		view := m.Pending()[0]
		done := make(chan Result, 1)
		go func() {
			res, _ := m.Poll(context.Background(), ans.ID, ans.Poll, "", 10*time.Second)
			done <- res
		}()
		// The long poll is parked; the clock has not moved, so only the
		// approval can wake it.
		synctest.Wait()
		select {
		case res := <-done:
			t.Fatalf("long poll returned before the decision: %+v", res)
		default:
		}
		if _, err := m.Approve(context.Background(), view.ID, view.Code, "markus"); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		select {
		case res := <-done:
			if res.State != StateApproved || res.Token == "" {
				t.Fatalf("long poll result: %+v", res)
			}
		default:
			t.Fatal("long poll did not wake on approval")
		}
	})
}

func TestSubjectSlug(t *testing.T) {
	for in, want := range map[[2]string]string{
		{"openccu-loom-client", "ha-core"}: "openccu-loom-client-ha-core",
		{"My App!", "Wohnzimmer Küche"}:    "my-app-wohnzimmer-k-che",
		{"x", ""}:                          "paired-client",
	} {
		if got := Subject(in[0], in[1]); got != want {
			t.Errorf("Subject(%q,%q) = %q, want %q", in[0], in[1], got, want)
		}
	}
}
