// Package legacysqlite reads the retired session database for explicit conversion.
// It never initializes, migrates, or writes the source schema.
package legacysqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	_ "modernc.org/sqlite"
)

// Row retains SQLite storage types; BlobColumns distinguishes a blob from text
// with identical base64 content in the portable JSON archive.
type Row struct {
	Values      map[string]any `json:"values"`
	BlobColumns []string       `json:"blob_columns,omitempty"`
}
type Table struct {
	Name   string `json:"name"`
	Schema string `json:"schema"`
	Rows   []Row  `json:"rows"`
}
type Snapshot struct {
	Tables      []Table `json:"tables"`
	Fingerprint string  `json:"-"`
}

// Read captures every application table in a single read-only transaction.
// The owner must stop all legacy writers before capture and keep them stopped
// until the selected documents authority is durably published.
func Read(ctx context.Context, path string) (Snapshot, bool, error) {
	var snapshot Snapshot
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return snapshot, false, nil
	} else if err != nil {
		return snapshot, false, err
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return snapshot, false, err
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(absolute)}
	q := u.Query()
	q.Set("mode", "ro")
	q.Add("_pragma", "query_only(1)")
	q.Add("_pragma", "busy_timeout(5000)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return snapshot, false, err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return snapshot, false, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT name, sql FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return snapshot, false, err
	}
	for rows.Next() {
		var table Table
		if err := rows.Scan(&table.Name, &table.Schema); err != nil {
			rows.Close()
			return snapshot, false, err
		}
		snapshot.Tables = append(snapshot.Tables, table)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return snapshot, false, err
	}
	for i := range snapshot.Tables {
		table := &snapshot.Tables[i]
		// Identifiers originate from sqlite_master and are quoted, never executed as SQL.
		name := `"` + strings.ReplaceAll(table.Name, `"`, `""`) + `"`
		rows, err := tx.QueryContext(ctx, `SELECT rowid AS __wuu_legacy_rowid, * FROM `+name+` ORDER BY rowid`)
		if err != nil {
			return snapshot, false, fmt.Errorf("read legacy table %s: %w", table.Name, err)
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			return snapshot, false, err
		}
		for rows.Next() {
			values := make([]any, len(columns))
			args := make([]any, len(columns))
			for j := range args {
				args[j] = &values[j]
			}
			if err := rows.Scan(args...); err != nil {
				rows.Close()
				return snapshot, false, err
			}
			row := Row{Values: make(map[string]any, len(columns))}
			for j, column := range columns {
				value := values[j]
				if blob, ok := value.([]byte); ok {
					value = append([]byte(nil), blob...)
					row.BlobColumns = append(row.BlobColumns, column)
				}
				row.Values[column] = value
			}
			sort.Strings(row.BlobColumns)
			table.Rows = append(table.Rows, row)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return snapshot, false, err
		}
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return snapshot, false, err
	}
	digest := sha256.Sum256(raw)
	snapshot.Fingerprint = hex.EncodeToString(digest[:])
	return snapshot, true, nil
}
