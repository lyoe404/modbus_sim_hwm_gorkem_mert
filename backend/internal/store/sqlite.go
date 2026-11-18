// Package store persists measurements and events in SQLite.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver: no cgo/gcc needed

	"backend/internal/poller"
)

const schema = `
CREATE TABLE IF NOT EXISTS measurements (
	id     INTEGER PRIMARY KEY AUTOINCREMENT,
	source TEXT    NOT NULL,
	metric TEXT    NOT NULL,
	value  REAL    NOT NULL,
	ts     INTEGER NOT NULL            -- unix milliseconds, UTC
);
CREATE INDEX IF NOT EXISTS idx_measurements_series ON measurements(source, metric, ts);

CREATE TABLE IF NOT EXISTS events (
	id      INTEGER PRIMARY KEY AUTOINCREMENT,
	ts      INTEGER NOT NULL,          -- unix milliseconds, UTC
	kind    TEXT    NOT NULL,          -- e.g. status_change, fan_transition
	source  TEXT    NOT NULL,
	message TEXT    NOT NULL,
	actor   TEXT    NOT NULL DEFAULT '' -- username for audited user actions
);
CREATE INDEX IF NOT EXISTS idx_events_ts ON events(ts);

-- Runtime-editable configuration (e.g. control thresholds). Every change is
-- also written to the events log, which is the history.
CREATE TABLE IF NOT EXISTS device_states (
    device_id TEXT PRIMARY KEY,
    state TEXT NOT NULL,
    last_seen INTEGER, -- NULL until first successful poll
    last_error TEXT NOT NULL DEFAULT '',
    consecutive_failures INTEGER NOT NULL DEFAULT 0,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS config (
	key        TEXT    PRIMARY KEY,
	value      TEXT    NOT NULL,       -- JSON
	updated_at INTEGER NOT NULL,       -- unix milliseconds, UTC
	updated_by TEXT    NOT NULL DEFAULT ''
);
`

// Event is an audit / state-change record.
type Event struct {
	ID      int64     `json:"id"`
	At      time.Time `json:"at"`
	Kind    string    `json:"kind"`
	Source  string    `json:"source"`
	Message string    `json:"message"`
	Actor   string    `json:"actor,omitempty"`
}

// Store is a SQLite-backed store. It implements poller.Sink.
type Store struct {
	db           *sql.DB
	observeEvent func(string)
}

// SetEventObserver configures observation of committed audit events before serving.
func (s *Store) SetEventObserver(fn func(string)) { s.observeEvent = fn }

// Open opens (creating if needed) the database at path and applies the schema.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// SQLite allows one writer; a single connection keeps this simple and
	// race-free, and makes the PRAGMAs below apply to every query.
	db.SetMaxOpenConns(1)
	for _, stmt := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		schema,
	} {
		if _, err := db.Exec(stmt); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("init database: %w", err)
		}
	}
	return &Store{db: db}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Save implements poller.Sink: all measurements of one poll go in one transaction.
func (s *Store) Save(ctx context.Context, ms []poller.Measurement) error {
	if len(ms) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() // no-op after a successful Commit

	stmt, err := tx.PrepareContext(ctx, "INSERT INTO measurements(source, metric, value, ts) VALUES(?,?,?,?)")
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, m := range ms {
		if _, err := stmt.ExecContext(ctx, m.Source, m.Metric, m.Value, m.At.UnixMilli()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Measurements returns a series in time order, newest `limit` rows since `since`.
func (s *Store) Measurements(ctx context.Context, source, metric string, since time.Time, limit int) ([]poller.Measurement, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT source, metric, value, ts FROM (
			SELECT source, metric, value, ts FROM measurements
			WHERE source = ? AND metric = ? AND ts >= ?
			ORDER BY ts DESC LIMIT ?
		) ORDER BY ts ASC`, source, metric, since.UnixMilli(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []poller.Measurement
	for rows.Next() {
		var m poller.Measurement
		var ts int64
		if err := rows.Scan(&m.Source, &m.Metric, &m.Value, &ts); err != nil {
			return nil, err
		}
		m.At = time.UnixMilli(ts).UTC()
		out = append(out, m)
	}
	return out, rows.Err()
}

// AddEvent appends an event to the audit log.
func (s *Store) AddEvent(ctx context.Context, e Event) error {
	if e.At.IsZero() {
		e.At = time.Now()
	}
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO events(ts, kind, source, message, actor) VALUES(?,?,?,?,?)",
		e.At.UnixMilli(), e.Kind, e.Source, e.Message, e.Actor)
	if err == nil && s.observeEvent != nil {
		s.observeEvent(e.Kind)
	}
	return err
}

// Events returns the newest `limit` events, newest first.
func (s *Store) Events(ctx context.Context, limit int) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, ts, kind, source, message, actor FROM events ORDER BY ts DESC, id DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Event
	for rows.Next() {
		var e Event
		var ts int64
		if err := rows.Scan(&e.ID, &ts, &e.Kind, &e.Source, &e.Message, &e.Actor); err != nil {
			return nil, err
		}
		e.At = time.UnixMilli(ts).UTC()
		out = append(out, e)
	}
	return out, rows.Err()
}

// GetConfig returns the stored value for key; found is false if none was saved.
func (s *Store) GetConfig(ctx context.Context, key string) (value string, found bool, err error) {
	err = s.db.QueryRowContext(ctx, "SELECT value FROM config WHERE key = ?", key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}

// PutConfig inserts or replaces the value for key, recording who changed it.
func (s *Store) PutConfig(ctx context.Context, key, value, by string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO config(key, value, updated_at, updated_by) VALUES(?,?,?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at, updated_by=excluded.updated_by`,
		key, value, time.Now().UnixMilli(), by)
	return err
}

// SaveDeviceStatus stores the last observed health of a device. On process restart
// the API still uses live pollers (initially unknown), never the old online row.
func (s *Store) SaveDeviceStatus(ctx context.Context, st poller.DeviceStatus) error {
	var lastSeen any
	if !st.LastSeen.IsZero() {
		lastSeen = st.LastSeen.UnixMilli()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO device_states
        (device_id, state, last_seen, last_error, consecutive_failures, updated_at)
        VALUES (?, ?, ?, ?, ?, ?)
        ON CONFLICT(device_id) DO UPDATE SET
        state=excluded.state, last_seen=excluded.last_seen,
        last_error=excluded.last_error, consecutive_failures=excluded.consecutive_failures,
        updated_at=excluded.updated_at`,
		st.Name, string(st.State), lastSeen, st.LastError, st.ConsecutiveFailures, time.Now().UnixMilli())
	return err
}

// DeviceStates returns persisted statuses for diagnostics/history, not an
// authoritative source for current control decisions.
func (s *Store) DeviceStates(ctx context.Context) ([]poller.DeviceStatus, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT device_id, state, last_seen, last_error,
        consecutive_failures FROM device_states ORDER BY device_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []poller.DeviceStatus{}
	for rows.Next() {
		var st poller.DeviceStatus
		var state string
		var lastSeen sql.NullInt64
		if err := rows.Scan(&st.Name, &state, &lastSeen, &st.LastError, &st.ConsecutiveFailures); err != nil {
			return nil, err
		}
		st.State = poller.State(state)
		if lastSeen.Valid {
			st.LastSeen = time.UnixMilli(lastSeen.Int64).UTC()
		}
		result = append(result, st)
	}
	return result, rows.Err()
}
