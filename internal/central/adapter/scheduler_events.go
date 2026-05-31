// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/scheduler"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// WireSchedulerEvents injects [scheduler.Job.OnStart] and
// [scheduler.Job.OnComplete] hooks into every job in jobs that publishes
// [hmevent.DataRefreshTriggeredEvent] / [hmevent.DataRefreshCompletedEvent]
// on bus.
//
// Call this after all jobs have been added to the scheduler but before
// [scheduler.Scheduler.Start]. Closes C-SCHED-2.
//
// loom:reachable:reason="scheduler event wiring; called in daemon.go after scheduler job assembly"
func WireSchedulerEvents(centralName string, bus *events.Bus, jobs []scheduler.Job) []scheduler.Job {
	wired := make([]scheduler.Job, len(jobs))
	copy(wired, jobs)
	for i, j := range wired {
		jobName := j.Name // capture for closure
		wired[i].OnStart = func(name string) {
			if j.OnStart != nil {
				j.OnStart(name)
			}
			events.Publish(bus, hmevent.DataRefreshTriggeredEvent{
				CentralName: centralName,
				JobName:     jobName,
				Scheduled:   true,
			})
		}
		wired[i].OnComplete = func(name string, durationMs int64, success bool, err error) {
			if j.OnComplete != nil {
				j.OnComplete(name, durationMs, success, err)
			}
			completed := hmevent.DataRefreshCompletedEvent{
				CentralName: centralName,
				JobName:     jobName,
				Duration:    durationMs,
				Success:     success,
			}
			if err != nil {
				completed.ErrorMessage = err.Error()
			}
			events.Publish(bus, completed)
		}
	}
	return wired
}
