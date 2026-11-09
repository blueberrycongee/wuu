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
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	_ "modernc.org/sqlite"
)

// Row retains SQLite storage types; BlobColumns distinguishes a blob from text
// with identical base64 content in the portable JSON archive. TextBytes retains
// invalid UTF-8 TEXT bytes that JSON would otherwise replace; Values still holds
// the original SQLite string for typed converters.
type Row struct {
	Values      map[string]any    `json:"values"`
	BlobColumns []string          `json:"blob_columns,omitempty"`
	TextBytes   map[string][]byte `json:"text_bytes,omitempty"`
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
	return read(ctx, path, func(table *Table, row Row) {
		table.Rows = append(table.Rows, row)
	})
}

// Fingerprint verifies every application table in one read-only transaction
// without retaining its rows. It uses the same JSON bytes and SQLite storage
// types as Read. Legacy writers must remain stopped through verification.
func Fingerprint(ctx context.Context, path string) (string, bool, error) {
	snapshot, exists, err := read(ctx, path, nil)
	return snapshot.Fingerprint, exists, err
}

func read(ctx context.Context, path string, visit func(*Table, Row)) (Snapshot, bool, error) {
	var snapshot Snapshot
	if err := ctx.Err(); err != nil {
		return snapshot, false, err
	}
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
	// Match json.Marshal(Snapshot) byte for byte, including nil slices and
	// default escaping. Only the current row needs an encoded buffer.
	digest := sha256.New()
	io.WriteString(digest, `{"tables":`)
	if snapshot.Tables == nil {
		io.WriteString(digest, `null`)
	} else {
		io.WriteString(digest, `[`)
	}
	for i := range snapshot.Tables {
		table := &snapshot.Tables[i]
		if i > 0 {
			io.WriteString(digest, `,`)
		}
		encodedName, _ := json.Marshal(table.Name)
		schema, _ := json.Marshal(table.Schema)
		io.WriteString(digest, `{"name":`)
		digest.Write(encodedName)
		io.WriteString(digest, `,"schema":`)
		digest.Write(schema)
		io.WriteString(digest, `,"rows":`)
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
		hasRows := false
		for rows.Next() {
			if err := ctx.Err(); err != nil {
				rows.Close()
				return snapshot, false, err
			}
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
				switch stored := value.(type) {
				case []byte:
					value = append([]byte(nil), stored...)
					row.BlobColumns = append(row.BlobColumns, column)
				case string:
					if !utf8.ValidString(stored) {
						if row.TextBytes == nil {
							row.TextBytes = make(map[string][]byte)
						}
						row.TextBytes[column] = []byte(stored)
					}
				}
				row.Values[column] = value
			}
			sort.Strings(row.BlobColumns)
			raw, err := json.Marshal(row)
			if err != nil {
				rows.Close()
				return snapshot, false, err
			}
			if hasRows {
				io.WriteString(digest, `,`)
			} else {
				io.WriteString(digest, `[`)
				hasRows = true
			}
			digest.Write(raw)
			if visit != nil {
				visit(table, row)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return snapshot, false, err
		}
		if hasRows {
			io.WriteString(digest, `]`)
		} else {
			io.WriteString(digest, `null`)
		}
		io.WriteString(digest, `}`)
	}
	if err := ctx.Err(); err != nil {
		return snapshot, false, err
	}
	if snapshot.Tables != nil {
		io.WriteString(digest, `]`)
	}
	io.WriteString(digest, `}`)
	snapshot.Fingerprint = hex.EncodeToString(digest.Sum(nil))
	return snapshot, true, nil
}
