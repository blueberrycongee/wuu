package personalreview

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence"
)

const (
	reviewActions        = "wuu.personal_review_actions.v1"
	reviewEvents         = "wuu.personal_review_events.v1"
	reviewSettings       = "wuu.personal_review_settings.v1"
	reviewSettingsEvents = "wuu.personal_review_settings_events.v1"
)

// NewDocumentStore binds the ledger to a selected provider. Its owner must retain
// that provider generation through cleanup; Close never closes or substitutes it.
// Storage is not an execution lease: WithExecutionAuthority must serialize
// dispatch with permission and task revocation across the entire effect.
func NewDocumentStore(documents persistence.Documents) (*Store, error) {
	if documents == nil {
		return nil, errors.New("Personal review requires selected document storage")
	}
	return &Store{documents: documents}, nil
}
func reviewNamespaces() []string {
	return []string{reviewActions, reviewEvents, reviewSettings, reviewSettingsEvents, reviewMigrations}
}
func (s *Store) claimAuthority(ctx context.Context) error {
	s.ownershipMu.Lock()
	defer s.ownershipMu.Unlock()
	if s.closed.Load() {
		return persistence.ErrClosed
	}
	if s.ownershipClaimed {
		return nil
	}
	if err := persistence.ClaimNamespaces(ctx, s.documents, reviewNamespaces()); err != nil {
		return err
	}
	s.ownershipClaimed = true
	return nil
}
func reviewPart(value string) string { return base64.RawURLEncoding.EncodeToString([]byte(value)) }
func reviewKey(key Key) string       { return reviewPart(key.SpaceID) + "/" + reviewPart(key.ID) }
func reviewRevisionKey(key string, revision int64) string {
	return fmt.Sprintf("%s/%016d", key, revision)
}
func (s *Store) documentTransaction(ctx context.Context, body func(*persistence.Tx) error) error {
	if s.closed.Load() {
		return persistence.ErrClosed
	}
	if err := s.claimAuthority(ctx); err != nil {
		return err
	}
	return persistence.Update(ctx, s.documents, body)
}
func (s *Store) documentGet(ctx context.Context, key Key) (Record, error) {
	var r Record
	err := s.documentTransaction(ctx, func(tx *persistence.Tx) error {
		found, err := tx.Get(reviewActions, reviewKey(key), &r)
		if err != nil {
			return err
		}
		if !found {
			return ErrNotFound
		}
		if r.Action.Key() != key || r.Revision < 1 {
			return errors.New("invalid review ledger identity")
		}
		return nil
	})
	return r, err
}
func (s *Store) documentSave(ctx context.Context, r Record, expected int64, actor string, at time.Time) (Record, error) {
	r.Revision = expected + 1
	event := Event{Revision: r.Revision, Status: r.Status, Outcome: r.Outcome, Reason: r.Reason, Actor: actor, At: at}
	err := s.documentTransaction(ctx, func(tx *persistence.Tx) error {
		var current Record
		found, err := tx.Get(reviewActions, reviewKey(r.Action.Key()), &current)
		if err != nil {
			return err
		}
		if found && (expected == 0 || current.Revision != expected) || !found && expected != 0 {
			return ErrConflict
		}
		if err = tx.Put(reviewActions, reviewKey(r.Action.Key()), r); err != nil {
			return err
		}
		return tx.Put(reviewEvents, reviewRevisionKey(reviewKey(r.Action.Key()), r.Revision), event)
	})
	if err != nil {
		return Record{}, err
	}
	return r, nil
}
func documentRows[T any](ctx context.Context, s *Store, namespace, prefix string) ([]T, error) {
	rows := []T{}
	err := s.documentTransaction(ctx, func(tx *persistence.Tx) error {
		rows = []T{}
		query := persistence.Query{Namespace: namespace, Prefix: prefix, Limit: 256}
		for {
			view, err := tx.ReadView(query)
			if err != nil {
				return err
			}
			for _, d := range view.Documents {
				var row T
				if err = json.Unmarshal(d.Value, &row); err != nil {
					return err
				}
				rows = append(rows, row)
			}
			if view.NextCursor == "" {
				return nil
			}
			query.Cursor, query.AtRevision = view.NextCursor, &view.Revision
		}
	})
	return rows, err
}
func defaultReviewSettings() Settings {
	return Settings{Revision: 1, PolicyVersion: PersonalFilesPolicyVersion, Scope: []string{"write_file", "edit_file"}}
}
func validateReviewSettings(settings Settings) error {
	if settings.PolicyVersion != PersonalFilesPolicyVersion {
		return errors.New("Personal review policy version changed; new owner opt-in is required")
	}
	if settings.Revision < 1 || settings.Revision > 1<<53-1 {
		return errors.New("Personal settings revision exceeds exact transport range")
	}
	return nil
}
func (s *Store) documentGetSettings(ctx context.Context, spaceID string) (Settings, error) {
	if strings.TrimSpace(spaceID) == "" {
		return Settings{}, errors.New("Personal review settings require a space")
	}
	var settings Settings
	err := s.documentTransaction(ctx, func(tx *persistence.Tx) error {
		found, err := tx.Get(reviewSettings, reviewPart(spaceID), &settings)
		if err != nil {
			return err
		}
		if !found {
			settings = defaultReviewSettings()
			return tx.Put(reviewSettings, reviewPart(spaceID), settings)
		}
		return validateReviewSettings(settings)
	})
	return settings, err
}
func (s *Store) documentSetSettings(ctx context.Context, spaceID string, enabled bool, expected int64, owners ...string) (Settings, error) {
	if expected < 1 || expected >= 1<<53-1 {
		return Settings{}, ErrConflict
	}
	if strings.TrimSpace(spaceID) == "" {
		return Settings{}, errors.New("Personal review settings require a space")
	}
	if len(owners) > 1 || len(owners) == 1 && strings.TrimSpace(owners[0]) == "" {
		return Settings{}, errors.New("settings audit requires one nonempty actor")
	}
	actor := "host"
	if len(owners) == 1 {
		actor = owners[0]
	}
	at := time.Now().UTC()
	var settings Settings
	err := s.documentTransaction(ctx, func(tx *persistence.Tx) error {
		found, err := tx.Get(reviewSettings, reviewPart(spaceID), &settings)
		if err != nil {
			return err
		}
		if !found {
			settings = defaultReviewSettings()
		}
		if err = validateReviewSettings(settings); err != nil {
			return err
		}
		if settings.Revision != expected {
			return ErrConflict
		}
		event := SettingsEvent{Actor: actor, At: at, OldEnabled: settings.AutoReviewEnabled, NewEnabled: enabled, Revision: expected + 1, PolicyVersion: PersonalFilesPolicyVersion}
		settings.AutoReviewEnabled, settings.Revision = enabled, expected+1
		if err = tx.Put(reviewSettings, reviewPart(spaceID), settings); err != nil {
			return err
		}
		return tx.Put(reviewSettingsEvents, reviewRevisionKey(reviewPart(spaceID), event.Revision), event)
	})
	if err != nil {
		return Settings{}, err
	}
	return settings, nil
}

// WithPolicyInspection fences policy writers through an effect on this exact
// selected binding. Initialize GetSettings for the relevant space beforehand;
// its conservative first-use default is itself a policy write.
func (s *Store) WithPolicyInspection(ctx context.Context, use func() error) error {
	if s == nil || s.documents == nil {
		return persistence.ErrInspectionUnsupported
	}
	if s.closed.Load() {
		return persistence.ErrClosed
	}
	if err := s.claimAuthority(ctx); err != nil {
		return err
	}
	return persistence.WithInspection(ctx, s.documents, []string{reviewSettings}, use)
}
