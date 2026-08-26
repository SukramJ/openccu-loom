// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package metrics

import (
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// SecurityCollector holds the Security & Safety metrics.
//
// The registry carries no label dimension, so the breakdown lives in
// the metric name: one counter per hazard class rather than one counter
// with a class label. That is the shape the registry supports, and
// "how many smoke reports" is the question a dashboard actually asks —
// a single undifferentiated total cannot answer it.
type SecurityCollector struct {
	notifications map[hmenum.SecurityClass]*Counter
	faultsRaised  *Counter
	faultsCleared *Counter
	openFaults    *Gauge
	severity      *Gauge
}

// NewSecurityCollector registers the metrics and subscribes them to the
// domain's own event bus. The returned stop func unsubscribes.
func NewSecurityCollector(reg *Registry, bus *events.Bus) (collector *SecurityCollector, stop func()) {
	c := &SecurityCollector{
		notifications: map[hmenum.SecurityClass]*Counter{},
		faultsRaised:  reg.Counter("security_faults_raised_total", "Total Security & Safety faults opened."),
		faultsCleared: reg.Counter("security_faults_cleared_total", "Total Security & Safety faults cleared."),
		openFaults:    reg.Gauge("security_faults_open", "Standing Security & Safety faults."),
		severity: reg.Gauge("security_severity",
			"Folded Security & Safety severity as an ordinal (0 ok, 1 info, 2 warning, 3 alarm, 4 critical)."),
	}
	for _, class := range hmenum.SecurityClasses() {
		c.notifications[class] = reg.Counter(
			"security_notifications_"+string(class)+"_total",
			"Total Security & Safety reports emitted for the "+string(class)+" class.",
		)
	}
	if bus == nil {
		return c, func() {}
	}
	unsubs := []func(){
		events.Subscribe(bus, c.onNotification),
		events.Subscribe(bus, c.onFaultChanged),
		events.Subscribe(bus, c.onStateChanged),
	}
	return c, func() {
		for _, u := range unsubs {
			u()
		}
	}
}

func (c *SecurityCollector) onNotification(e hmevent.SecurityNotificationEvent) {
	if ctr, ok := c.notifications[e.Class]; ok {
		ctr.Inc()
	}
}

func (c *SecurityCollector) onFaultChanged(e hmevent.SecurityFaultChangedEvent) {
	if e.Acknowledged {
		// An acknowledgement changes neither the count nor the ledger.
		return
	}
	if e.Open {
		c.faultsRaised.Inc()
	} else {
		c.faultsCleared.Inc()
	}
	c.openFaults.Set(float64(e.OpenCount))
}

// onStateChanged exports the folded severity as its rank rather than
// its label: a gauge a dashboard can threshold on beats a string it has
// to map.
func (c *SecurityCollector) onStateChanged(e hmevent.SecurityStateChangedEvent) {
	c.severity.Set(float64(e.To.Rank()))
	// The state event carries the standing count, so the gauge tracks
	// the domain even across a restart and a central detach — both of
	// which change the ledger without emitting a fault transition. The
	// registry exports an untouched gauge as a confident 0 rather than
	// an absent series, so an unseeded gauge is not merely missing, it
	// is wrong.
	c.openFaults.Set(float64(e.OpenFaults))
}
