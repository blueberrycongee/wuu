package account

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// Open connects to PostgreSQL and applies transactional schema migrations.
// The database must already exist and be dedicated to the account service.
func Open(databaseURL string) (*Store, error) {
	if !strings.HasPrefix(databaseURL, "postgres://") && !strings.HasPrefix(databaseURL, "postgresql://") {
		return nil, errors.New("account database requires a PostgreSQL URL")
	}
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		// Parser errors can include the original URL and its password.
		return nil, errors.New("invalid account PostgreSQL URL")
	}
	config.ConnectTimeout = 10 * time.Second
	config.RuntimeParams["statement_timeout"] = "15000"
	config.RuntimeParams["lock_timeout"] = "10000"
	db := stdlib.OpenDB(*config)
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxIdleTime(5 * time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, errors.New("cannot connect to account PostgreSQL database; check server availability and credentials")
	}
	if err := migrate(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func migrate(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Serialize schema initialization across processes, including an empty database.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(88117001)`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_version (id INTEGER PRIMARY KEY CHECK (id=1), version INTEGER NOT NULL)`); err != nil {
		return err
	}
	var version int
	err = tx.QueryRowContext(ctx, `SELECT version FROM schema_version WHERE id=1`).Scan(&version)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && (version < 1 || version > 3) {
		return fmt.Errorf("unsupported account database version %d", version)
	}
	if errors.Is(err, sql.ErrNoRows) {
		if _, err = tx.ExecContext(ctx, `
CREATE TABLE accounts(username TEXT PRIMARY KEY,salt BYTEA NOT NULL,password BYTEA NOT NULL,recovery BYTEA NOT NULL);
CREATE TABLE devices(pub TEXT PRIMARY KEY,account TEXT NOT NULL REFERENCES accounts(username) ON DELETE CASCADE,name TEXT NOT NULL,role TEXT NOT NULL CHECK(role IN ('host','phone')),added_at BIGINT NOT NULL);
CREATE TABLE sessions(hash BYTEA PRIMARY KEY,pub TEXT NOT NULL REFERENCES devices(pub) ON DELETE CASCADE,expires BIGINT NOT NULL);
CREATE TABLE push_devices(pub TEXT PRIMARY KEY REFERENCES devices(pub) ON DELETE CASCADE,platform TEXT NOT NULL,token TEXT NOT NULL);
CREATE INDEX devices_account ON devices(account);
CREATE INDEX sessions_pub ON sessions(pub);
CREATE INDEX sessions_expires ON sessions(expires);
INSERT INTO schema_version VALUES(1,1);
`); err != nil {
			return err
		}
	}
	if version < 2 {
		if _, err = tx.ExecContext(ctx, `
CREATE TABLE account_identities(provider TEXT NOT NULL,subject TEXT NOT NULL,account TEXT NOT NULL UNIQUE REFERENCES accounts(username) ON DELETE CASCADE,display_name TEXT NOT NULL DEFAULT '',PRIMARY KEY(provider,subject));
UPDATE schema_version SET version=2 WHERE id=1;
`); err != nil {
			return err
		}
	}
	if version < 3 {
		if _, err = tx.ExecContext(ctx, `
CREATE TABLE conversation_sync(host TEXT PRIMARY KEY REFERENCES devices(pub) ON DELETE CASCADE,enabled BOOLEAN NOT NULL DEFAULT false,generation TEXT NOT NULL,revision BIGINT NOT NULL DEFAULT 0);
CREATE TABLE conversation_copies(host TEXT NOT NULL REFERENCES conversation_sync(host) ON DELETE CASCADE,id TEXT NOT NULL,revision BIGINT NOT NULL,title TEXT NOT NULL,updated_at TEXT NOT NULL,digest TEXT NOT NULL,body JSONB,PRIMARY KEY(host,id));
CREATE INDEX conversation_changes ON conversation_copies(host,revision);
UPDATE schema_version SET version=3 WHERE id=1;
`); err != nil {
			return err
		}
	}
	return tx.Commit()
}
