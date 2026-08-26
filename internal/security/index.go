// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package security

import (
	"context"
	"sort"
	"strconv"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/safety"
	"github.com/SukramJ/openccu-loom/internal/model/security"
	"github.com/SukramJ/openccu-loom/internal/routingkey"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// RebuildIndex derives the classification index from the device model,
// the alarm enrollment and the operator overrides.
//
// It is built once per model change rather than consulted per event: a
// value-change event carries only the data-point key and the value, not
// the device model, the channel type or the value list the classifier
// needs. Resolving those on every event would mean three registry
// lookups per wire message across the whole fleet.
func (s *Service) RebuildIndex(ctx context.Context) error {
	overrides, err := s.loadOverrides(ctx)
	if err != nil {
		s.markIndexUnavailable(err)
		return err
	}
	enrolled, alarmDevices, err := s.loadEnrollment(ctx)
	if err != nil {
		s.markIndexUnavailable(err)
		return err
	}

	index := map[string]*indexedSource{}
	// seed carries the activations read straight from the model, so a
	// source that is already active when the index is built starts out
	// active rather than waiting for a change that may never come.
	seed := map[string]bool{}
	// readyCentrals names every central whose initial southbound
	// bring-up has completed at least once. RebuildIndex can run before
	// that — attachUnit's own doc notes the model is populated
	// asynchronously, "long after this service starts" — so a source's
	// absence from this pass only means "genuinely gone" when its
	// owning central is in this set; otherwise it may simply not have
	// loaded yet.
	readyCentrals := map[string]bool{}
	for _, u := range s.reg.List() {
		s.indexUnit(u, overrides, enrolled, alarmDevices, index, seed)
		readyCentrals[u.Name()] = u.IsSouthboundReady()
	}

	now := nowMS(s.clk.Now())
	s.mu.Lock()
	// The reads above succeeded, so the index once again reflects the live
	// model: clear any degraded flag a previous failed rebuild set.
	s.agg.indexHealthy = true
	// Activation state survives a rebuild for sources that still exist;
	// dropping it would make every active detector look like it cleared
	// the moment an operator saved an unrelated override.
	for key := range s.agg.active {
		if _, ok := index[key]; !ok {
			delete(s.agg.active, key)
		}
	}
	// A source the model already reports active starts active. Existing
	// entries keep their observation time — the interesting fact is when
	// a detector first fired, not when the index was last rebuilt.
	for key := range seed {
		if _, already := s.agg.active[key]; already {
			continue
		}
		s.agg.active[key] = now
		if src, ok := index[key]; ok && s.agg.classSince[src.class] == 0 {
			s.agg.classSince[src.class] = now
		}
	}
	s.agg.sources = index
	// Sources whose ledger state disagrees with the model, decided under
	// the same lock that just settled the activation set.
	reconcileFaults := map[string]*indexedSource{}
	for key, src := range index {
		if src.reason == "" || !src.relevant {
			continue
		}
		_, isActive := s.agg.active[key]
		if isActive != s.faultStandsLocked(src) {
			reconcileFaults[key] = src
		}
	}
	// Faults whose source left the model entirely (the device was
	// removed, not merely reclassified). This loop only walks the fresh
	// index above, so a fault like this is never revisited by anything
	// else either: applyFault needs an *indexedSource for the source it
	// clears, and the live wire path only dispatches for a key that is
	// still in s.agg.sources. Without closing it here, removing the
	// faulty device is an operator's only recourse for a stuck fault
	// and it does not work — the fault, and the severity it holds up,
	// would stand forever.
	var orphaned []*security.Fault
	for _, f := range s.agg.faults {
		if !readyCentrals[f.Source.Central] {
			// The owning central is not currently registered, or its
			// model has never finished loading: a source missing from
			// this pass is not evidence it is gone, only that this pass
			// cannot see it yet (or DetachCentral already owns clearing
			// it, for a central that left entirely).
			continue
		}
		if _, stillPresent := index[f.Source.Ref]; !stillPresent {
			orphaned = append(orphaned, f)
		}
	}
	snap := s.agg.snapshot()
	s.mu.Unlock()

	// A fault that arose or resolved while the daemon was down reaches
	// the ledger here and nowhere else. The seeding above only sets the
	// in-memory activation, so a device that went unreachable during a
	// restart showed as active in the class view while the fault plane
	// stayed empty: no row, no `since`, no report — and no clear event
	// afterwards either, because Clear finds no row to close.
	for key, src := range reconcileFaults {
		s.mu.Lock()
		_, isActive := s.agg.active[key]
		s.mu.Unlock()
		s.applyFault(ctx, src, isActive)
	}
	for _, f := range orphaned {
		s.clearOrphanedFault(ctx, f)
	}
	s.refreshZoneSlugs(ctx)
	// A rebuild changes what is active, which class is known and which
	// severity holds — the seeding above can turn a class on outright.
	// Publishing the result is what keeps the retained plane from
	// disagreeing with the in-memory state until some unrelated event
	// happens to reconcile it, which for a latching sensor may be never.
	s.publishState(snap)
	return nil
}

// markIndexUnavailable records that the classification index could not be
// rebuilt — its SQLite reads failed (lock contention, disk-full, a WAL
// stall). Every caller of RebuildIndex only logs and continues, and the
// empty/stale index would otherwise fold into a coherent "all clear"
// (severity OK, no active classes) published to MQTT/REST/WS — smoke,
// water, door and duress monitoring reporting "no problems" when the
// domain in fact knows nothing. Flagging the aggregate unhealthy raises
// the folded severity to a warning and stamps IndexHealthy=false on the
// snapshot, so the state reads "unknown/degraded" until a later rebuild
// succeeds. The degraded snapshot is published so the retained north-bound
// planes stop advertising the false all-clear.
func (s *Service) markIndexUnavailable(err error) {
	s.log.Error("security: classification index unavailable — reporting degraded state", "error", err)
	s.mu.Lock()
	s.agg.indexHealthy = false
	snap := s.agg.snapshot()
	s.mu.Unlock()
	s.publishState(snap)
}

// faultStandsLocked reports whether the ledger holds an open fault for
// this source and reason. The caller holds s.mu.
func (s *Service) faultStandsLocked(src *indexedSource) bool {
	for _, f := range s.agg.faults {
		if f.Source.Ref == src.ref.Ref && f.Reason == src.reason {
			return true
		}
	}
	return false
}

// refreshZoneSlugs loads the stable external identifier of every zone.
//
// Without it the domain fell back to the zone UUID, which then reached
// the MQTT topic and the consumer entity id — exactly what the slug
// exists to prevent. A pre-migration row with an empty slug gets one
// derived here rather than keeping the UUID, and the derived value is
// written back through the store so it is frozen from this boot on —
// exactly like a freshly created zone's slug — instead of being
// re-derived from the current name on every future boot, which would
// let a later rename move it and orphan every consumer entity of that
// zone.
func (s *Service) refreshZoneSlugs(ctx context.Context) {
	if s.stores.Zones == nil {
		return
	}
	rows, err := s.stores.Zones.GetAll(ctx)
	if err != nil {
		s.log.Error("security: load zone slugs", "error", err)
		return
	}
	taken := make(map[string]bool, len(rows))
	for i := range rows {
		if rows[i].Slug != "" {
			taken[rows[i].Slug] = true
		}
	}
	slugs := make([]string, len(rows))
	for i := range rows {
		if rows[i].Slug != "" {
			slugs[i] = rows[i].Slug
			continue
		}
		slug := deriveUniqueZoneSlug(rows[i].Name, taken)
		taken[slug] = true
		if err := s.stores.Zones.SetSlug(ctx, rows[i].ID, slug); err != nil {
			s.log.Error("security: persist repaired zone slug", "zone", rows[i].ID, "error", err)
		}
		slugs[i] = slug
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range rows {
		z := s.agg.zones[rows[i].ID]
		z.ID = rows[i].ID
		z.Slug = slugs[i]
		if z.Name == "" {
			z.Name = rows[i].Name
		}
		s.agg.zones[rows[i].ID] = z
	}
}

// deriveUniqueZoneSlug derives a stable external identifier for name,
// unique against taken — the same base-plus-numeric-suffix rule a
// freshly created zone gets (mirrors uniqueZoneSlug in the alarm-config
// REST handler, which this package cannot import without an inverted
// dependency).
func deriveUniqueZoneSlug(name string, taken map[string]bool) string {
	base := routingkey.HubSlug(name)
	if base == "" {
		base = "zone"
	}
	if !taken[base] {
		return base
	}
	for n := 2; ; n++ {
		candidate := base + "-" + strconv.Itoa(n)
		if !taken[candidate] {
			return candidate
		}
	}
}

// indexUnit classifies every data point of one central.
func (s *Service) indexUnit(u *central.Unit, overrides map[string]sqlitestore.SecuritySource,
	enrolled map[string]sqlitestore.AlarmSensorRow, alarmDevices map[string]bool,
	out map[string]*indexedSource, seed map[string]bool,
) {
	centralName := u.Name()
	for _, d := range u.QueryFacade().ModelDevices() {
		deviceRelevant := alarmDevices[centralName+"|"+d.Address]
		for _, ch := range d.Channels() {
			for _, dp := range ch.DataPoints() {
				param := dp.Parameter()
				key := hmevent.SecurityRefKey(centralName, dp.DataPointKey().InterfaceID, ch.Address, string(param))

				src, ok := s.classify(centralName, d.Model, ch.Type, ch.Address,
					dp.DataPointKey().InterfaceID, param, overrides[key], enrolled[key], deviceRelevant)
				if !ok {
					continue
				}
				src.ref.Name = channelDisplayName(ch, d.Name())
				// Seed the activation from the model's current value.
				//
				// Without this nothing is active until its next wire
				// event: a detector that is already wet at daemon start
				// reads inactive until it changes, which for a latching
				// hazard sensor can be never. The same gap reappears
				// after every index rebuild for newly added sources.
				src.valueList = dp.ParameterData().ValueList
				src.silentPanic = silentPanicFromConfig(enrolled[key])
				if raw, ok := dp.RawValue(); ok {
					if active, known := activeFromRaw(src.activeValues, raw,
						dp.ParameterData().ValueList); known && active {
						seed[key] = true
					}
				}
				out[key] = &src
			}
		}
	}
}

// classify decides what one data point means for the domain.
//
// Precedence is: operator override, then alarm enrollment, then the
// classifier table. An override wins outright — the operator has seen
// the device, the classifier has only seen its model string.
func (s *Service) classify(centralName, model, channelType, channelAddress, interfaceID string,
	param hmenum.Parameter, override sqlitestore.SecuritySource,
	enrollment sqlitestore.AlarmSensorRow, deviceRelevant bool,
) (indexedSource, bool) {
	// An excluded parameter is never indexed, whatever anyone
	// configured: it is something the alarm engine writes, and reading
	// it back would let the domain report its own output as a cause.
	if safety.Excluded(param) {
		return indexedSource{}, false
	}
	ref := hmevent.NewSecuritySourceRef(centralName, interfaceID, channelAddress, string(param))
	src := indexedSource{ref: ref, zoneID: enrollment.ZoneID}
	// An excluded source is still indexed, only never aggregated. Dropping
	// it outright would also drop it from the inventory, and the inventory
	// is the only place an operator can find the row to undo the
	// exclusion — they would have to already know the raw routing key.
	excluded := override.Parameter != "" && !override.Included

	switch {
	case override.Class != "":
		src.class = hmenum.SecurityClass(override.Class)
		src.relevant = true
		// Carry the classifier's reason facet even when the operator
		// overrode the class. applyFault returns early on an empty
		// reason, so dropping it here left an already-open fault with no
		// path to ever close: the clear branch is unreachable, Raise
		// deduplicates onto the standing row, Acknowledge does not
		// close, and REST offers no clear route.
		if cls, ok := safety.Classify(model, channelType, param); ok {
			src.reason = cls.Reason
			src.activeValues = cls.ActiveValues
		}
	case enrollment.ID != "":
		if cls, ok := hmenum.SecurityClassForSensorType(enrollment.SensorType); ok {
			src.class = cls
			// A tamper-typed enrollment lands on a diagnostic class, so
			// it needs a reason or its faults can never close.
			if verdict, found := safety.Classify(model, channelType, param); found {
				src.reason = verdict.Reason
				src.activeValues = verdict.ActiveValues
			}
		} else if cls, ok := safety.Classify(model, channelType, param); ok {
			src.class = cls.Class
			src.reason = cls.Reason
			src.activeValues = cls.ActiveValues
		} else {
			// An enrolled hazard sensor the classifier cannot place
			// still belongs to the domain; it just has no class yet.
			src.class = hmenum.SecurityClassIntrusion
		}
		src.relevant = true
	default:
		cls, ok := safety.Classify(model, channelType, param)
		if !ok {
			return indexedSource{}, false
		}
		src.class = cls.Class
		src.reason = cls.Reason
		src.activeValues = cls.ActiveValues
		// Hazard classes are always relevant — a smoke detector matters
		// whether or not anyone enrolled it. The diagnostic classes are
		// gated on the device carrying an alarm role, so `problem` stays
		// a real signal instead of standing permanently on across a
		// whole fleet.
		src.relevant = cls.Class.Hazard() || deviceRelevant
	}
	src.ref.Class = src.class
	if !src.class.Valid() {
		return indexedSource{}, false
	}
	if excluded {
		src.relevant = false
	}
	// The source is indexed either way; relevance decides aggregation.
	return src, true
}

// silentPanicFromConfig resolves the covert-trigger flag out of the
// enrollment's opaque config document.
//
// The flag decides whether a trigger may reach a retained surface, and
// the domain cannot read that decision from anywhere else: the sensor
// row keeps it inside ConfigJSON, and every consumer downstream sees
// only the rendered report.
func silentPanicFromConfig(row sqlitestore.AlarmSensorRow) bool {
	if row.ID == "" || row.SensorType != hmenum.AlarmSensorTypePanic {
		return false
	}
	cfg, err := engine.ParseSensorConfig(row.ConfigJSON)
	if err != nil {
		// An unparsable config is treated as covert. The safe direction
		// is to withhold, not to expose: a report that fails to arrive
		// is a bug, a report that exposes a person under coercion is a
		// harm.
		return true
	}
	return cfg.PanicSilent
}

// loadOverrides indexes the operator decisions by routing key.
func (s *Service) loadOverrides(ctx context.Context) (map[string]sqlitestore.SecuritySource, error) {
	rows, err := s.stores.Sources.GetAll(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]sqlitestore.SecuritySource, len(rows))
	for i := range rows {
		r := rows[i]
		out[hmevent.SecurityRefKey(r.CentralName, r.InterfaceID, r.ChannelAddress, r.Parameter)] = r
	}
	return out, nil
}

// loadEnrollment indexes the alarm sensors by routing key, and the
// devices that carry at least one by device key. The second half is
// what makes the diagnostic classes selective.
func (s *Service) loadEnrollment(ctx context.Context) (byRef map[string]sqlitestore.AlarmSensorRow, alarmDevices map[string]bool, err error) {
	if s.stores.Sensors == nil {
		return map[string]sqlitestore.AlarmSensorRow{}, map[string]bool{}, nil
	}
	rows, err := s.stores.Sensors.GetAll(ctx)
	if err != nil {
		return nil, nil, err
	}
	byRef = make(map[string]sqlitestore.AlarmSensorRow, len(rows))
	alarmDevices = map[string]bool{}
	for i := range rows {
		r := rows[i]
		byRef[hmevent.SecurityRefKey(r.CentralName, r.InterfaceID, r.ChannelAddress, r.Parameter)] = r
		alarmDevices[r.CentralName+"|"+hmevent.SecurityDeviceAddress(r.ChannelAddress)] = true
	}
	return byRef, alarmDevices, nil
}

// channelDisplayName picks the most useful name for a source, falling
// back through channel name, device name, address.
func channelDisplayName(ch *device.Channel, deviceName string) string {
	if n := ch.NameData().ChannelName; n != "" {
		return n
	}
	if deviceName != "" {
		return deviceName
	}
	return ch.Address
}

// Sources returns the classified inventory.
//
// This is the operator's view of what the domain believes about their
// installation, and the place an override is made from. It is
// deliberately the whole index rather than a filtered slice: a source
// the classifier got wrong is invisible in every aggregate, so the only
// way to find it is to list everything.
func (s *Service) Sources(ctx context.Context) []security.SourceView {
	overrides, err := s.loadOverrides(ctx)
	if err != nil {
		s.log.Error("security: load overrides for inventory", "error", err)
		overrides = map[string]sqlitestore.SecuritySource{}
	}
	s.mu.Lock()
	out := make([]security.SourceView, 0, len(s.agg.sources))
	for key, src := range s.agg.sources {
		at, active := s.agg.active[key]
		override, overridden := overrides[key]
		out = append(out, security.SourceView{
			Ref:              src.ref.Ref,
			Central:          src.ref.Central,
			InterfaceID:      src.ref.InterfaceID,
			ChannelAddress:   src.ref.ChannelAddress,
			DeviceAddress:    src.ref.DeviceAddress,
			Parameter:        src.ref.Parameter,
			Name:             src.ref.Name,
			Class:            src.class,
			Reason:           src.reason,
			Active:           active,
			Relevant:         src.relevant,
			ZoneID:           src.zoneID,
			Overridden:       overridden,
			OverrideIncluded: override.Included,
			SinceMS:          at,
		})
	}
	s.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Central != out[j].Central {
			return out[i].Central < out[j].Central
		}
		if out[i].ChannelAddress != out[j].ChannelAddress {
			return out[i].ChannelAddress < out[j].ChannelAddress
		}
		return out[i].Parameter < out[j].Parameter
	})
	return out
}

// SetSourceOverride records an operator decision about one data point
// and rebuilds the index so it takes effect immediately.
//
// An empty class with included=true deletes the override, returning the
// data point to the classifier's verdict — that is the "undo" a wrong
// override needs.
func (s *Service) SetSourceOverride(ctx context.Context, centralName, interfaceID, channelAddress, parameter string,
	class hmenum.SecurityClass, included bool, note string,
) error {
	if class == "" && included && note == "" {
		if err := s.stores.Sources.Delete(ctx, centralName, interfaceID, channelAddress, parameter); err != nil {
			return err
		}
		return s.RebuildIndex(ctx)
	}
	if class != "" && !class.Valid() {
		return security.ErrInvalidClass
	}
	row := sqlitestore.SecuritySource{
		CentralName: centralName, InterfaceID: interfaceID, ChannelAddress: channelAddress,
		Parameter: parameter, Class: string(class), Included: included, Note: note,
		UpdatedAt: nowMS(s.clk.Now()),
	}
	if err := s.stores.Sources.Upsert(ctx, row); err != nil {
		return err
	}
	return s.RebuildIndex(ctx)
}
