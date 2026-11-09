package personalreview

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"

	"github.com/blueberrycongee/wuu/internal/persistence"
)

// LegacyMigrationInspection describes the logical snapshot, including committed
// WAL rows. It contains no action contents, credentials, or reusable authority.
type LegacyMigrationInspection struct {
	MigrationRecorded bool   `json:"migration_recorded"`
	SourceDigest      string `json:"source_digest"`
	Actions           int    `json:"actions"`
	Events            int    `json:"events"`
	Settings          int    `json:"settings"`
	SettingsEvents    int    `json:"settings_events"`
}

var ErrLegacySourceChanged = errors.New("legacy review source digest changed")

// InspectLegacyMigration reads a source snapshot and an optional selected
// migration marker without claiming namespaces or importing review records.
func InspectLegacyMigration(ctx context.Context, path string, documents persistence.Documents) (LegacyMigrationInspection, error) {
	writes, err := legacyReviewWrites(ctx, path)
	if err != nil {
		return LegacyMigrationInspection{}, err
	}
	result := inspectLegacyWrites(writes)
	if documents == nil {
		return result, nil
	}
	key, err := legacyReviewMarkerKey(path)
	if err != nil {
		return result, err
	}
	view, err := documents.Read(ctx, persistence.Query{Namespace: reviewMigrations, Keys: []string{key}})
	if err != nil {
		return result, err
	}
	if len(view.Documents) == 1 {
		var marker legacyReviewMarker
		result.MigrationRecorded = json.Unmarshal(view.Documents[0].Value, &marker) == nil && marker.Format == "personal-review-legacy-sqlite.v1" && marker.Digest == result.SourceDigest
	}
	return result, nil
}

func inspectLegacyWrites(writes []persistence.Write) LegacyMigrationInspection {
	result := LegacyMigrationInspection{SourceDigest: legacyReviewDigest(writes)}
	for _, write := range writes {
		switch write.Namespace {
		case reviewActions:
			result.Actions++
		case reviewEvents:
			result.Events++
		case reviewSettings:
			result.Settings++
		case reviewSettingsEvents:
			result.SettingsEvents++
		}
	}
	return result
}

// MigrateLegacy copies a stopped legacy ledger into an empty selected ledger.
// The caller owns stopping writers and retaining the destination generation.
// This explicit maintenance operation never runs implicitly during startup,
// changes the source, revives dispatch claims, or overwrites selected records.
func MigrateLegacy(ctx context.Context, path string, documents persistence.Documents) error {
	_, err := migrateLegacy(ctx, path, documents, "")
	return err
}

// MigrateLegacyExpected compares the operator's digest against the same frozen
// read-only logical snapshot used by the atomic destination import. A changed
// source, nonempty destination, or unknown commit outcome never overwrites or
// deletes the source. CheckLegacyMigration can verify a committed marker later.
func MigrateLegacyExpected(ctx context.Context, path string, documents persistence.Documents, expected string) (LegacyMigrationInspection, error) {
	if expected == "" {
		return LegacyMigrationInspection{}, errors.New("expected source digest is required")
	}
	return migrateLegacy(ctx, path, documents, expected)
}

func migrateLegacy(ctx context.Context, path string, documents persistence.Documents, expected string) (LegacyMigrationInspection, error) {
	if documents == nil {
		return LegacyMigrationInspection{}, errors.New("Personal review requires selected document storage")
	}
	writes, err := legacyReviewWrites(ctx, path)
	if err != nil {
		return LegacyMigrationInspection{}, err
	}
	inspection := inspectLegacyWrites(writes)
	if expected != "" && expected != inspection.SourceDigest {
		return inspection, ErrLegacySourceChanged
	}
	markerKey, err := legacyReviewMarkerKey(path)
	if err != nil {
		return inspection, err
	}
	if err := persistence.ClaimNamespaces(ctx, documents, reviewNamespaces()); err != nil {
		return inspection, err
	}
	marker := legacyReviewMarker{Format: "personal-review-legacy-sqlite.v1", Digest: inspection.SourceDigest}
	err = persistence.Update(ctx, documents, func(tx *persistence.Tx) error {
		for _, namespace := range []string{reviewActions, reviewEvents, reviewSettings, reviewSettingsEvents} {
			existing, err := tx.Read(persistence.Query{Namespace: namespace, Limit: 1})
			if err != nil {
				return err
			}
			if len(existing) > 0 {
				return fmt.Errorf("selected Personal review ledger is not empty: %s", namespace)
			}
		}
		for _, write := range writes {
			if err := tx.Put(write.Namespace, write.Key, write.Value); err != nil {
				return err
			}
		}
		return tx.Put(reviewMigrations, markerKey, marker)
	})
	return inspection, err
}

func legacyReviewWrites(ctx context.Context, path string) ([]persistence.Write, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("legacy review ledger must be a regular file")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	sourceURL := url.URL{Scheme: "file", Path: absolute, RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", sourceURL.String())
	if err != nil {
		return nil, err
	}
	defer db.Close()
	source, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer source.Rollback()
	var writes []persistence.Write
	add := func(namespace, key string, value any) error {
		raw, err := json.Marshal(value)
		if err == nil {
			writes = append(writes, persistence.Write{Namespace: namespace, Key: key, Value: raw})
		}
		return err
	}
	for _, table := range []struct{ table, namespace string }{{"personal_review_actions", reviewActions}, {"personal_review_events", reviewEvents}} {
		query := "SELECT space,id,revision,body FROM " + table.table
		rows, err := source.QueryContext(ctx, query)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var key Key
			var revision int64
			var body []byte
			if err = rows.Scan(&key.SpaceID, &key.ID, &revision, &body); err != nil {
				rows.Close()
				return nil, err
			}
			documentKey := reviewKey(key)
			if table.namespace == reviewActions {
				var r Record
				if json.Unmarshal(body, &r) != nil || r.Action.Key() != key || r.Revision != revision || revision < 1 {
					rows.Close()
					return nil, errors.New("invalid legacy review action")
				}
			} else {
				var event Event
				if json.Unmarshal(body, &event) != nil || event.Revision != revision || revision < 1 {
					rows.Close()
					return nil, errors.New("invalid legacy review audit")
				}
				documentKey = reviewRevisionKey(documentKey, revision)
			}
			writes = append(writes, persistence.Write{Namespace: table.namespace, Key: documentKey, Value: json.RawMessage(body)})
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	rows, err := source.QueryContext(ctx, `SELECT space,revision,policy_version,auto_review_enabled FROM personal_review_settings`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var space string
		settings := defaultReviewSettings()
		if err = rows.Scan(&space, &settings.Revision, &settings.PolicyVersion, &settings.AutoReviewEnabled); err != nil {
			rows.Close()
			return nil, err
		}
		if err = add(reviewSettings, reviewPart(space), settings); err != nil {
			rows.Close()
			return nil, err
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = source.QueryContext(ctx, `SELECT space,revision,body FROM personal_review_settings_events`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var space string
		var revision int64
		var body []byte
		if err = rows.Scan(&space, &revision, &body); err != nil {
			rows.Close()
			return nil, err
		}
		var event SettingsEvent
		if json.Unmarshal(body, &event) != nil || event.Revision != revision {
			rows.Close()
			return nil, errors.New("invalid legacy review settings audit")
		}
		writes = append(writes, persistence.Write{Namespace: reviewSettingsEvents, Key: reviewRevisionKey(reviewPart(space), revision), Value: json.RawMessage(body)})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}

	return writes, nil
}

const reviewMigrations = "wuu.personal_review_migrations.v1"

var ErrLegacyMigrationRequired = errors.New("personal_review_legacy_migration_required")

type legacyReviewMarker struct {
	Format string `json:"format"`
	Digest string `json:"digest"`
}

func legacyReviewMarkerKey(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(filepath.Clean(absolute)))
	return hex.EncodeToString(sum[:]), nil
}
func legacyReviewDigest(writes []persistence.Write) string {
	sort.Slice(writes, func(i, j int) bool {
		if writes[i].Namespace != writes[j].Namespace {
			return writes[i].Namespace < writes[j].Namespace
		}
		return writes[i].Key < writes[j].Key
	})
	raw, _ := json.Marshal(writes)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// CheckLegacyMigration prevents silently hiding a prior ledger. It never uses
// legacy records as live authority or imports them. A changed legacy source
// requires explicit stopped-writer migration/reconciliation again.
func CheckLegacyMigration(ctx context.Context, path string, documents persistence.Documents) error {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if documents == nil {
		return ErrLegacyMigrationRequired
	}
	if err := persistence.ClaimNamespaces(ctx, documents, reviewNamespaces()); err != nil {
		return err
	}
	key, err := legacyReviewMarkerKey(path)
	if err != nil {
		return err
	}
	view, err := documents.Read(ctx, persistence.Query{Namespace: reviewMigrations, Keys: []string{key}})
	if err != nil {
		return err
	}
	if len(view.Documents) != 1 {
		return ErrLegacyMigrationRequired
	}
	var marker legacyReviewMarker
	if json.Unmarshal(view.Documents[0].Value, &marker) != nil || marker.Format != "personal-review-legacy-sqlite.v1" {
		return ErrLegacyMigrationRequired
	}
	writes, err := legacyReviewWrites(ctx, path)
	if err != nil {
		return err
	}
	if marker.Digest != legacyReviewDigest(writes) {
		return ErrLegacyMigrationRequired
	}
	return nil
}
