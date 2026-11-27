package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// SaveStaffingDecision keeps a validated staffing decision, encoded as JSON,
// under the key of the orchestrator call that made it, replacing any decision
// already kept there.
func (store *Store) SaveStaffingDecision(ctx context.Context, key string, decision json.RawMessage, decidedAt time.Time) error {
	if key == "" || !json.Valid(decision) {
		return errors.New("staffing decision key and JSON are required")
	}
	_, err := store.db.ExecContext(ctx, `INSERT INTO staffing_decisions(key,decision,decided_at) VALUES(?,?,?)
		ON CONFLICT(key) DO UPDATE SET decision=excluded.decision, decided_at=excluded.decided_at`,
		key, string(decision), timestamp(decidedAt))
	return err
}

// StaffingDecision returns the decision kept under key and when it was made;
// found is false when none is.
func (store *Store) StaffingDecision(ctx context.Context, key string) (decision json.RawMessage, decidedAt time.Time, found bool, err error) {
	var encoded, decided string
	err = store.db.QueryRowContext(ctx, `SELECT decision,decided_at FROM staffing_decisions WHERE key=?`, key).Scan(&encoded, &decided)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, time.Time{}, false, nil
	}
	if err != nil {
		return nil, time.Time{}, false, err
	}
	decidedAt, _ = time.Parse(time.RFC3339Nano, decided)
	return json.RawMessage(encoded), decidedAt, true, nil
}
