// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/SukramJ/openccu-loom/internal/config"
)

// CentralsStore persists the CCU connection list. Replaces the
// YAML `centrals:` array on production daemons. Tests and the in-process
// CCU simulator continue to consume []config.CentralConfig directly.
type CentralsStore struct {
	db *sql.DB
}

// NewCentralsStore returns a store backed by db.
func NewCentralsStore(db *sql.DB) *CentralsStore {
	return &CentralsStore{db: db}
}

// CentralRow is one row of the centrals table. Mirrors the runtime
// [config.CentralConfig] shape but holds password material as
// either an env-var name (preferred) or plaintext fallback.
type CentralRow struct {
	Name                  string                  `json:"name"`
	Host                  string                  `json:"host"`
	Port                  int                     `json:"port,omitempty"`
	JSONRPCPort           int                     `json:"json_rpc_port,omitempty"`
	Username              string                  `json:"username,omitempty"`
	PasswordEnv           string                  `json:"password_env,omitempty"`   // env var name; empty when plaintext used
	PasswordPlain         string                  `json:"password_plain,omitempty"` // plaintext fallback; populated only when allow_plaintext_secrets is on
	TLS                   bool                    `json:"tls,omitempty"`
	TLSInsecureSkipVerify bool                    `json:"tls_insecure_skip_verify,omitempty"`
	PrimaryInterface      string                  `json:"primary_interface,omitempty"`
	Interfaces            []config.InterfaceSpec  `json:"interfaces"`
	Ports                 map[string]int          `json:"ports,omitempty"`
	Visibility            config.VisibilityConfig `json:"visibility,omitempty"`
	Enabled               bool                    `json:"enabled"`
	CreatedAt             time.Time               `json:"created_at,omitempty"`
	UpdatedAt             time.Time               `json:"updated_at,omitempty"`
}

// ErrCentralNotFound is returned for missing rows.
var ErrCentralNotFound = errors.New("sqlite: central not found")

// Put creates or replaces a row.
func (s *CentralsStore) Put(ctx context.Context, r CentralRow) error {
	r.Name = strings.TrimSpace(r.Name)
	if r.Name == "" {
		return errors.New("sqlite: central name required")
	}
	if r.Host == "" {
		return errors.New("sqlite: central host required")
	}
	ifJSON, err := json.Marshal(r.Interfaces)
	if err != nil {
		return fmt.Errorf("sqlite: centrals: marshal interfaces: %w", err)
	}
	if r.Ports == nil {
		r.Ports = map[string]int{}
	}
	portsJSON, err := json.Marshal(r.Ports)
	if err != nil {
		return fmt.Errorf("sqlite: centrals: marshal ports: %w", err)
	}
	visJSON, err := json.Marshal(r.Visibility)
	if err != nil {
		return fmt.Errorf("sqlite: centrals: marshal visibility: %w", err)
	}
	now := time.Now().UTC()
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO centrals
		 (name, host, port, json_rpc_port, username, password_env, password_plain,
		  tls, tls_insecure_skip_verify, primary_interface, interfaces_json,
		  ports_json, visibility_json, enabled, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(name) DO UPDATE SET
		   host=excluded.host, port=excluded.port, json_rpc_port=excluded.json_rpc_port,
		   username=excluded.username, password_env=excluded.password_env,
		   password_plain=excluded.password_plain, tls=excluded.tls,
		   tls_insecure_skip_verify=excluded.tls_insecure_skip_verify,
		   primary_interface=excluded.primary_interface,
		   interfaces_json=excluded.interfaces_json, ports_json=excluded.ports_json,
		   visibility_json=excluded.visibility_json, enabled=excluded.enabled,
		   updated_at=excluded.updated_at`,
		r.Name, r.Host, r.Port, r.JSONRPCPort, r.Username, r.PasswordEnv, r.PasswordPlain,
		boolInt(r.TLS), boolInt(r.TLSInsecureSkipVerify), r.PrimaryInterface,
		string(ifJSON), string(portsJSON), string(visJSON), boolInt(r.Enabled),
		now, now)
	if err != nil {
		return fmt.Errorf("sqlite: centrals upsert: %w", err)
	}
	return nil
}

// Delete removes a row by name.
func (s *CentralsStore) Delete(ctx context.Context, centralName string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM centrals WHERE name = ?`, centralName)
	if err != nil {
		return fmt.Errorf("sqlite: centrals delete: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrCentralNotFound
	}
	return nil
}

// Get returns one row by name.
func (s *CentralsStore) Get(ctx context.Context, centralName string) (CentralRow, error) {
	var r CentralRow
	if err := s.scanRow(s.db.QueryRowContext(ctx, selectCentralsSQL+` WHERE name = ?`, centralName), &r); err != nil {
		return CentralRow{}, err
	}
	return r, nil
}

// List returns every row sorted by name.
func (s *CentralsStore) List(ctx context.Context) ([]CentralRow, error) {
	rows, err := s.db.QueryContext(ctx, selectCentralsSQL+` ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: centrals list: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []CentralRow
	for rows.Next() {
		var r CentralRow
		if err := s.scanRow(rows, &r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

const selectCentralsSQL = `SELECT name, host, port, json_rpc_port, username, password_env,
		    password_plain, tls, tls_insecure_skip_verify, primary_interface,
		    interfaces_json, ports_json, visibility_json, enabled,
		    created_at, updated_at FROM centrals`

// scannable is implemented by both *sql.Row and *sql.Rows so the
// scanner is shared between Get and List.
type scannable interface {
	Scan(dest ...any) error
}

func (s *CentralsStore) scanRow(row scannable, r *CentralRow) error {
	var (
		ifJSON, portsJSON, visJSON string
		tls, insec, enabled        int
	)
	err := row.Scan(&r.Name, &r.Host, &r.Port, &r.JSONRPCPort, &r.Username,
		&r.PasswordEnv, &r.PasswordPlain, &tls, &insec, &r.PrimaryInterface,
		&ifJSON, &portsJSON, &visJSON, &enabled, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrCentralNotFound
	}
	if err != nil {
		return fmt.Errorf("sqlite: centrals scan: %w", err)
	}
	r.TLS = tls != 0
	r.TLSInsecureSkipVerify = insec != 0
	r.Enabled = enabled != 0
	if err := json.Unmarshal([]byte(ifJSON), &r.Interfaces); err != nil {
		return fmt.Errorf("sqlite: centrals: parse interfaces: %w", err)
	}
	if err := json.Unmarshal([]byte(portsJSON), &r.Ports); err != nil {
		return fmt.Errorf("sqlite: centrals: parse ports: %w", err)
	}
	if err := json.Unmarshal([]byte(visJSON), &r.Visibility); err != nil {
		return fmt.Errorf("sqlite: centrals: parse visibility: %w", err)
	}
	return nil
}

// Count returns the number of central rows.
func (s *CentralsStore) Count(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM centrals`).Scan(&n); err != nil {
		return 0, fmt.Errorf("sqlite: centrals count: %w", err)
	}
	return n, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
