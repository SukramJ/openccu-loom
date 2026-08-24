// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"fmt"
	"log/slog"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/wiring"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// wireProgramExecuteAudit records every CCU program run the daemon triggers —
// as an audit entry AND as an INFO line in the daemon log.
//
// It subscribes to the event bus rather than instrumenting the REST, WebSocket
// and MQTT handlers individually: all three reach the CCU through
// hub.Program.Execute, which publishes ProgramExecutedEvent, so one subscriber
// covers every route and a fourth route added later is covered for free.
//
// The record answers a question that is otherwise unanswerable when an
// operator reports a program running twice. The CCU executes the program
// either way, and its own log does not say who asked; without this record
// there is no way to distinguish a duplicate the daemon sent from one the CCU
// produced on its own — and those two point at entirely different causes. The
// log line exists alongside the audit entry because operators debug from the
// daemon log first — an execution that only surfaces in the audit database is
// invisible in exactly the situation the record was built for. Source names
// the ingress surface (see [hmevent.ProgramExecutedEvent.Source]).
//
// Returns a teardown that removes every subscription. It is a safe no-op when
// there is nothing to wire.
//
// The subscription rides the registry observer because a boot walk saw only
// the centrals present at boot: a CCU adopted at runtime got no subscription
// at all, so a program run on it left neither an audit row nor a log line —
// the record built to answer "did we send that second execution?" was silently
// empty for precisely the central the operator was asking about, while it
// worked for its neighbours.
func wireProgramExecuteAudit(
	reg *central.Registry, rec audit.Recorder, logger *slog.Logger,
) (teardown func()) {
	if reg == nil || rec == nil {
		return func() {}
	}
	if logger == nil {
		logger = slog.Default()
	}
	return reg.OnRegisterDeclared(wiring.Seam{
		Name:         "audit.program_execute",
		Collaborator: "subscribeProgramExecuteAudit",
		Phase:        wiring.PhasePerCentral,
		Why:          "a program run on the central is neither audited nor logged, so the audit trail cannot answer who triggered it and the log a reader falls back to has no entry either — this subscriber writes both",
	}, func(u *central.Unit) func() {
		return subscribeProgramExecuteAudit(u, rec, logger)
	})
}

// subscribeProgramExecuteAudit records one central's program executions. It
// returns the unsubscribe closure, or nil when the central has no bus to
// subscribe to.
func subscribeProgramExecuteAudit(u *central.Unit, rec audit.Recorder, logger *slog.Logger) func() {
	if u == nil || u.EventBus == nil || rec == nil {
		return nil
	}
	return events.Subscribe(u.EventBus, func(e hmevent.ProgramExecutedEvent) {
		source := e.Source
		if source == "" {
			source = "unknown"
		}
		logger.Info("program.execute",
			slog.String("central", e.CentralName),
			slog.String("program", e.ProgramID),
			slog.String("source", source),
			slog.Bool("success", e.Success))
		rec.Record(audit.Entry{
			Action: audit.ActionProgramExecute,
			Note: fmt.Sprintf("central=%s program=%s trigger=%s source=%s success=%t",
				e.CentralName, e.ProgramID, e.Trigger, source, e.Success),
		})
	})
}
