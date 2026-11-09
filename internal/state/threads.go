package state

import (
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/alexjsp/hammock/internal/thread"
)

// PutThread upserts a thread and replaces its records (threads are
// re-decoded from the harness's transcript as it grows; records are sealed
// so unchanged prefixes keep their hashes).
func (s *Store) PutThread(t thread.Thread, recs []thread.Record) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	tb, _ := json.Marshal(t)
	if _, err := tx.Exec(`INSERT INTO threads(id,harness,native_id,data) VALUES(?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET data=excluded.data`, t.ID, t.Harness, t.NativeID, tb); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM thread_records WHERE thread_id=?`, t.ID); err != nil {
		return err
	}
	for _, r := range recs {
		rb, _ := json.Marshal(r)
		if _, err := tx.Exec(`INSERT INTO thread_records(thread_id,seq,hash,data) VALUES(?,?,?,?)`, t.ID, r.Seq, r.Hash, rb); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ThreadByNative finds the thread captured from a harness session.
func (s *Store) ThreadByNative(harness, nativeID string) (thread.Thread, bool, error) {
	var b []byte
	err := s.db.QueryRow(`SELECT data FROM threads WHERE harness=? AND native_id=?`, harness, nativeID).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return thread.Thread{}, false, nil
	}
	if err != nil {
		return thread.Thread{}, false, err
	}
	var t thread.Thread
	return t, true, json.Unmarshal(b, &t)
}

// LoadThread returns a thread and its records.
func (s *Store) LoadThread(id string) (thread.Thread, []thread.Record, error) {
	var tb []byte
	if err := s.db.QueryRow(`SELECT data FROM threads WHERE id=?`, id).Scan(&tb); err != nil {
		return thread.Thread{}, nil, err
	}
	var t thread.Thread
	if err := json.Unmarshal(tb, &t); err != nil {
		return t, nil, err
	}
	rows, err := s.db.Query(`SELECT data FROM thread_records WHERE thread_id=? ORDER BY seq`, id)
	if err != nil {
		return t, nil, err
	}
	defer rows.Close()
	var recs []thread.Record
	for rows.Next() {
		var rb []byte
		if err := rows.Scan(&rb); err != nil {
			return t, nil, err
		}
		var r thread.Record
		if err := json.Unmarshal(rb, &r); err != nil {
			return t, nil, err
		}
		recs = append(recs, r)
	}
	return t, recs, rows.Err()
}
