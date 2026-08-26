// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package outputs

import (
	"context"
	"time"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	sirencdp "github.com/SukramJ/openccu-loom/internal/model/custom/siren"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// chirpDuration bounds one confirmation-tone emission. The tones are
// intrinsically short; the finite duration keeps the atomic paramset
// write bounded either way (S1 applies to every acoustic write).
const chirpDuration = time.Second

// Chirp implements engine.OutputPort: best-effort confirmation
// squawks and countdown ticks on the zone's chirp outputs. Chirps
// degrade first (S5): emissions are rate-limited per output, ticks
// thin to an accelerating pattern, and every chirp is suppressed
// while an alarm activation is in flight — chirp radio budget must
// never compete with a stop.
func (m *Manager) Chirp(ctx context.Context, zoneID string, req engine.ChirpRequest) error {
	m.mu.Lock()
	instances := append([]*instance(nil), m.byZone[zoneID]...)
	zoneBusy := false
	for _, act := range m.active {
		if act.zoneID == zoneID {
			zoneBusy = true
			break
		}
	}
	m.mu.Unlock()
	if zoneBusy {
		return nil
	}
	if isTick(req.Kind) && !tickDue(req.Remaining) {
		return nil
	}
	now := m.clk.Now()
	for _, inst := range instances {
		if inst.row.Class != hmenum.AlarmOutputClassChirp {
			continue
		}
		m.mu.Lock()
		last := m.lastChirp[inst.row.ID]
		tooSoon := now.Sub(last) < chirpMinGap
		if !tooSoon {
			m.lastChirp[inst.row.ID] = now
		}
		m.mu.Unlock()
		if tooSoon {
			continue
		}
		if err := m.emitChirp(ctx, inst, req.Kind); err != nil {
			m.log.Debug("alarm chirp emission failed", "output", inst.row.ID, "error", err)
		}
	}
	return nil
}

// emitChirp plays one chirp on one output: an ASIR confirmation tone
// or an MP3 soundfile, both at low priority.
func (m *Manager) emitChirp(ctx context.Context, inst *instance, kind engine.ChirpKind) error {
	if inst.cfg.SoundfileIndex > 0 {
		dev, err := m.resolver.Sound(inst.row.CentralName, inst.row.ChannelAddress)
		if err != nil {
			return err
		}
		vol := 0.5
		if inst.cfg.Volume != nil {
			vol = *inst.cfg.Volume
		}
		return dev.PlayChirp(ctx, inst.cfg.SoundfileIndex, vol, hmenum.CommandPriorityLow)
	}
	tone := m.chirpTone(inst, kind)
	if tone == "" {
		return nil
	}
	dev, err := m.resolver.Siren(inst.row.CentralName, inst.row.ChannelAddress)
	if err != nil {
		return err
	}
	// The selection pointer is what reaches the wire; without it the
	// device replays its last-observed selection — after a full alarm
	// that would be a slice of the alarm tone instead of the chirp.
	return dev.TurnOn(ctx, sirencdp.OnConfig{
		Duration: chirpDuration, AcousticSelection: &tone, AcousticTone: tone,
	}, hmenum.CommandPriorityLow)
}

// chirpTone resolves the configured tone label for a chirp kind.
func (m *Manager) chirpTone(inst *instance, kind engine.ChirpKind) string {
	switch kind {
	case engine.ChirpArmSquawk:
		return inst.cfg.ChirpArmTone
	case engine.ChirpDisarmSquawk:
		return inst.cfg.ChirpDisarmTone
	case engine.ChirpCountdownTick, engine.ChirpEntryWarning, engine.ChirpChime:
		return inst.cfg.ChirpTickTone
	default:
		return ""
	}
}

// isTick reports whether the kind is a countdown tick.
func isTick(kind engine.ChirpKind) bool {
	return kind == engine.ChirpCountdownTick || kind == engine.ChirpEntryWarning
}

// tickDue implements the accelerating countdown pattern: one tick
// every ten seconds until the final ten, then every other second.
func tickDue(remaining time.Duration) bool {
	s := int(remaining.Round(time.Second).Seconds())
	if s <= 0 {
		return false
	}
	if s <= 10 {
		return s%2 == 0
	}
	return s%10 == 0
}
