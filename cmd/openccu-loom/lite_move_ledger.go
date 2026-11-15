// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/internal/warnings"
)

// liteMoveLookupTimeout bounds the one central read per pending move that
// [liteMoveLedger.PendingLiteMoves] does on every warning-list computation.
const liteMoveLookupTimeout = 2 * time.Second

// liteMoveLedger holds the centrals the lite-addon onboarder left as CCU
// centrals on an openccu-lite box, each with the reason, and serves them to
// the warning list as [warnings.LiteMoveSource].
//
// A recorded move stays pending only while its central is still stored as a
// CCU central: the read re-checks the stored row, so a central the operator
// switched to openccu-lite or deleted drops out of the list without anyone
// having to tell the ledger.
type liteMoveLedger struct {
	admin handlers.CentralAdminService

	mu        sync.Mutex
	pending   map[string]warnings.LiteMoveReason
	observers map[int]func()
	nextID    int
}

func newLiteMoveLedger(admin handlers.CentralAdminService) *liteMoveLedger {
	return &liteMoveLedger{
		admin:     admin,
		pending:   map[string]warnings.LiteMoveReason{},
		observers: map[int]func(){},
	}
}

// record marks names as waiting to be moved for reason and tells every
// observer. Safe on nil: a test fixture without a ledger records nothing.
func (l *liteMoveLedger) record(names []string, reason warnings.LiteMoveReason) {
	if l == nil || len(names) == 0 {
		return
	}
	l.mu.Lock()
	for _, n := range names {
		l.pending[n] = reason
	}
	notify := make([]func(), 0, len(l.observers))
	for _, f := range l.observers {
		notify = append(notify, f)
	}
	l.mu.Unlock()
	for _, f := range notify {
		f()
	}
}

// OnChange registers f to run after every record and returns what detaches
// it. The box-notice mirror nudges through it.
func (l *liteMoveLedger) OnChange(f func()) (detach func()) {
	l.mu.Lock()
	id := l.nextID
	l.nextID++
	l.observers[id] = f
	l.mu.Unlock()
	return func() {
		l.mu.Lock()
		delete(l.observers, id)
		l.mu.Unlock()
	}
}

// PendingLiteMoves implements [warnings.LiteMoveSource], sorted by central.
// A central that is no longer stored as a CCU central — switched, or
// deleted — is dropped for good. A read that fails for another reason keeps
// the entry: a moment of database trouble must not hide the warning.
func (l *liteMoveLedger) PendingLiteMoves() []warnings.LiteMove {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	snapshot := make([]warnings.LiteMove, 0, len(l.pending))
	for name, reason := range l.pending {
		snapshot = append(snapshot, warnings.LiteMove{Central: name, Reason: reason})
	}
	l.mu.Unlock()

	out := make([]warnings.LiteMove, 0, len(snapshot))
	var settled []string
	for _, m := range snapshot {
		if l.admin != nil {
			ctx, cancel := context.WithTimeout(context.Background(), liteMoveLookupTimeout)
			row, err := l.admin.Get(ctx, m.Central)
			cancel()
			switch {
			case errors.Is(err, sqlitestore.ErrCentralNotFound):
				settled = append(settled, m.Central)
				continue
			case err == nil && !centralIsCCU(row):
				settled = append(settled, m.Central)
				continue
			}
		}
		out = append(out, m)
	}
	if len(settled) > 0 {
		l.mu.Lock()
		for _, n := range settled {
			delete(l.pending, n)
		}
		l.mu.Unlock()
	}
	slices.SortFunc(out, func(a, b warnings.LiteMove) int { return strings.Compare(a.Central, b.Central) })
	return out
}
