package session

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"time"
)

// StorageCompactionResult reports allocated database pages in bytes. These
// counts exclude temporary files and the write-ahead log.
type StorageCompactionResult struct {
	DatabaseBytesBefore int64 `json:"database_bytes_before"`
	DatabaseBytesAfter  int64 `json:"database_bytes_after"`
}

// CompactStorage rebuilds an existing sessions database and enables incremental
// reclamation for older stores. Callers should stop other Wuu processes first:
// the rebuild needs temporary disk space and holds SQLite's writer lock.
// Cancellation rolls back an unfinished VACUUM; conversation data is retained.
func CompactStorage(ctx context.Context, sessDir string) (StorageCompactionResult, error) {
	var result StorageCompactionResult
	if err := ctx.Err(); err != nil {
		return result, err
	}
	db, exists, err := openStoreForScan(sessDir)
	if err != nil {
		return result, err
	}
	if !exists {
		return result, fmt.Errorf("compact sessions database: %w", os.ErrNotExist)
	}
	defer db.Close()
	conn, err := storageMaintenanceConnection(ctx, db)
	if err != nil {
		return result, err
	}
	defer conn.Close()
	before, err := readStoragePages(ctx, conn)
	if err != nil {
		return result, err
	}
	result.DatabaseBytesBefore = before.pageCount * before.pageSize
	// Both statements must use the same connection. Setting auto_vacuum alone
	// cannot convert a populated database from NONE to INCREMENTAL.
	if _, err := conn.ExecContext(ctx, `PRAGMA auto_vacuum = INCREMENTAL`); err != nil {
		return result, fmt.Errorf("enable incremental session storage reclamation: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `VACUUM`); err != nil {
		return result, fmt.Errorf("compact sessions database: %w", err)
	}
	var busy, frames, checkpointed int
	if err := conn.QueryRowContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &frames, &checkpointed); err != nil {
		return result, fmt.Errorf("checkpoint compacted sessions database: %w", err)
	}
	if busy != 0 {
		return result, errors.New("sessions database compacted but its WAL is busy; stop other Wuu processes and retry")
	}
	after, err := readStoragePages(ctx, conn)
	if err != nil {
		return result, err
	}
	result.DatabaseBytesAfter = after.pageCount * after.pageSize
	return result, nil
}

type storagePages struct {
	pageSize   int64
	pageCount  int64
	freePages  int64
	autoVacuum int
}

func storageMaintenanceConnection(ctx context.Context, db *sql.DB) (*sql.Conn, error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	// The normal five-second busy handler can outlive context cancellation.
	// Maintenance yields immediately to other writers; these private database
	// handles are closed by the caller after maintenance finishes.
	if _, err := conn.ExecContext(ctx, `PRAGMA busy_timeout = 0`); err != nil {
		conn.Close()
		return nil, fmt.Errorf("configure session storage maintenance: %w", err)
	}
	return conn, nil
}

func readStoragePages(ctx context.Context, conn *sql.Conn) (storagePages, error) {
	var pages storagePages
	err := conn.QueryRowContext(ctx, `
SELECT page_size, page_count, freelist_count, auto_vacuum
FROM pragma_page_size, pragma_page_count, pragma_freelist_count, pragma_auto_vacuum`).Scan(
		&pages.pageSize, &pages.pageCount, &pages.freePages, &pages.autoVacuum)
	if err != nil {
		return pages, fmt.Errorf("inspect session storage pages: %w", err)
	}
	return pages, nil
}

// reclaimSessionStorage keeps routine maintenance small even for a large
// freelist. Legacy NONE databases need the explicit CompactStorage operation.
func reclaimSessionStorage(ctx context.Context, db *sql.DB) error {
	bounded, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	conn, err := storageMaintenanceConnection(bounded, db)
	if err != nil {
		return err
	}
	defer conn.Close()
	pages, err := readStoragePages(bounded, conn)
	if err != nil {
		return err
	}
	if pages.autoVacuum != 2 || pages.freePages*pages.pageSize < 64*1024*1024 || pages.freePages < pages.pageCount/4 {
		return nil
	}
	// Leave room for new writes, and reclaim at most 64 MiB of freelist pages
	// per pass (SQLite may also remove pointer-map pages). Each statement
	// releases its lock after 64 pages.
	reserve := max(int64(4*1024*1024)/pages.pageSize, pages.pageCount/10)
	remaining := min(pages.freePages-reserve, int64(64*1024*1024)/pages.pageSize)
	for remaining > 0 {
		if err := bounded.Err(); err != nil {
			return ctx.Err()
		}
		batch := min(remaining, int64(64))
		// incremental_vacuum yields one row per reclaimed page. Consume all
		// rows so the driver does not stop after the first page.
		rows, err := conn.QueryContext(bounded, fmt.Sprintf("PRAGMA incremental_vacuum(%d)", batch))
		if err != nil {
			if bounded.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("reclaim session storage: %w", err)
		}
		for rows.Next() {
		}
		readErr := rows.Err()
		closeErr := rows.Close()
		if bounded.Err() != nil {
			return ctx.Err()
		}
		if err := errors.Join(readErr, closeErr); err != nil {
			return fmt.Errorf("reclaim session storage: %w", err)
		}
		remaining -= batch
	}
	return nil
}
