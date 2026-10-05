package harness

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/securefs"
	"github.com/blueberrycongee/wuu/internal/storelock"
)

type Store struct {
	dir string
	mu  sync.Mutex
}

func NewStore(dir string) *Store {
	return &Store{dir: strings.TrimSpace(dir)}
}

func (s *Store) Dir() string {
	if s == nil {
		return ""
	}
	return s.dir
}

func (s *Store) UpsertTask(task Task) error {
	if s == nil || s.dir == "" {
		return nil
	}
	release, err := s.lockStore()
	if err != nil {
		return err
	}
	defer release()
	if err := s.ensureDirLocked(); err != nil {
		return err
	}
	tasks, err := s.loadTasks()
	if err != nil {
		return err
	}
	if task.CreatedAt.IsZero() {
		task.CreatedAt = time.Now().UTC()
	}
	if task.UpdatedAt.IsZero() {
		task.UpdatedAt = time.Now().UTC()
	}
	replaced := false
	for i := range tasks {
		if tasks[i].ID == task.ID {
			if task.CreatedAt.IsZero() {
				task.CreatedAt = tasks[i].CreatedAt
			}
			tasks[i] = task
			replaced = true
			break
		}
	}
	if !replaced {
		tasks = append(tasks, task)
	}
	sort.Slice(tasks, func(i, j int) bool {
		if tasks[i].Path == tasks[j].Path {
			return tasks[i].ID < tasks[j].ID
		}
		return tasks[i].Path < tasks[j].Path
	})
	return writeJSONFile(filepath.Join(s.dir, "tasks.json"), tasks)
}

func (s *Store) UpdateTaskStatus(taskID string, status TaskStatus, completedAt time.Time, inputTokens, outputTokens int, errText string) (Task, error) {
	if s == nil || s.dir == "" {
		return Task{}, nil
	}
	release, err := s.lockStore()
	if err != nil {
		return Task{}, err
	}
	defer release()
	if err := s.ensureDirLocked(); err != nil {
		return Task{}, err
	}
	tasks, err := s.loadTasks()
	if err != nil {
		return Task{}, err
	}
	for i := range tasks {
		if tasks[i].ID != taskID {
			continue
		}
		now := time.Now().UTC()
		tasks[i].Status = status
		tasks[i].UpdatedAt = now
		tasks[i].InputTokens = inputTokens
		tasks[i].OutputTokens = outputTokens
		tasks[i].Error = strings.TrimSpace(errText)
		if !completedAt.IsZero() {
			tasks[i].CompletedAt = completedAt
		}
		if err := writeJSONFile(filepath.Join(s.dir, "tasks.json"), tasks); err != nil {
			return Task{}, err
		}
		return tasks[i], nil
	}
	return Task{}, fmt.Errorf("task %q not found", taskID)
}

func (s *Store) UpsertRun(run AgentRun) error {
	if s == nil || s.dir == "" {
		return nil
	}
	release, err := s.lockStore()
	if err != nil {
		return err
	}
	defer release()
	if err := s.ensureDirLocked(); err != nil {
		return err
	}
	runs, err := s.loadRuns()
	if err != nil {
		return err
	}
	if run.StartedAt.IsZero() {
		run.StartedAt = time.Now().UTC()
	}
	replaced := false
	for i := range runs {
		if runs[i].ID == run.ID {
			runs[i] = run
			replaced = true
			break
		}
	}
	if !replaced {
		runs = append(runs, run)
	}
	sort.Slice(runs, func(i, j int) bool {
		if runs[i].TaskID == runs[j].TaskID {
			return runs[i].StartedAt.Before(runs[j].StartedAt)
		}
		return runs[i].TaskID < runs[j].TaskID
	})
	return writeJSONFile(filepath.Join(s.dir, "runs.json"), runs)
}

func (s *Store) UpdateRunStatus(runID string, status TaskStatus, completedAt time.Time, inputTokens, outputTokens int, errText string) (AgentRun, error) {
	if s == nil || s.dir == "" {
		return AgentRun{}, nil
	}
	release, err := s.lockStore()
	if err != nil {
		return AgentRun{}, err
	}
	defer release()
	if err := s.ensureDirLocked(); err != nil {
		return AgentRun{}, err
	}
	runs, err := s.loadRuns()
	if err != nil {
		return AgentRun{}, err
	}
	for i := range runs {
		if runs[i].ID != runID {
			continue
		}
		runs[i].Status = status
		runs[i].InputTokens = inputTokens
		runs[i].OutputTokens = outputTokens
		runs[i].Error = strings.TrimSpace(errText)
		if !completedAt.IsZero() {
			runs[i].CompletedAt = completedAt
		}
		if err := writeJSONFile(filepath.Join(s.dir, "runs.json"), runs); err != nil {
			return AgentRun{}, err
		}
		return runs[i], nil
	}
	return AgentRun{}, fmt.Errorf("run %q not found", runID)
}

func (s *Store) AddArtifact(artifact Artifact) error {
	if s == nil || s.dir == "" {
		return nil
	}
	release, err := s.lockStore()
	if err != nil {
		return err
	}
	defer release()
	if err := s.ensureDirLocked(); err != nil {
		return err
	}
	artifacts, err := s.loadArtifacts()
	if err != nil {
		return err
	}
	if artifact.CreatedAt.IsZero() {
		artifact.CreatedAt = time.Now().UTC()
	}
	if artifact.ID == "" {
		artifact.ID = artifactID(artifact)
	}
	replaced := false
	for i := range artifacts {
		if artifacts[i].ID == artifact.ID {
			artifacts[i] = artifact
			replaced = true
			break
		}
	}
	if !replaced {
		artifacts = append(artifacts, artifact)
	}
	sort.Slice(artifacts, func(i, j int) bool {
		if artifacts[i].TaskID == artifacts[j].TaskID {
			return artifacts[i].CreatedAt.Before(artifacts[j].CreatedAt)
		}
		return artifacts[i].TaskID < artifacts[j].TaskID
	})
	if err := writeJSONFile(filepath.Join(s.dir, "artifacts.json"), artifacts); err != nil {
		return err
	}
	tasks, err := s.loadTasks()
	if err != nil {
		return err
	}
	for i := range tasks {
		if tasks[i].ID != artifact.TaskID {
			continue
		}
		if artifact.Path != "" && !contains(tasks[i].ArtifactPaths, artifact.Path) {
			tasks[i].ArtifactPaths = append(tasks[i].ArtifactPaths, artifact.Path)
		}
		if artifact.Kind == ArtifactReport {
			tasks[i].ReportPath = artifact.Path
		}
		tasks[i].UpdatedAt = time.Now().UTC()
		if err := writeJSONFile(filepath.Join(s.dir, "tasks.json"), tasks); err != nil {
			return err
		}
		break
	}
	return s.appendEventLocked(Event{
		Type:      EventArtifactRecorded,
		TaskID:    artifact.TaskID,
		RunID:     artifact.RunID,
		Artifact:  artifact.Path,
		CreatedAt: time.Now().UTC(),
	})
}

func (s *Store) SubmitReport(report Report) (Report, error) {
	if s == nil || s.dir == "" {
		return report, nil
	}
	release, err := s.lockStore()
	if err != nil {
		return Report{}, err
	}
	defer release()
	if err := s.ensureDirLocked(); err != nil {
		return Report{}, err
	}
	now := time.Now().UTC()
	if report.SubmittedAt.IsZero() {
		report.SubmittedAt = now
	}
	if report.ID == "" {
		report.ID = report.TaskID + "-report"
	}
	if report.ReportPath == "" {
		report.ReportPath = filepath.Join(s.dir, "reports", report.TaskID+".md")
	}
	reports, err := s.loadReports()
	if err != nil {
		return Report{}, err
	}
	report.Kind = NormalizeReportKind(report.Kind)
	// A synthesized final_text report is a stand-in, never a competitor: it
	// is only stored while the task has no report at all, and a structured
	// agent_report submission supersedes any stand-in already recorded.
	// This keeps concurrent completion-time synthesis and tool-time
	// structured submissions deterministic regardless of arrival order.
	var supersededReportIDs []string
	if report.Kind == ReportKindFinalText {
		for i := range reports {
			if reports[i].TaskID == report.TaskID {
				return reports[i], nil
			}
		}
	} else {
		kept := reports[:0]
		for _, existing := range reports {
			if existing.TaskID == report.TaskID && NormalizeReportKind(existing.Kind) == ReportKindFinalText {
				supersededReportIDs = append(supersededReportIDs, existing.ID)
				continue
			}
			kept = append(kept, existing)
		}
		reports = kept
	}
	// The markdown render happens only after the kind rules above decided
	// this submission actually lands, so a rejected stand-in can never
	// clobber a structured report's file at the shared default path.
	if err := os.MkdirAll(filepath.Dir(report.ReportPath), securefs.DirMode); err != nil {
		return Report{}, fmt.Errorf("create report dir: %w", err)
	}
	if err := os.WriteFile(report.ReportPath, []byte(renderReportMarkdown(report)), securefs.FileMode); err != nil {
		return Report{}, fmt.Errorf("write report: %w", err)
	}
	replaced := false
	for i := range reports {
		if reports[i].ID == report.ID {
			reports[i] = report
			replaced = true
			break
		}
	}
	if !replaced {
		reports = append(reports, report)
	}
	sort.Slice(reports, func(i, j int) bool {
		if reports[i].TaskID == reports[j].TaskID {
			return reports[i].SubmittedAt.Before(reports[j].SubmittedAt)
		}
		return reports[i].TaskID < reports[j].TaskID
	})
	if err := writeJSONFile(filepath.Join(s.dir, "reports.json"), reports); err != nil {
		return Report{}, err
	}
	artifact := Artifact{
		ID:        report.ID + "-artifact",
		TaskID:    report.TaskID,
		RunID:     report.RunID,
		Kind:      ArtifactReport,
		Path:      report.ReportPath,
		Summary:   report.Summary,
		CreatedAt: report.SubmittedAt,
	}
	artifacts, err := s.loadArtifacts()
	if err != nil {
		return Report{}, err
	}
	// A superseded stand-in report takes its artifact entry with it, so the
	// artifact list mirrors the surviving report exactly.
	if len(supersededReportIDs) > 0 {
		stale := make(map[string]struct{}, len(supersededReportIDs))
		for _, id := range supersededReportIDs {
			stale[id+"-artifact"] = struct{}{}
		}
		keptArtifacts := artifacts[:0]
		for _, existing := range artifacts {
			if _, gone := stale[existing.ID]; gone {
				continue
			}
			keptArtifacts = append(keptArtifacts, existing)
		}
		artifacts = keptArtifacts
	}
	replacedArtifact := false
	for i := range artifacts {
		if artifacts[i].ID == artifact.ID {
			artifacts[i] = artifact
			replacedArtifact = true
			break
		}
	}
	if !replacedArtifact {
		artifacts = append(artifacts, artifact)
	}
	if err := writeJSONFile(filepath.Join(s.dir, "artifacts.json"), artifacts); err != nil {
		return Report{}, err
	}
	tasks, err := s.loadTasks()
	if err != nil {
		return Report{}, err
	}
	for i := range tasks {
		if tasks[i].ID != report.TaskID {
			continue
		}
		tasks[i].ReportPath = report.ReportPath
		if !contains(tasks[i].ArtifactPaths, report.ReportPath) {
			tasks[i].ArtifactPaths = append(tasks[i].ArtifactPaths, report.ReportPath)
		}
		tasks[i].UpdatedAt = now
		if err := writeJSONFile(filepath.Join(s.dir, "tasks.json"), tasks); err != nil {
			return Report{}, err
		}
		break
	}
	if err := s.appendEventLocked(Event{
		Type:      EventReportSubmitted,
		TaskID:    report.TaskID,
		RunID:     report.RunID,
		AgentID:   report.AgentID,
		Path:      report.AgentPath,
		Status:    report.Outcome,
		Artifact:  report.ReportPath,
		CreatedAt: report.SubmittedAt,
	}); err != nil {
		return Report{}, err
	}
	return report, nil
}

func (s *Store) AppendEvent(event Event) error {
	if s == nil || s.dir == "" {
		return nil
	}
	release, err := s.lockStore()
	if err != nil {
		return err
	}
	defer release()
	if err := s.ensureDirLocked(); err != nil {
		return err
	}
	return s.appendEventLocked(event)
}

func (s *Store) UpsertQueueItem(item QueueItem) error {
	if s == nil || s.dir == "" {
		return nil
	}
	release, err := s.lockStore()
	if err != nil {
		return err
	}
	defer release()
	if err := s.ensureDirLocked(); err != nil {
		return err
	}
	items, err := s.loadQueue()
	if err != nil {
		return err
	}
	if item.ID == "" {
		item.ID = item.TaskID
	}
	now := time.Now().UTC()
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = now
	}
	replaced := false
	for i := range items {
		if items[i].ID == item.ID {
			item.CreatedAt = items[i].CreatedAt
			items[i] = item
			replaced = true
			break
		}
	}
	if !replaced {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	return writeJSONFile(filepath.Join(s.dir, "queue.json"), items)
}

func (s *Store) DeleteQueueItem(id string) error {
	_, err := s.ClaimQueueItem(id)
	return err
}

func (s *Store) ClaimQueueItem(id string) (bool, error) {
	if s == nil || s.dir == "" {
		return false, nil
	}
	release, err := s.lockStore()
	if err != nil {
		return false, err
	}
	defer release()
	if err := s.ensureDirLocked(); err != nil {
		return false, err
	}
	items, err := s.loadQueue()
	if err != nil {
		return false, err
	}
	out := items[:0]
	found := false
	for _, item := range items {
		if item.ID == id {
			found = true
			continue
		}
		out = append(out, item)
	}
	if !found {
		return false, nil
	}
	if err := writeJSONFile(filepath.Join(s.dir, "queue.json"), out); err != nil {
		return false, err
	}
	return true, nil
}

// MarkQueueItemCancelling durably converts launch intent into a cancellation
// tombstone. Recovery must finish compensation and settlement for such an
// item instead of executing its payload.
func (s *Store) MarkQueueItemCancelling(id string) (bool, error) {
	return s.markQueueItemState(id, QueueItemStateCancelling, "")
}

// MarkQueueItemFailing durably records a launch failure before compensation.
// Recovery must settle this terminal intent instead of launching the payload.
func (s *Store) MarkQueueItemFailing(id, errText string) (bool, error) {
	return s.markQueueItemState(id, QueueItemStateFailing, errText)
}

func (s *Store) markQueueItemState(id string, state QueueItemState, errText string) (bool, error) {
	if s == nil || s.dir == "" {
		return true, nil
	}
	release, err := s.lockStore()
	if err != nil {
		return false, err
	}
	defer release()
	if err := s.ensureDirLocked(); err != nil {
		return false, err
	}
	items, err := s.loadQueue()
	if err != nil {
		return false, err
	}
	for i := range items {
		if items[i].ID != id {
			continue
		}
		if items[i].State == state && items[i].Error == strings.TrimSpace(errText) {
			return true, nil
		}
		items[i].State = state
		items[i].Error = strings.TrimSpace(errText)
		items[i].UpdatedAt = time.Now().UTC()
		if err := writeJSONFile(filepath.Join(s.dir, "queue.json"), items); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}

// GetQueueItem reads a durable queue item without consuming it.
func (s *Store) GetQueueItem(id string) (QueueItem, bool, error) {
	if s == nil || s.dir == "" {
		return QueueItem{}, false, nil
	}
	items, err := s.loadQueue()
	if err != nil {
		return QueueItem{}, false, err
	}
	for _, item := range items {
		if item.ID == id {
			return item, true, nil
		}
	}
	return QueueItem{}, false, nil
}

// QueueItemExists checks whether a durable queue payload or cancellation
// tombstone is present without consuming it.
func (s *Store) QueueItemExists(id string) (bool, error) {
	_, exists, err := s.GetQueueItem(id)
	return exists, err
}

func (s *Store) ListQueueItems() ([]QueueItem, error) {
	if s == nil || s.dir == "" {
		return nil, nil
	}
	return s.loadQueue()
}

func (s *Store) ListTasks() ([]Task, error) {
	if s == nil || s.dir == "" {
		return nil, nil
	}
	return s.loadTasks()
}

func (s *Store) ListRuns() ([]AgentRun, error) {
	if s == nil || s.dir == "" {
		return nil, nil
	}
	return s.loadRuns()
}

func (s *Store) ListArtifacts() ([]Artifact, error) {
	if s == nil || s.dir == "" {
		return nil, nil
	}
	return s.loadArtifacts()
}

func (s *Store) ListReports() ([]Report, error) {
	if s == nil || s.dir == "" {
		return nil, nil
	}
	return s.loadReports()
}

func (s *Store) ReportForTask(taskID string) (Report, bool, error) {
	reports, err := s.ListReports()
	if err != nil {
		return Report{}, false, err
	}
	for i := len(reports) - 1; i >= 0; i-- {
		if reports[i].TaskID == taskID {
			return reports[i], true, nil
		}
	}
	return Report{}, false, nil
}

func (s *Store) ReadEvents() ([]Event, error) {
	if s == nil || s.dir == "" {
		return nil, nil
	}
	path := filepath.Join(s.dir, "events.jsonl")
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open harness events: %w", err)
	}
	defer file.Close()
	var events []Event
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event Event
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan harness events: %w", err)
	}
	return events, nil
}

// lockStore serializes durable read-modify-write transactions, in-process and
// across processes. Pure read methods must not take it: every JSON index is
// replaced via atomic rename and the event log is append-only with a scanner
// that skips an incomplete tail line, so direct reads always observe a
// complete snapshot. Reads also must not block on s.mu, because a writer
// waiting on the cross-process lock holds s.mu for the whole wait and would
// stall every read behind a wedged transaction in another process.
func (s *Store) lockStore() (func(), error) {
	s.mu.Lock()
	durable, err := storelock.Acquire(s.dir)
	if err != nil {
		s.mu.Unlock()
		return nil, fmt.Errorf("acquire harness store lock: %w", err)
	}
	return func() {
		_ = durable.Release()
		s.mu.Unlock()
	}, nil
}

func (s *Store) ensureDirLocked() error {
	if err := os.MkdirAll(s.dir, securefs.DirMode); err != nil {
		return fmt.Errorf("create harness store: %w", err)
	}
	return nil
}

// The load helpers read one index file each. They are safe without the store
// lock because writers replace each index atomically; write transactions call
// them under lockStore for read-modify-write atomicity.
func (s *Store) loadTasks() ([]Task, error) {
	var out []Task
	if err := readJSONFile(filepath.Join(s.dir, "tasks.json"), &out); err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Status = NormalizeTaskStatus(out[i].Status)
	}
	return out, nil
}

func (s *Store) loadRuns() ([]AgentRun, error) {
	var out []AgentRun
	if err := readJSONFile(filepath.Join(s.dir, "runs.json"), &out); err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Status = NormalizeTaskStatus(out[i].Status)
	}
	return out, nil
}

func (s *Store) loadArtifacts() ([]Artifact, error) {
	var out []Artifact
	if err := readJSONFile(filepath.Join(s.dir, "artifacts.json"), &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) loadReports() ([]Report, error) {
	var out []Report
	if err := readJSONFile(filepath.Join(s.dir, "reports.json"), &out); err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Kind = NormalizeReportKind(out[i].Kind)
	}
	return out, nil
}

func (s *Store) loadQueue() ([]QueueItem, error) {
	var out []QueueItem
	if err := readJSONFile(filepath.Join(s.dir, "queue.json"), &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) appendEventLocked(event Event) error {
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	path := filepath.Join(s.dir, "events.jsonl")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, securefs.FileMode)
	if err != nil {
		return fmt.Errorf("open harness events: %w", err)
	}
	defer file.Close()
	if err := json.NewEncoder(file).Encode(event); err != nil {
		return fmt.Errorf("write harness event: %w", err)
	}
	return nil
}

func readJSONFile(path string, out any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode %s: %w", filepath.Base(path), err)
	}
	return nil
}

func writeJSONFile(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), securefs.DirMode); err != nil {
		return fmt.Errorf("create %s dir: %w", filepath.Base(path), err)
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", filepath.Base(path), err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("create %s tmp: %w", filepath.Base(path), err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("write %s tmp: %w", filepath.Base(path), err)
	}
	if _, err := tmp.Write([]byte("\n")); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("write %s newline: %w", filepath.Base(path), err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("close %s tmp: %w", filepath.Base(path), err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("rename %s: %w", filepath.Base(path), err)
	}
	return nil
}

func renderReportMarkdown(report Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Agent Report\n\n")
	fmt.Fprintf(&b, "- Task: %s\n", report.TaskID)
	if report.AgentPath != "" {
		fmt.Fprintf(&b, "- Agent: %s\n", report.AgentPath)
	}
	fmt.Fprintf(&b, "- Outcome: %s\n", strings.TrimSpace(report.Outcome))
	if !report.SubmittedAt.IsZero() {
		fmt.Fprintf(&b, "- Submitted: %s\n", report.SubmittedAt.Format(time.RFC3339))
	}
	if strings.TrimSpace(report.Summary) != "" {
		fmt.Fprintf(&b, "\n## Summary\n\n%s\n", strings.TrimSpace(report.Summary))
	}
	writeList := func(title string, values []string) {
		if len(values) == 0 {
			return
		}
		fmt.Fprintf(&b, "\n## %s\n\n", title)
		for _, value := range values {
			if strings.TrimSpace(value) == "" {
				continue
			}
			fmt.Fprintf(&b, "- %s\n", strings.TrimSpace(value))
		}
	}
	writeList("Changed Files", report.ChangedFiles)
	writeList("Work Done", report.WorkDone)
	writeList("Blockers", report.Blockers)
	writeList("Risks", report.Risks)
	writeList("Verification", report.Verification)
	writeList("Next Steps", report.NextSteps)
	if len(report.Evidence) > 0 {
		fmt.Fprintf(&b, "\n## Evidence\n\n")
		for _, ev := range report.Evidence {
			fmt.Fprintf(&b, "- %s\n", formatEvidence(ev))
		}
	}
	writeList("Artifacts", report.Artifacts)
	if strings.TrimSpace(report.RawResult) != "" {
		fmt.Fprintf(&b, "\n## Raw Result\n\n```text\n%s\n```\n", strings.TrimSpace(report.RawResult))
	}
	return b.String()
}

func formatEvidence(ev EvidenceRef) string {
	parts := make([]string, 0, 4)
	if ev.Type != "" {
		parts = append(parts, ev.Type)
	}
	if ev.Path != "" {
		path := ev.Path
		if ev.Line > 0 {
			path = fmt.Sprintf("%s:%d", path, ev.Line)
		}
		parts = append(parts, path)
	}
	if ev.Command != "" {
		parts = append(parts, "`"+ev.Command+"`")
	}
	if ev.Note != "" {
		parts = append(parts, ev.Note)
	}
	if ev.Output != "" {
		parts = append(parts, ev.Output)
	}
	return strings.Join(parts, " - ")
}

func artifactID(artifact Artifact) string {
	kind := strings.TrimSpace(string(artifact.Kind))
	if kind == "" {
		kind = "artifact"
	}
	taskID := strings.TrimSpace(artifact.TaskID)
	if taskID == "" {
		taskID = "task"
	}
	return taskID + "-" + kind
}

func contains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}
