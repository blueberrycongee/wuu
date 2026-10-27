package personalreview

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Settings belongs only to the Personal review store. Neither reading nor
// changing it reads, inherits, or writes any Coding configuration.
type Settings struct {
	AutoReviewEnabled bool     `json:"auto_review_enabled"`
	Revision          int64    `json:"revision"`
	PolicyVersion     string   `json:"policy_version"`
	Scope             []string `json:"scope"`
}

const PersonalFilesPolicyVersion = "personal-files.v1"

// GetSettings creates the conservative, manual-only default on first use.
// The host must authenticate callers and serialize policy changes with dispatch.
func (s *Store) GetSettings(ctx context.Context, spaceID string) (Settings, error) {
	if s.documents != nil {
		return s.documentGetSettings(ctx, spaceID)
	}
	if strings.TrimSpace(spaceID) == "" {
		return Settings{}, errors.New("Personal review settings require a space")
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO personal_review_settings(space,revision,policy_version,auto_review_enabled) VALUES(?,1,?,0) ON CONFLICT DO NOTHING`, spaceID, PersonalFilesPolicyVersion); err != nil {
		return Settings{}, err
	}
	settings := Settings{Scope: []string{"write_file", "edit_file"}}
	if err := s.db.QueryRowContext(ctx, `SELECT revision,policy_version,auto_review_enabled FROM personal_review_settings WHERE space=?`, spaceID).Scan(&settings.Revision, &settings.PolicyVersion, &settings.AutoReviewEnabled); err != nil {
		return Settings{}, err
	}
	if settings.PolicyVersion != PersonalFilesPolicyVersion {
		return Settings{}, errors.New("Personal review policy version changed; new owner opt-in is required")
	}
	if settings.Revision < 1 || settings.Revision > 1<<53-1 {
		return Settings{}, errors.New("Personal settings revision exceeds exact transport range")
	}
	return settings, nil
}

// SettingsEvent is a durable record of one settings choice, not authorization
// to perform any action. Defaults are not attributed to a human choice.
type SettingsEvent struct {
	Actor         string    `json:"actor"`
	At            time.Time `json:"at"`
	OldEnabled    bool      `json:"old_enabled"`
	NewEnabled    bool      `json:"new_enabled"`
	Revision      int64     `json:"revision"`
	PolicyVersion string    `json:"policy_version"`
}

// SetSettings applies only the exact displayed revision and commits its audit
// in the same transaction. A missing response is resolved by reading settings,
// not automatically repeating the mutation. The authenticated host must supply
// its stable owner ID and serialize changes with dispatch. An omitted actor is
// recorded as host, never as an owner assertion, for internal host callers.
func (s *Store) SetSettings(ctx context.Context, spaceID string, enabled bool, expectedRevision int64, ownerID ...string) (Settings, error) {
	if s.documents != nil {
		return s.documentSetSettings(ctx, spaceID, enabled, expectedRevision, ownerID...)
	}
	if expectedRevision < 1 || expectedRevision >= 1<<53-1 {
		return Settings{}, ErrConflict
	}
	actor := "host"
	if len(ownerID) > 1 || len(ownerID) == 1 && strings.TrimSpace(ownerID[0]) == "" {
		return Settings{}, errors.New("settings audit requires one nonempty actor")
	}
	if len(ownerID) == 1 {
		actor = ownerID[0]
	}
	settings, err := s.GetSettings(ctx, spaceID)
	if err != nil {
		return Settings{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Settings{}, err
	}
	defer tx.Rollback()
	var previous bool
	err = tx.QueryRowContext(ctx, `SELECT auto_review_enabled FROM personal_review_settings WHERE space=? AND revision=? AND policy_version=?`, spaceID, expectedRevision, PersonalFilesPolicyVersion).Scan(&previous)
	if errors.Is(err, sql.ErrNoRows) {
		return Settings{}, ErrConflict
	}
	if err != nil {
		return Settings{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE personal_review_settings SET revision=revision+1,auto_review_enabled=? WHERE space=? AND revision=? AND policy_version=?`, enabled, spaceID, expectedRevision, PersonalFilesPolicyVersion)
	if err != nil {
		return Settings{}, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return Settings{}, err
	}
	if changed != 1 {
		return Settings{}, ErrConflict
	}
	event := SettingsEvent{Actor: actor, At: time.Now().UTC(), OldEnabled: previous, NewEnabled: enabled, Revision: expectedRevision + 1, PolicyVersion: PersonalFilesPolicyVersion}
	body, err := json.Marshal(event)
	if err != nil {
		return Settings{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO personal_review_settings_events(space,revision,body) VALUES(?,?,?)`, spaceID, event.Revision, body); err != nil {
		return Settings{}, err
	}
	if err := tx.Commit(); err != nil {
		return Settings{}, err
	}
	settings.AutoReviewEnabled, settings.Revision = enabled, event.Revision
	return settings, nil
}

// SettingsEvents is a space-scoped host audit read. It does not authenticate a
// client and must not be exposed as an unrestricted plugin or worker service.
func (s *Store) SettingsEvents(ctx context.Context, spaceID string) ([]SettingsEvent, error) {
	if s.documents != nil {
		if strings.TrimSpace(spaceID) == "" {
			return nil, errors.New("Personal settings audit requires a space")
		}
		return documentRows[SettingsEvent](ctx, s, reviewSettingsEvents, reviewPart(spaceID)+"/")
	}
	if strings.TrimSpace(spaceID) == "" {
		return nil, errors.New("Personal settings audit requires a space")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT body FROM personal_review_settings_events WHERE space=? ORDER BY revision`, spaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []SettingsEvent{}
	for rows.Next() {
		var body []byte
		var event SettingsEvent
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(body, &event); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}
