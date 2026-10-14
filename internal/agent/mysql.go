package agent

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/go-sql-driver/mysql"
)

// OpenMySQL stores the same terminal results and idempotent approvals as SQLite.
// Use a dedicated database; this initializes v1 tables, not arbitrary migrations.
func OpenMySQL(dsn string) (*Store, error) {
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil || cfg.DBName == "" {
		return nil, errors.New("valid MYSQL_DSN with database required")
	}
	cfg.Timeout = 5 * time.Second
	cfg.ReadTimeout = 5 * time.Second
	cfg.WriteTimeout = 5 * time.Second
	connector, err := mysql.NewConnector(cfg)
	if err != nil {
		return nil, errors.New("invalid MySQL configuration")
	}
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(3 * time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	statements := []string{
		`CREATE TABLE IF NOT EXISTS runs (id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY, question TEXT NOT NULL, result LONGTEXT NOT NULL, created BIGINT NOT NULL) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS tickets (id BIGINT PRIMARY KEY AUTO_INCREMENT, run_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL UNIQUE, title TEXT NOT NULL, body TEXT NOT NULL, approved_at BIGINT NOT NULL, FOREIGN KEY(run_id) REFERENCES runs(id)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
	}
	for _, statement := range statements {
		if _, err = db.ExecContext(ctx, statement); err != nil {
			db.Close()
			return nil, errors.New("MySQL schema initialization failed; check database and privileges")
		}
	}
	return &Store{db: db, dialect: "mysql"}, nil
}
