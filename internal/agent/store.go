package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	_ "modernc.org/sqlite"
	"time"
)

type Store struct {
	db      *sql.DB
	dialect string
}

func Open(path string) (*Store, error) {
	db, e := sql.Open("sqlite", path)
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	_, e = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000; PRAGMA foreign_keys=ON;
 CREATE TABLE IF NOT EXISTS runs(id TEXT PRIMARY KEY,question TEXT NOT NULL,result TEXT NOT NULL,created INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS tickets(id INTEGER PRIMARY KEY,run_id TEXT NOT NULL UNIQUE REFERENCES runs(id),title TEXT NOT NULL,body TEXT NOT NULL,approved_at INTEGER NOT NULL);`)
	if e != nil {
		db.Close()
		return nil, e
	}
	return &Store{db: db, dialect: "sqlite"}, nil
}
func (s *Store) Close() error { return s.db.Close() }
func (s *Store) Save(ctx context.Context, q string, r Result) error {
	b, e := json.Marshal(r)
	if e != nil {
		return e
	}
	_, e = s.db.ExecContext(ctx, "INSERT INTO runs VALUES(?,?,?,?)", r.ID, q, string(b), time.Now().UnixMilli())
	return e
}
func (s *Store) Get(ctx context.Context, id string) (Result, error) {
	var b string
	e := s.db.QueryRowContext(ctx, "SELECT result FROM runs WHERE id=?", id).Scan(&b)
	if e != nil {
		return Result{}, e
	}
	var r Result
	e = json.Unmarshal([]byte(b), &r)
	return r, e
}

type Ticket struct {
	ID    int64  `json:"id"`
	RunID string `json:"run_id"`
	Title string `json:"title"`
	Body  string `json:"body"`
}

var ErrNoDraft = errors.New("completed draft required")

// Approval is a separate human-authorized path, not an agent tool. Unique run_id makes repeats safe.
func (s *Store) Approve(ctx context.Context, id string) (Ticket, error) {
	r, e := s.Get(ctx, id)
	if e != nil {
		return Ticket{}, e
	}
	if r.Status != "completed" || r.Answer == nil || r.Answer.Draft == nil {
		return Ticket{}, ErrNoDraft
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return Ticket{}, e
	}
	defer tx.Rollback()
	insert := "INSERT INTO tickets(run_id,title,body,approved_at) VALUES(?,?,?,?) ON CONFLICT(run_id) DO NOTHING"
	if s.dialect == "mysql" {
		insert = "INSERT INTO tickets(run_id,title,body,approved_at) VALUES(?,?,?,?) ON DUPLICATE KEY UPDATE run_id=run_id"
	}
	_, e = tx.ExecContext(ctx, insert, id, r.Answer.Draft.Title, r.Answer.Draft.Body, time.Now().UnixMilli())
	if e != nil {
		return Ticket{}, e
	}
	var t Ticket
	e = tx.QueryRowContext(ctx, "SELECT id,run_id,title,body FROM tickets WHERE run_id=?", id).Scan(&t.ID, &t.RunID, &t.Title, &t.Body)
	if e != nil {
		return t, e
	}
	return t, tx.Commit()
}
