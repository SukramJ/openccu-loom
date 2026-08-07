// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"fmt"
	"log/slog"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
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
// Returns a teardown that removes every subscription, or a no-op when there is
// nothing to wire.
func wireProgramExecuteAudit(reg *central.Registry, rec audit.Recorder, logger *slog.Logger) func() {
	if reg == nil || rec == nil {
		return func() {}
	}
	if logger == nil {
		logger = slog.Default()
	}
	var unsubs []func()
	for _, u := range reg.List() {
		bus := u.EventBus
		if bus == nil {
			continue
		}
		unsub := events.Subscribe(bus, func(e hmevent.ProgramExecutedEvent) {
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
		if unsub != nil {
			unsubs = append(unsubs, unsub)
		}
	}
	return func() {
		for _, unsub := range unsubs {
			unsub()
		}
	}
}
