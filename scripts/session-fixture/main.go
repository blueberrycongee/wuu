// session-fixture seeds and reads disposable E2E sessions through the selected
// production authority. It is a script helper, not a product RPC or plugin API.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
)

type fixtureSession struct {
	Session session.Session         `json:"session"`
	History []session.HistoryRecord `json:"history"`
}

type request struct {
	Home     string           `json:"home"`
	Workdir  string           `json:"workdir"`
	Op       string           `json:"op"`
	Sessions []fixtureSession `json:"sessions,omitempty"`
	IDs      []string         `json:"ids,omitempty"`
	Domains  []string         `json:"domains,omitempty"`
}

func main() {
	if err := run(context.Background(), os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "session-fixture:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, input io.Reader, output io.Writer) error {
	decoder := json.NewDecoder(input)
	decoder.DisallowUnknownFields()
	var req request
	if err := decoder.Decode(&req); err != nil {
		return fmt.Errorf("decode request: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("expected one JSON request")
	}
	if req.Op != "seed" && req.Op != "read" && req.Op != "inspect" {
		return fmt.Errorf("unsupported operation %q", req.Op)
	}
	if req.Op == "seed" {
		seen := make(map[string]bool)
		for _, fixture := range req.Sessions {
			id := strings.TrimSpace(fixture.Session.ID)
			if id == "" || id != fixture.Session.ID || seen[id] {
				return fmt.Errorf("seed requires unique nonempty session IDs: %q", fixture.Session.ID)
			}
			seen[id] = true
			for index, record := range fixture.History {
				if record.Seq != 0 && record.Seq != index+1 {
					return fmt.Errorf("session %q history seq must match array order at entry %d", id, index+1)
				}
			}
		}
	}
	home, workdir, err := fixturePaths(req.Home, req.Workdir)
	if err != nil {
		return err
	}
	// All discovery in this short-lived process belongs to the fixture. In
	// particular, an inherited WUU_HOME must never override the validated home.
	for key, value := range map[string]string{
		"WUU_HOME": home, "HOME": home, "USERPROFILE": home,
		"CODEX_HOME": filepath.Join(home, ".codex"), "GROK_HOME": filepath.Join(home, ".grok"),
		"XDG_CONFIG_HOME": filepath.Join(home, ".config"), "XDG_DATA_HOME": filepath.Join(home, ".local", "share"),
	} {
		if err := os.Setenv(key, value); err != nil {
			return err
		}
	}
	cfg, configPath, err := config.LoadPath(filepath.Join(home, "config.json"))
	if err != nil {
		return err
	}
	result, err := execute(ctx, req, runtime.StateOptions{RootDir: workdir, HomeDir: home, ConfigPath: configPath, ConfigLoadMode: runtime.ConfigLoadFile, Config: cfg})
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(result)
}

func execute(ctx context.Context, req request, opts runtime.StateOptions) (result any, err error) {
	state, err := runtime.OpenState(ctx, opts)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, state.Close()) }()
	if req.Op == "inspect" {
		return inspectDocuments(ctx, state.Documents, req.Domains)
	}
	if req.Op == "seed" {
		ids := make([]string, 0, len(req.Sessions))
		for _, fixture := range req.Sessions {
			fixture.Session.Entries = len(fixture.History)
			created, err := state.Sessions.CreateInitialized(fixture.Session, fixture.History)
			if err != nil {
				return nil, fmt.Errorf("seed %q: %w", fixture.Session.ID, err)
			}
			ids = append(ids, created.ID)
		}
		return struct {
			IDs []string `json:"ids"`
		}{ids}, nil
	}
	ids := req.IDs
	if ids == nil {
		all, err := state.Sessions.List(0)
		if err != nil {
			return nil, err
		}
		for _, metadata := range all {
			ids = append(ids, metadata.ID)
		}
	}
	fixtures := make([]fixtureSession, 0, len(ids))
	for _, id := range ids {
		metadata, found, err := state.Sessions.Find(id)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, fmt.Errorf("%w: %q", session.ErrSessionNotFound, id)
		}
		history, err := state.Sessions.LoadHistoryRecords(id, true)
		if err != nil {
			return nil, err
		}
		if history == nil {
			history = []session.HistoryRecord{}
		}
		fixtures = append(fixtures, fixtureSession{metadata, history})
	}
	return struct {
		Sessions []fixtureSession `json:"sessions"`
	}{fixtures}, nil
}

type inspection struct {
	Revision  persistence.Revision              `json:"revision,string"`
	Documents map[string][]persistence.Document `json:"documents"`
}

// Receipt and accounting projections omit fields needed by recovery E2Es. This
// test-only reader retains the selected provider's complete domain documents.
func inspectDocuments(ctx context.Context, documents persistence.Documents, domains []string) (inspection, error) {
	var result inspection
	if len(domains) == 0 {
		return result, errors.New("inspect requires explicit domains")
	}
	for _, domain := range domains {
		switch domain {
		case "held_user_work", "session_history_retractions", "inference_operations", "inference_submissions", "inference_workflows":
		default:
			return result, fmt.Errorf("unsupported inspection domain %q", domain)
		}
	}
	err := persistence.Update(ctx, documents, func(tx *persistence.Tx) error {
		// A concurrent commit restarts the whole read, not only its last page.
		result.Documents = make(map[string][]persistence.Document, len(domains))
		for _, domain := range domains {
			result.Documents[domain] = []persistence.Document{}
			query := persistence.Query{Namespace: "wuu." + domain + ".v1", Limit: 256}
			for {
				view, err := tx.ReadView(query)
				if err != nil {
					return err
				}
				result.Revision = view.Revision
				result.Documents[domain] = append(result.Documents[domain], view.Documents...)
				if view.NextCursor == "" {
					break
				}
				query.Cursor, query.AtRevision = view.NextCursor, &view.Revision
			}
		}
		return nil
	})
	return result, err
}

func fixturePaths(home, workdir string) (string, string, error) {
	if !filepath.IsAbs(home) || !filepath.IsAbs(workdir) {
		return "", "", errors.New("home and workdir must be explicit absolute paths")
	}
	resolvedHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		return "", "", err
	}
	resolvedWorkdir, err := filepath.EvalSymlinks(workdir)
	if err != nil {
		return "", "", err
	}
	account, err := user.Current()
	if err != nil {
		return "", "", fmt.Errorf("resolve account home: %w", err)
	}
	accountHome, err := filepath.EvalSymlinks(account.HomeDir)
	if err != nil {
		return "", "", err
	}
	userState := filepath.Join(accountHome, ".wuu")
	if resolvedState, err := filepath.EvalSymlinks(userState); err == nil {
		userState = resolvedState
	}
	if resolvedHome == accountHome || resolvedHome == userState || strings.HasPrefix(resolvedHome, userState+string(os.PathSeparator)) {
		return "", "", errors.New("refusing actual user home or Wuu state")
	}
	for _, path := range []string{resolvedHome, resolvedWorkdir} {
		info, err := os.Stat(path)
		if err != nil {
			return "", "", err
		}
		if !info.IsDir() {
			return "", "", fmt.Errorf("fixture path is not a directory: %s", path)
		}
	}
	for _, name := range []string{".wuu-session-fixture", "config.json"} {
		info, err := os.Lstat(filepath.Join(resolvedHome, name))
		if err != nil {
			return "", "", err
		}
		if !info.Mode().IsRegular() {
			return "", "", fmt.Errorf("fixture %s must be a regular file", name)
		}
	}
	marker, err := os.ReadFile(filepath.Join(resolvedHome, ".wuu-session-fixture"))
	if err != nil {
		return "", "", err
	}
	if string(marker) != "wuu-session-fixture-v1\n" {
		return "", "", errors.New("missing synthetic session fixture marker")
	}
	return resolvedHome, resolvedWorkdir, nil
}
