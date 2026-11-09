// Package pluginsettings persists values owned by installed plugins. The Go
// core owns this store so every shell observes the same user and workspace
// settings.
package pluginsettings

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/blueberrycongee/wuu/internal/securefs"
	"github.com/blueberrycongee/wuu/internal/statepath"
	"github.com/blueberrycongee/wuu/internal/storelock"
)

const (
	SchemaVersion     = 1
	maxSettings       = 256
	maxDocumentBytes  = 1 << 20
	settingsDirectory = "plugin-settings"
)

type Scope string

const (
	ScopeUser      Scope = "user"
	ScopeWorkspace Scope = "workspace"
)

type Document struct {
	SchemaVersion int                        `json:"schema_version"`
	PluginID      string                     `json:"plugin_id"`
	Fingerprint   string                     `json:"fingerprint,omitempty"`
	Values        map[string]json.RawMessage `json:"values"`
}

var safeNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func Read(wuuHome, workspaceRoot, pluginID string, scope Scope) (Document, error) {
	path, _, err := documentPath(wuuHome, workspaceRoot, pluginID, scope)
	if err != nil {
		return Document{}, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return emptyDocument(pluginID), nil
	}
	if err != nil {
		return Document{}, fmt.Errorf("read plugin settings: %w", err)
	}
	if len(data) > maxDocumentBytes {
		return Document{}, fmt.Errorf("plugin settings document exceeds %d bytes", maxDocumentBytes)
	}
	var document Document
	if err := json.Unmarshal(data, &document); err != nil {
		return Document{}, fmt.Errorf("decode plugin settings: %w", err)
	}
	if err := validateDocument(document, pluginID); err != nil {
		return Document{}, err
	}
	return cloneDocument(document), nil
}

// Update holds a cross-process lock for the complete read-modify-write
// transaction. The callback owns a private value map and may mutate it freely.
func Update(
	wuuHome, workspaceRoot, pluginID string,
	scope Scope,
	fingerprint string,
	update func(map[string]json.RawMessage) error,
) (Document, error) {
	if update == nil {
		return Document{}, errors.New("plugin settings update callback is required")
	}
	path, storeDir, err := documentPath(wuuHome, workspaceRoot, pluginID, scope)
	if err != nil {
		return Document{}, err
	}
	lock, err := storelock.Acquire(storeDir)
	if err != nil {
		return Document{}, fmt.Errorf("lock plugin settings: %w", err)
	}
	defer lock.Release() //nolint:errcheck -- the write result is authoritative

	document, err := readLocked(path, pluginID)
	if err != nil {
		return Document{}, err
	}
	values := cloneValues(document.Values)
	if err := update(values); err != nil {
		return Document{}, err
	}
	document = Document{
		SchemaVersion: SchemaVersion,
		PluginID:      pluginID,
		Fingerprint:   strings.TrimSpace(fingerprint),
		Values:        values,
	}
	if err := validateDocument(document, pluginID); err != nil {
		return Document{}, err
	}
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return Document{}, fmt.Errorf("encode plugin settings: %w", err)
	}
	data = append(data, '\n')
	if len(data) > maxDocumentBytes {
		return Document{}, fmt.Errorf("plugin settings document exceeds %d bytes", maxDocumentBytes)
	}
	if err := securefs.WriteFileAtomic(path, data); err != nil {
		return Document{}, fmt.Errorf("write plugin settings: %w", err)
	}
	return cloneDocument(document), nil
}

func Remove(wuuHome, workspaceRoot, pluginID string, scope Scope) error {
	path, storeDir, err := documentPath(wuuHome, workspaceRoot, pluginID, scope)
	if err != nil {
		return err
	}
	lock, err := storelock.Acquire(storeDir)
	if err != nil {
		return fmt.Errorf("lock plugin settings: %w", err)
	}
	defer lock.Release() //nolint:errcheck -- removal result is authoritative
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove plugin settings: %w", err)
	}
	return nil
}

func documentPath(wuuHome, workspaceRoot, pluginID string, scope Scope) (string, string, error) {
	pluginID = strings.TrimSpace(pluginID)
	if pluginID == "" || len(pluginID) > 256 {
		return "", "", fmt.Errorf("invalid plugin id %q", pluginID)
	}
	wuuHome = strings.TrimSpace(wuuHome)
	if wuuHome == "" {
		return "", "", errors.New("Wuu home is required")
	}
	var storeDir string
	switch scope {
	case ScopeUser:
		storeDir = filepath.Join(wuuHome, settingsDirectory, string(ScopeUser))
	case ScopeWorkspace:
		workspaceRoot = strings.TrimSpace(workspaceRoot)
		if workspaceRoot == "" {
			return "", "", errors.New("workspace root is required for workspace plugin settings")
		}
		workspaceDir, err := statepath.WorkspaceDir(wuuHome, workspaceRoot)
		if err != nil {
			return "", "", fmt.Errorf("resolve plugin settings workspace: %w", err)
		}
		storeDir = filepath.Join(workspaceDir, settingsDirectory)
	default:
		return "", "", fmt.Errorf("unsupported plugin settings scope %q", scope)
	}
	return filepath.Join(storeDir, pluginDocumentName(pluginID)), storeDir, nil
}

func pluginDocumentName(pluginID string) string {
	if safeNamePattern.MatchString(pluginID) {
		return pluginID + ".json"
	}
	return fmt.Sprintf("sha256-%x.json", sha256.Sum256([]byte(pluginID)))
}

func readLocked(path, pluginID string) (Document, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return emptyDocument(pluginID), nil
	}
	if err != nil {
		return Document{}, fmt.Errorf("read plugin settings: %w", err)
	}
	if len(data) > maxDocumentBytes {
		return Document{}, fmt.Errorf("plugin settings document exceeds %d bytes", maxDocumentBytes)
	}
	var document Document
	if err := json.Unmarshal(data, &document); err != nil {
		return Document{}, fmt.Errorf("decode plugin settings: %w", err)
	}
	if err := validateDocument(document, pluginID); err != nil {
		return Document{}, err
	}
	return document, nil
}

func validateDocument(document Document, expectedPluginID string) error {
	if document.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported plugin settings schema version %d", document.SchemaVersion)
	}
	if document.PluginID != expectedPluginID {
		return fmt.Errorf("plugin settings belong to %q, not %q", document.PluginID, expectedPluginID)
	}
	if len(document.Values) > maxSettings {
		return fmt.Errorf("plugin settings contain more than %d values", maxSettings)
	}
	for key, value := range document.Values {
		if !safeNamePattern.MatchString(key) {
			return fmt.Errorf("invalid plugin setting key %q", key)
		}
		if len(value) == 0 || !json.Valid(value) {
			return fmt.Errorf("plugin setting %q is not valid JSON", key)
		}
	}
	return nil
}

func emptyDocument(pluginID string) Document {
	return Document{SchemaVersion: SchemaVersion, PluginID: pluginID, Values: map[string]json.RawMessage{}}
}

func cloneDocument(document Document) Document {
	document.Values = cloneValues(document.Values)
	return document
}

func cloneValues(values map[string]json.RawMessage) map[string]json.RawMessage {
	cloned := make(map[string]json.RawMessage, len(values))
	for key, value := range values {
		cloned[key] = append(json.RawMessage(nil), value...)
	}
	return cloned
}
