// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// wireProgramExecuteAudit records every CCU program run the daemon triggers.
//
// It subscribes to the event bus rather than instrumenting the REST, WebSocket
// and MQTT handlers individually: all three reach the CCU through
// hub.Program.Execute, which publishes ProgramExecutedEvent, so one subscriber
// covers every route and a fourth route added later is covered for free.
//
// The entry answers a question that is otherwise unanswerable when an operator
// reports a program running twice. The CCU executes the program either way,
// and its own log does not say who asked; without this record there is no way
// to distinguish a duplicate the daemon sent from one the CCU produced on its
// own — and those two point at entirely different causes.
//
// Returns a teardown that removes every subscription, or a no-op when there is
// nothing to wire.
func wireProgramExecuteAudit(reg *central.Registry, rec audit.Recorder) func() {
	if reg == nil || rec == nil {
		return func() {}
	}
	var unsubs []func()
	for _, u := range reg.List() {
		bus := u.EventBus
		if bus == nil {
			continue
		}
		unsub := events.Subscribe(bus, func(e hmevent.ProgramExecutedEvent) {
			rec.Record(audit.Entry{
				Action: audit.ActionProgramExecute,
				Note: fmt.Sprintf("central=%s program=%s trigger=%s success=%t",
					e.CentralName, e.ProgramID, e.Trigger, e.Success),
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
