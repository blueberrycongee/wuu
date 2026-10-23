package execution

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/blueberrycongee/wuu/internal/persistence"
)

// NewLegacyConverter collects the two execution tables from one stopped-writer
// snapshot. An empty table name finalizes their ownership indexes atomically.
func NewLegacyConverter() func(string, []map[string]any) ([]persistence.Write, bool, error) {
	tables := make(map[string][]map[string]any)
	return func(table string, rows []map[string]any) ([]persistence.Write, bool, error) {
		if table != "" {
			if table != "execution_runs" && table != "execution_run_turns" {
				return nil, false, nil
			}
			tables[table] = rows
			return nil, true, nil
		}
		runs := make(map[string]Run)
		for _, row := range tables["execution_runs"] {
			var stored struct {
				ID            string
				RuntimeID     string `json:"runtime_id"`
				Status        Status
				Request       string `json:"request_json"`
				Runtime       string `json:"runtime_json"`
				Result        string `json:"result_json"`
				Error         string `json:"error_json"`
				ThreadID      string `json:"thread_id"`
				WorkspaceID   string `json:"workspace_id"`
				WorkspaceRoot string `json:"workspace_root"`
				CreatedAt     int64  `json:"created_at"`
				StartedAt     int64  `json:"started_at"`
				UpdatedAt     int64  `json:"updated_at"`
				CompletedAt   int64  `json:"completed_at"`
			}
			raw, err := json.Marshal(row)
			if err != nil {
				return nil, true, err
			}
			if err := json.Unmarshal(raw, &stored); err != nil {
				return nil, true, err
			}
			if stored.ID == "" || stored.ThreadID == "" || stored.RuntimeID == "" {
				return nil, true, fmt.Errorf("%w: legacy execution identity is missing", persistence.ErrCorrupt)
			}
			if _, ok := runs[stored.ID]; ok {
				return nil, true, fmt.Errorf("%w: duplicate legacy run %q", persistence.ErrCorrupt, stored.ID)
			}
			run := Run{ID: stored.ID, RuntimeID: stored.RuntimeID, Status: stored.Status, ThreadID: stored.ThreadID, Workspace: WorkspaceRef{ID: stored.WorkspaceID, Root: stored.WorkspaceRoot}, CreatedAt: fromMillis(stored.CreatedAt), UpdatedAt: fromMillis(stored.UpdatedAt), StartedAt: optionalTime(stored.StartedAt), CompletedAt: optionalTime(stored.CompletedAt), Turns: []TurnRef{}}
			if !run.Status.Terminal() && run.Status != StatusAccepted && run.Status != StatusRunning {
				return nil, true, fmt.Errorf("%w: invalid legacy execution status %q", persistence.ErrCorrupt, run.Status)
			}
			for _, field := range []struct {
				raw   string
				value any
			}{{stored.Request, &run.Request}, {stored.Runtime, &run.Runtime}, {stored.Result, &run.Result}, {stored.Error, &run.Error}} {
				if field.raw != "" {
					if err := json.Unmarshal([]byte(field.raw), field.value); err != nil {
						return nil, true, fmt.Errorf("decode legacy run %q: %w", run.ID, err)
					}
				}
			}
			runs[run.ID] = run
		}
		owners := make(map[string]string)
		for _, row := range tables["execution_run_turns"] {
			var stored struct {
				RunID      string `json:"run_id"`
				ThreadID   string `json:"thread_id"`
				TurnID     string `json:"turn_id"`
				Ordinal    int
				TracePath  string `json:"trace_path"`
				AttachedAt int64  `json:"attached_at"`
			}
			raw, err := json.Marshal(row)
			if err != nil {
				return nil, true, err
			}
			if err := json.Unmarshal(raw, &stored); err != nil {
				return nil, true, err
			}
			run, ok := runs[stored.RunID]
			if !ok || stored.TurnID == "" || stored.ThreadID != run.ThreadID || stored.Ordinal < 1 {
				return nil, true, fmt.Errorf("%w: legacy turn has no matching run", persistence.ErrCorrupt)
			}
			key := base64.RawURLEncoding.EncodeToString([]byte(stored.ThreadID)) + "/" + base64.RawURLEncoding.EncodeToString([]byte(stored.TurnID))
			if _, exists := owners[key]; exists {
				return nil, true, fmt.Errorf("%w: duplicate legacy turn ownership", persistence.ErrCorrupt)
			}
			owners[key] = run.ID
			for _, turn := range run.Turns {
				if turn.Ordinal == stored.Ordinal {
					return nil, true, fmt.Errorf("%w: duplicate legacy turn ordinal", persistence.ErrCorrupt)
				}
			}
			run.Turns = append(run.Turns, TurnRef{ThreadID: stored.ThreadID, TurnID: stored.TurnID, Ordinal: stored.Ordinal, TracePath: stored.TracePath, AttachedAt: fromMillis(stored.AttachedAt)})
			runs[run.ID] = run
		}
		var writes []persistence.Write
		add := func(namespace, key string, value any) error {
			raw, err := json.Marshal(value)
			if err != nil {
				return err
			}
			writes = append(writes, persistence.Write{Namespace: namespace, Key: key, Value: raw})
			return nil
		}
		active := make(map[string]string)
		for _, run := range runs {
			slices.SortFunc(run.Turns, func(a, b TurnRef) int { return a.Ordinal - b.Ordinal })
			if err := add(runsNamespace, run.ID, run); err != nil {
				return nil, true, err
			}
			if !run.Status.Terminal() {
				if _, ok := active[run.ThreadID]; ok {
					return nil, true, fmt.Errorf("%w: multiple active legacy runs for thread %q", persistence.ErrCorrupt, run.ThreadID)
				}
				active[run.ThreadID] = run.ID
			}
		}
		for key, id := range active {
			if err := add(activeThreadsNamespace, key, id); err != nil {
				return nil, true, err
			}
		}
		for key, id := range owners {
			if err := add(turnOwnersNamespace, key, id); err != nil {
				return nil, true, err
			}
		}
		slices.SortFunc(writes, func(a, b persistence.Write) int {
			if n := strings.Compare(a.Namespace, b.Namespace); n != 0 {
				return n
			}
			return strings.Compare(a.Key, b.Key)
		})
		return writes, true, nil
	}
}
