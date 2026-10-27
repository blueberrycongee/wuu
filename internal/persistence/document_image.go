package persistence

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"time"
)

// DocumentImage is a domain transaction's JSON collections. It is private to
// one Update attempt; callbacks must not retain it or perform external effects.
// This lets legacy multi-index domains preserve their algorithms while moving
// their atomic authority to a selected state.documents provider.
type DocumentImage struct {
	Values  map[string]json.RawMessage `json:"values"`
	Version int                        `json:"version"`
	dirty   bool
}

func (i *DocumentImage) Get(key string, out any) error {
	raw, ok := i.Values[key]
	if !ok {
		return nil
	}
	return json.Unmarshal(raw, out)
}

func (i *DocumentImage) Put(key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if i.Values == nil {
		i.Values = make(map[string]json.RawMessage)
	}
	i.Values[key] = raw
	i.dirty = true
	return nil
}

// AccessDocumentImage loads the domain through the selected provider and
// atomically commits its changes. Import runs only if the provider confirms
// that no image exists, never on provider errors. The imported image is committed
// even for a read, fencing future access from stale legacy files. Update retries
// only definitive revision conflicts; uncertain commits retain their exact intent
// in CommitError and are not reconstructed or silently retried.
func AccessDocumentImage(ctx context.Context, documents Documents, namespace, key string, importLegacy func(*DocumentImage) error, body func(*DocumentImage) error) error {
	if documents == nil {
		return fmt.Errorf("%w: selected document provider is unavailable", ErrClosed)
	}
	if key == "" {
		return fmt.Errorf("%w: domain scope is required", ErrInvalid)
	}
	return Update(ctx, documents, func(tx *Tx) error {
		image := &DocumentImage{Version: 1, Values: make(map[string]json.RawMessage)}
		found, err := tx.Get(namespace, key, image)
		if err != nil {
			return err
		}
		if !found {
			if importLegacy != nil {
				if err := importLegacy(image); err != nil {
					return err
				}
			}
			image.dirty = true
		}
		if image.Version != 1 {
			return fmt.Errorf("%w: unsupported domain image version %d", ErrCorrupt, image.Version)
		}
		if err := body(image); err != nil {
			return err
		}
		if !image.dirty {
			return nil
		}
		return tx.Put(namespace, key, image)
	})
}

// MergeLegacyCollection combines copies discovered under historical workspace
// roots before publishing the one-time migration. Indexed entities use their
// stable ID and newest recorded timestamp; append-only facts deduplicate exact
// JSON values and retain chronological order. Corrupt input fails migration.
func MergeLegacyCollection(image *DocumentImage, key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	var incoming, existing []json.RawMessage
	if err := json.Unmarshal(raw, &incoming); err != nil {
		return err
	}
	if err := image.Get(key, &existing); err != nil {
		return err
	}
	if key == "events" {
		// Preserve repeated facts inside one log; only remove copies already
		// represented by another root's log, including duplicated root discovery.
		copies := make(map[string]int, len(existing))
		for _, raw := range existing {
			copies[string(raw)]++
		}
		filtered := make([]json.RawMessage, 0, len(incoming))
		for _, raw := range incoming {
			if copies[string(raw)] > 0 {
				copies[string(raw)]--
				continue
			}
			filtered = append(filtered, raw)
		}
		incoming = filtered
	}
	type entry struct {
		raw json.RawMessage
		at  time.Time
	}
	entries := make([]entry, 0, len(existing)+len(incoming))
	indexes := make(map[string]int)
	for _, raw := range append(existing, incoming...) {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return err
		}
		var id string
		if key != "events" {
			if err := json.Unmarshal(fields["id"], &id); err != nil && len(fields["id"]) != 0 {
				return err
			}
		}
		if id == "" {
			id = string(raw)
		}
		var at time.Time
		for _, field := range []string{"updated_at", "completed_at", "submitted_at", "created_at", "started_at"} {
			if len(fields[field]) == 0 {
				continue
			}
			var timestamp time.Time
			if err := json.Unmarshal(fields[field], &timestamp); err != nil {
				return err
			}
			if timestamp.After(at) {
				at = timestamp
			}
		}
		if key == "events" {
			entries = append(entries, entry{raw, at})
			continue
		}
		if index, found := indexes[id]; found {
			if at.After(entries[index].at) {
				entries[index] = entry{raw, at}
			}
		} else {
			indexes[id] = len(entries)
			entries = append(entries, entry{raw, at})
		}
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].at.Before(entries[j].at) })
	merged := make([]json.RawMessage, 0, len(entries))
	for _, entry := range entries {
		merged = append(merged, entry.raw)
	}
	return image.Put(key, merged)
}

// ReadLegacyEvents permits only an incomplete trailing append, not a corrupt
// complete event. Silently skipping a damaged delivery claim could replay a
// result after migration; such corruption must stop admission for repair.
func ReadLegacyEvents(path string) ([]json.RawMessage, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	lines := bytes.Split(raw, []byte{'\n'})
	events := make([]json.RawMessage, 0, len(lines))
	for index, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		if !json.Valid(line) {
			if index == len(lines)-1 && len(raw) > 0 && raw[len(raw)-1] != '\n' {
				break
			}
			return nil, fmt.Errorf("%w: invalid legacy event at %s:%d", ErrCorrupt, path, index+1)
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal(line, &object); err != nil || object == nil {
			return nil, fmt.Errorf("%w: invalid legacy event at %s:%d", ErrCorrupt, path, index+1)
		}
		events = append(events, append(json.RawMessage(nil), line...))
	}
	return events, nil
}
