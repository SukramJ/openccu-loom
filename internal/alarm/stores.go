// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package alarm

import (
	"database/sql"

	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// Stores bundles the seven alarm access types over the shared daemon
// database handle.
type Stores struct {
	Areas     *sqlitestore.AlarmAreaStore
	Sensors   *sqlitestore.AlarmSensorStore
	Outputs   *sqlitestore.AlarmOutputStore
	State     *sqlitestore.AlarmStateStore
	Incidents *sqlitestore.AlarmIncidentStore
	Journal   *sqlitestore.AlarmJournalStore
	Runtime   *sqlitestore.AlarmRuntimeStore
}

// NewStores builds the bundle on db.
func NewStores(db *sql.DB) *Stores {
	return &Stores{
		Areas:     sqlitestore.NewAlarmAreaStore(db),
		Sensors:   sqlitestore.NewAlarmSensorStore(db),
		Outputs:   sqlitestore.NewAlarmOutputStore(db),
		State:     sqlitestore.NewAlarmStateStore(db),
		Incidents: sqlitestore.NewAlarmIncidentStore(db),
		Journal:   sqlitestore.NewAlarmJournalStore(db),
		Runtime:   sqlitestore.NewAlarmRuntimeStore(db),
	}
}
