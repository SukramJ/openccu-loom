// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package matteradapter

import (
	"cmp"
	"slices"
	"sync"
	"time"
)

// FindingCode names what the assembly found about one source.
type FindingCode string

const (
	// FindingDeviceRefused is a source go-fabric's device kit refused or
	// does not carry the device type of;
	// the source yields no endpoint.
	FindingDeviceRefused FindingCode = "device_refused"
	// FindingDeviceWithheld is a source the configured specification level
	// (north.matter.spec_level) withholds; the operator's choice, not a
	// defect.
	FindingDeviceWithheld FindingCode = "device_withheld"
)

// Finding is one source the assembly could not serve, as the operator
// should see it. It persists while the walk keeps finding the same thing
// and is dropped once the source builds or leaves the fleet.
type Finding struct {
	Code           FindingCode
	Key            string
	DeviceType     uint16
	DeviceAddress  string
	ChannelAddress string
	Name           string
	Err            string
	// Since is when the finding was first seen; it does not move while
	// the finding persists.
	Since time.Time
}

// findingEntry is a held finding plus the walk generation that last saw
// it, which is what a completed walk prunes by.
type findingEntry struct {
	finding Finding
	scope   string
	seen    uint64
}

// findingRegistry holds the assembly's findings, one per source key. It is
// the single record behind both the once-per-key log line and
// [Assembler.Findings], so the two can never disagree.
type findingRegistry struct {
	mu      sync.Mutex
	gen     uint64
	entries map[string]*findingEntry
}

// record stores f for the source in scope and reports whether it is new:
// a key seen for the first time, or a key whose code changed. A finding
// already held keeps its Since; every call marks it seen by the current
// walk generation.
func (r *findingRegistry) record(scope string, f Finding) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.entries[f.Key]; ok && e.finding.Code == f.Code {
		e.seen = r.gen
		return false
	}
	if r.entries == nil {
		r.entries = make(map[string]*findingEntry)
	}
	r.entries[f.Key] = &findingEntry{finding: f, scope: scope, seen: r.gen}
	return true
}

// clear drops the finding of key: the source built.
func (r *findingRegistry) clear(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.entries, key)
}

// beginWalk opens a walk generation and returns it. Every finding the walk
// records is stamped with it (or a later one, when walks overlap), which
// is what [findingRegistry.prune] keeps.
func (r *findingRegistry) beginWalk() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gen++
	return r.gen
}

// prune drops the findings of scope that no walk since gen has seen: the
// walk of scope completed and did not meet their source, so the device was
// deleted, unexposed or no longer projects it.
func (r *findingRegistry) prune(scope string, gen uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for k, e := range r.entries {
		if e.scope == scope && e.seen < gen {
			delete(r.entries, k)
		}
	}
}

// dropScope drops every finding of scope: the central left the
// configuration.
func (r *findingRegistry) dropScope(scope string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for k, e := range r.entries {
		if e.scope == scope {
			delete(r.entries, k)
		}
	}
}

// snapshot returns a copy of every held finding, ordered by code and then
// key so two reads of an unchanged registry compare equal.
func (r *findingRegistry) snapshot() []Finding {
	r.mu.Lock()
	out := make([]Finding, 0, len(r.entries))
	for _, e := range r.entries {
		out = append(out, e.finding)
	}
	r.mu.Unlock()
	slices.SortFunc(out, func(a, b Finding) int {
		return cmp.Or(cmp.Compare(a.Code, b.Code), cmp.Compare(a.Key, b.Key))
	})
	return out
}

// Findings returns the sources the assembly currently cannot serve —
// refused by the kit or withheld by the specification level — in a
// deterministic order (code, then key). The slice is a copy.
func (a *Assembler) Findings() []Finding {
	return a.findings.snapshot()
}

// recordFinding records a finding for the source in and logs it the first
// time it is seen; the source retries its build on every assembly and the
// finding does not change between two of them, so the log names it once.
func (a *Assembler) recordFinding(in specInput, code FindingCode, err error) {
	f := Finding{
		Code:       code,
		Key:        in.key.String(),
		DeviceType: in.deviceType,
		Name:       in.friendlyName,
		Err:        err.Error(),
		Since:      a.now(),
	}
	if in.dev != nil {
		f.DeviceAddress = in.dev.Address
	}
	if in.ch != nil {
		f.ChannelAddress = in.ch.Address
	}
	if !a.findings.record(in.key.CentralName, f) {
		return
	}
	if code == FindingDeviceWithheld {
		a.logWithheld(in.deviceType, f.Key, err)
		return
	}
	a.logRefused(in.deviceType, f.Key, err)
}

// now is the configured clock, or the wall clock.
func (a *Assembler) now() time.Time {
	if a.cfg.Now != nil {
		return a.cfg.Now()
	}
	return time.Now()
}
