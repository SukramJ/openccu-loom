// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package litefake

import (
	"context"
	"fmt"
	"time"
)

// SetReady switches between a box whose occulited answers and one where
// only the web server in front of it does. Going not-ready ends every
// open event stream, lite-rpc and metadata alike, because the process
// behind them stopped answering; the ring and the boot id are kept (use
// [Fake.RestartBoot] for a process restart).
func (f *Fake) SetReady(ready bool) {
	f.mu.Lock()
	was := f.ready
	f.ready = ready
	f.mu.Unlock()
	if was && !ready {
		f.ring.dropStreams()
		f.meta.dropStreams()
	}
}

// SetInterfaceDown marks an interface process as not answering (the
// proxy answers 503 down, /interfaces reports running false, hello
// reports state down) or as back up. A change emits an interface
// message with state down or up.
func (f *Fake) SetInterfaceDown(iface string, down bool) error {
	f.mu.Lock()
	st, ok := f.ifaces[iface]
	if !ok {
		f.mu.Unlock()
		return fmt.Errorf("litefake: unknown interface %s", iface)
	}
	changed := st.down != down
	st.down = down
	f.mu.Unlock()
	if changed {
		state := "up"
		if down {
			state = "down"
		}
		f.publishInterface(iface, state)
	}
	return nil
}

// RestartInterface models a restart of one interface process: the
// subscriber registers again (the daemon lost its callback table, so it
// re-sends its catalogue as newDevices) and an interface message with
// state restarted follows.
func (f *Fake) RestartInterface(ctx context.Context, iface string) error {
	if f.closed() {
		return errFakeClosed
	}
	if err := f.subscribe(ctx, iface); err != nil {
		return err
	}
	f.publishInterface(iface, "restarted")
	return nil
}

// RestartBoot models an occulited restart: a new boot id, an empty
// ring, every stream closed, /state entries turned into unconfirmed
// values restored from disk, the metadata change log (memory only)
// emptied while the store itself survives, and the subscriber registering again with
// every interface process.
func (f *Fake) RestartBoot(ctx context.Context) error {
	if f.closed() {
		return errFakeClosed
	}
	f.ring.restart()
	f.values.markRestored()
	f.meta.restart()
	for _, name := range f.interfaceNames() {
		if err := f.subscribe(ctx, name); err != nil {
			return err
		}
	}
	return nil
}

// DropStreams ends every open event stream without a closing frame.
func (f *Fake) DropStreams() { f.ring.dropStreams() }

// ForceOverflow marks every open stream as having lost messages: each
// sends resync{overflow} at its next heartbeat tick and ends.
func (f *Fake) ForceOverflow() { f.ring.forceOverflow() }

// ForceGap discards the held ring messages, so a resume from any
// position before the newest one answers resync{gap}.
func (f *Fake) ForceGap() { f.ring.forceGap() }

// SetTokens replaces the token table: secret → stored scopes. A stream
// whose token disappears or loses rpc:read ends at its next heartbeat.
func (f *Fake) SetTokens(tokens map[string][]string) { f.setTokens(tokens) }

func (f *Fake) setTokens(tokens map[string][]string) {
	table := make(map[string]tokenEntry, len(tokens))
	for secret, scopes := range tokens {
		table[secret] = tokenEntry{name: tokenName(secret), scopes: append([]string{}, scopes...)}
	}
	f.mu.Lock()
	f.tokens = table
	f.mu.Unlock()
}

// SetHeartbeatInterval changes the SSE heartbeat period for streams
// opened afterwards.
func (f *Fake) SetHeartbeatInterval(d time.Duration) {
	if d <= 0 {
		d = DefaultHeartbeatInterval
	}
	f.mu.Lock()
	f.heartbeat = d
	f.mu.Unlock()
}

// OpenStreams reports how many event streams are attached.
func (f *Fake) OpenStreams() int { return f.ring.openStreams() }

// LastEventID returns "<boot_id>-<seq>" of the newest ring position.
func (f *Fake) LastEventID() string {
	boot, seq := f.ring.position()
	return fmt.Sprintf("%s-%d", boot, seq)
}

// SetAccounts replaces the accounts that can log in. Existing sessions
// stay valid.
func (f *Fake) SetAccounts(accounts []Account) {
	table := make(map[string]Account, len(accounts))
	for _, a := range accounts {
		a.Scopes = append([]string{}, a.Scopes...)
		table[a.Username] = a
	}
	f.mu.Lock()
	f.accounts = table
	f.mu.Unlock()
}

// SetMetaHeartbeatInterval changes the change-stream heartbeat period
// for streams opened afterwards.
func (f *Fake) SetMetaHeartbeatInterval(d time.Duration) {
	if d <= 0 {
		d = DefaultMetaHeartbeatInterval
	}
	f.mu.Lock()
	f.metaHeartbeat = d
	f.mu.Unlock()
}

// Sessions returns the number of open account sessions (logins not yet
// logged out).
func (f *Fake) Sessions() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sessions)
}
