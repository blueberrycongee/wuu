package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	DefaultExperimentConcurrency = 2
	MaxExperimentConcurrency     = 8
	DefaultExperimentTimeout     = time.Minute
	MaxExperimentTimeout         = 30 * time.Minute
	MaxExperimentCandidates      = 32
	DefaultExperimentOutputBytes = 64 << 10
	MaxExperimentOutputBytes     = 1 << 20
	// A runtime helper can close MCP transports (5s) before its host (3s).
	experimentCancellationGrace = 10 * time.Second
)

// ExperimentEvaluationRequest runs an explicit command against each archived
// candidate. An empty Workdir gives each run a fresh scratch directory. An
// explicit Workdir remains writable by every run; it is never copied implicitly.
// Command arguments and output are retained as evidence and must not contain
// credentials. Workspace, when supplied, restricts candidates to that workspace.
type ExperimentEvaluationRequest struct {
	ID             string
	CandidateIDs   []string
	Command        []string
	Workspace      string
	Workdir        string
	Concurrency    int
	Timeout        time.Duration
	MaxOutputBytes int64
}

// ExperimentEvaluationReport records command outcomes, not comparative quality.
// The isolated homes contain no copied production settings or credentials.
// This is state separation, not a filesystem or network security sandbox.
type ExperimentEvaluationReport struct {
	EvaluationID        string                `json:"evaluation_id"`
	ReportPath          string                `json:"report_path"`
	ID                  string                `json:"id"`
	Command             []string              `json:"command"`
	Concurrency         int                   `json:"concurrency"`
	TimeoutMS           int64                 `json:"timeout_ms"`
	CancellationGraceMS int64                 `json:"cancellation_grace_ms"`
	OutputLimit         int64                 `json:"output_limit_bytes"`
	StartedAt           time.Time             `json:"started_at"`
	FinishedAt          time.Time             `json:"finished_at"`
	DurationMS          int64                 `json:"duration_ms"`
	Isolation           string                `json:"isolation"`
	Interpretation      string                `json:"interpretation"`
	Runs                []ExperimentRunReport `json:"runs"`
}

type ExperimentRunReport struct {
	RunID           string             `json:"run_id"`
	CandidateID     string             `json:"candidate_id"`
	Fingerprint     string             `json:"fingerprint"`
	SourceSHA256    string             `json:"source_sha256"`
	BaseFingerprint string             `json:"base_fingerprint"`
	WuuHome         string             `json:"wuu_home"`
	Workdir         string             `json:"workdir"`
	StartedAt       time.Time          `json:"started_at"`
	FinishedAt      time.Time          `json:"finished_at"`
	DurationMS      int64              `json:"duration_ms"`
	Status          string             `json:"status"`
	ExitCode        *int               `json:"exit_code"`
	Error           string             `json:"error,omitempty"`
	Stdout          ExperimentArtifact `json:"stdout"`
	Stderr          ExperimentArtifact `json:"stderr"`
}

type ExperimentArtifact struct {
	Path      string `json:"path"`
	Bytes     int64  `json:"bytes"`
	Truncated bool   `json:"truncated"`
}

// EvaluateExperiments persists a report even when commands fail, time out, or
// are cancelled. Those outcomes are in Runs; errors indicate invalid input or
// an inability to save evidence. Cancellation permits up to ten seconds of
// graceful cleanup before force-kill; duration includes that cleanup. It never
// changes the process-wide environment.
func EvaluateExperiments(ctx context.Context, home string, request ExperimentEvaluationRequest) (ExperimentEvaluationReport, error) {
	var report ExperimentEvaluationReport
	if !experimentProcessGroupsSupported {
		return report, errors.New("experiment evaluation currently supports Linux and macOS process-group lifecycle; Windows evaluation is unavailable")
	}
	if strings.TrimSpace(home) == "" {
		return report, errors.New("Wuu home is required")
	}
	if len(request.CandidateIDs) == 0 || len(request.CandidateIDs) > MaxExperimentCandidates {
		return report, fmt.Errorf("evaluation requires 1 to %d candidates", MaxExperimentCandidates)
	}
	if len(request.Command) == 0 || strings.TrimSpace(request.Command[0]) == "" || len(request.Command) > 256 {
		return report, errors.New("evaluation requires an explicit command with at most 256 arguments")
	}
	commandBytes := 0
	for _, arg := range request.Command {
		commandBytes += len(arg)
		if strings.ContainsRune(arg, '\x00') {
			return report, errors.New("evaluation command contains a NUL byte")
		}
	}
	if commandBytes > 16<<10 {
		return report, errors.New("evaluation command exceeds 16 KiB")
	}
	if request.Concurrency == 0 {
		request.Concurrency = DefaultExperimentConcurrency
	}
	if request.Concurrency < 1 || request.Concurrency > MaxExperimentConcurrency {
		return report, fmt.Errorf("concurrency must be between 1 and %d", MaxExperimentConcurrency)
	}
	if request.Timeout == 0 {
		request.Timeout = DefaultExperimentTimeout
	}
	if request.Timeout < 0 || request.Timeout > MaxExperimentTimeout {
		return report, fmt.Errorf("timeout must be positive and no greater than %s", MaxExperimentTimeout)
	}
	if request.MaxOutputBytes == 0 {
		request.MaxOutputBytes = DefaultExperimentOutputBytes
	}
	if request.MaxOutputBytes < 1 || request.MaxOutputBytes > MaxExperimentOutputBytes {
		return report, fmt.Errorf("output limit must be between 1 and %d bytes per stream", MaxExperimentOutputBytes)
	}
	var err error
	home, err = filepath.Abs(home)
	if err != nil {
		return report, err
	}
	if request.Workdir != "" {
		request.Workdir, err = filepath.Abs(request.Workdir)
		if err != nil {
			return report, fmt.Errorf("resolve evaluation workdir: %w", err)
		}
		info, err := os.Stat(request.Workdir)
		if err != nil {
			return report, fmt.Errorf("inspect evaluation workdir: %w", err)
		}
		if !info.IsDir() {
			return report, errors.New("evaluation workdir must be a directory")
		}
	}
	if request.Workspace != "" {
		request.Workspace, err = filepath.Abs(request.Workspace)
		if err != nil {
			return report, err
		}
		request.Workspace, err = filepath.EvalSymlinks(request.Workspace)
		if err != nil {
			return report, fmt.Errorf("resolve evaluation workspace: %w", err)
		}
	}
	candidates := make([]ExperimentCandidate, len(request.CandidateIDs))
	for i, id := range request.CandidateIDs {
		candidates[i], err = ReadExperimentCandidate(home, request.ID, id)
		if err != nil {
			return report, err
		}
		if request.Workspace != "" && (candidates[i].Workspace != request.Workspace || !pathContains(request.Workspace, candidates[i].SourcePath)) {
			return report, fmt.Errorf("candidate %s does not belong to evaluation workspace", id)
		}
	}
	root := filepath.Join(home, "experiments", "runs")
	if err := rejectExperimentSymlinks(home, root); err != nil {
		return report, err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return report, fmt.Errorf("create evaluation report directory: %w", err)
	}
	if err := rejectExperimentSymlinks(home, root); err != nil {
		return report, err
	}
	directory, err := os.MkdirTemp(root, "evaluation-")
	if err != nil {
		return report, fmt.Errorf("create evaluation: %w", err)
	}
	started := time.Now()
	report = ExperimentEvaluationReport{
		EvaluationID: filepath.Base(directory), ReportPath: filepath.Join(directory, "report.json"),
		ID: request.ID, Command: append([]string(nil), request.Command...),
		Concurrency: request.Concurrency, TimeoutMS: request.Timeout.Milliseconds(), OutputLimit: request.MaxOutputBytes,
		CancellationGraceMS: experimentCancellationGrace.Milliseconds(),
		StartedAt:           started.UTC(), Runs: make([]ExperimentRunReport, len(candidates)),
		Isolation:      "Fresh WUU_HOME, HOME, temporary directories, and default scratch cwd; no copied production configuration or inherited credentials. Not a filesystem or network sandbox. Explicit workdirs remain writable.",
		Interpretation: "Exit code zero means only that the requested command completed successfully. This report does not establish candidate quality or authorize promotion. Arguments and command output are retained; do not include secrets.",
	}
	jobs := make(chan int)
	var workers sync.WaitGroup
	for range min(request.Concurrency, len(candidates)) {
		workers.Go(func() {
			for index := range jobs {
				report.Runs[index] = evaluateExperimentRun(ctx, home, directory, index, candidates[index], request)
			}
		})
	}
	for index := range candidates {
		jobs <- index
	}
	close(jobs)
	workers.Wait()
	report.FinishedAt = time.Now().UTC()
	report.DurationMS = time.Since(started).Milliseconds()
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return report, fmt.Errorf("encode evaluation report: %w", err)
	}
	temporary := report.ReportPath + ".tmp"
	if err := os.WriteFile(temporary, append(data, '\n'), 0o600); err != nil {
		return report, fmt.Errorf("write evaluation report: %w", err)
	}
	if err := os.Rename(temporary, report.ReportPath); err != nil {
		return report, fmt.Errorf("publish evaluation report: %w", err)
	}
	return report, nil
}

func evaluateExperimentRun(ctx context.Context, home, directory string, index int, candidate ExperimentCandidate, request ExperimentEvaluationRequest) (report ExperimentRunReport) {
	started := time.Now()
	runID := fmt.Sprintf("run-%03d", index+1)
	root := filepath.Join(directory, runID)
	report = ExperimentRunReport{
		RunID: runID, CandidateID: candidate.CandidateID, Fingerprint: candidate.Fingerprint,
		SourceSHA256: candidate.SourceSHA256, BaseFingerprint: candidate.BaseFingerprint,
		WuuHome: filepath.Join(root, "home"), Workdir: request.Workdir,
		StartedAt: started.UTC(), Status: "setup_failed",
		Stdout: ExperimentArtifact{Path: filepath.Join(root, "stdout.log")},
		Stderr: ExperimentArtifact{Path: filepath.Join(root, "stderr.log")},
	}
	defer func() {
		report.FinishedAt = time.Now().UTC()
		report.DurationMS = time.Since(started).Milliseconds()
	}()
	if ctx.Err() != nil {
		report.Status, report.Error = "cancelled", ctx.Err().Error()
		return report
	}
	if report.Workdir == "" {
		report.Workdir = filepath.Join(root, "workspace")
	}
	personalHome := filepath.Join(root, "user-home")
	temporary := filepath.Join(root, "tmp")
	for _, path := range []string{root, report.Workdir, personalHome, temporary} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			report.Error = err.Error()
			return report
		}
	}
	prepared, err := PrepareExperimentRun(home, candidate.ID, candidate.CandidateID, report.WuuHome)
	if err != nil {
		report.Error = err.Error()
		return report
	}
	stdout, err := os.OpenFile(report.Stdout.Path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		report.Error = err.Error()
		return report
	}
	defer stdout.Close()
	stderr, err := os.OpenFile(report.Stderr.Path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		report.Error = err.Error()
		return report
	}
	defer stderr.Close()
	out := &experimentOutput{file: stdout, limit: request.MaxOutputBytes}
	errout := &experimentOutput{file: stderr, limit: request.MaxOutputBytes}
	runContext, cancel := context.WithTimeout(ctx, request.Timeout)
	defer cancel()
	cmd := exec.Command(request.Command[0], request.Command[1:]...)
	cmd.Dir = report.Workdir
	cmd.Env = experimentEnvironment(personalHome, temporary, report.WuuHome, prepared)
	cmd.Stdout, cmd.Stderr = out, errout
	cmd.WaitDelay = 2 * time.Second
	configureExperimentProcess(cmd)
	if err = runContext.Err(); err == nil {
		err = cmd.Start()
		if err == nil {
			finished := make(chan error, 1)
			go func() { finished <- cmd.Wait() }()
			select {
			case err = <-finished:
			case <-runContext.Done():
				// Runtime helpers own nested plugin groups. Give their signal-aware
				// cleanup a bounded opportunity before forcibly retiring our group.
				terminationErr := terminateExperimentProcess(cmd)
				if errors.Is(terminationErr, os.ErrProcessDone) {
					terminationErr = nil
				}
				grace := time.NewTimer(experimentCancellationGrace)
				select {
				case err = <-finished:
				case <-grace.C:
					killErr := killExperimentProcess(cmd)
					if errors.Is(killErr, os.ErrProcessDone) {
						killErr = nil
					}
					err = errors.Join(<-finished, killErr)
				}
				grace.Stop()
				err = errors.Join(runContext.Err(), terminationErr, err)
			}
		}
	}
	// A successful leader can leave descendants with redirected stdio. Always
	// retire the owned process group, even after an ordinary zero exit status.
	if cmd.Process != nil {
		if cleanupErr := killExperimentProcess(cmd); cleanupErr != nil && !errors.Is(cleanupErr, os.ErrProcessDone) {
			err = errors.Join(err, fmt.Errorf("clean up evaluation process group: %w", cleanupErr))
		}
	}
	err = errors.Join(err, stdout.Close(), stderr.Close())
	report.Stdout.Bytes, report.Stdout.Truncated = out.written, out.truncated
	report.Stderr.Bytes, report.Stderr.Truncated = errout.written, errout.truncated
	if cmd.ProcessState != nil {
		code := cmd.ProcessState.ExitCode()
		report.ExitCode = &code
	}
	switch {
	case errors.Is(runContext.Err(), context.DeadlineExceeded):
		report.Status = "timed_out"
	case ctx.Err() != nil:
		report.Status = "cancelled"
	case err != nil:
		report.Status = "failed"
	default:
		report.Status = "succeeded"
	}
	if err != nil {
		report.Error = err.Error()
	}
	return report
}

func experimentEnvironment(home, temporary, wuuHome string, candidate ExperimentCandidate) []string {
	var environment []string
	// Preserve executable discovery and OS runtime essentials, not ambient API
	// keys, provider settings, shell initialization, or credential-directory paths.
	for _, key := range []string{"PATH", "SystemRoot", "WINDIR", "COMSPEC", "PATHEXT", "LANG", "LC_ALL", "TZ", "WUU_NODE_EXECUTABLE"} {
		if value, ok := os.LookupEnv(key); ok {
			environment = append(environment, key+"="+value)
		}
	}
	return append(environment,
		"HOME="+home, "USERPROFILE="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, ".config"), "XDG_DATA_HOME="+filepath.Join(home, ".local", "share"),
		"XDG_CACHE_HOME="+filepath.Join(home, ".cache"), "XDG_STATE_HOME="+filepath.Join(home, ".local", "state"),
		"APPDATA="+filepath.Join(home, "AppData", "Roaming"), "LOCALAPPDATA="+filepath.Join(home, "AppData", "Local"),
		"TMPDIR="+temporary, "TMP="+temporary, "TEMP="+temporary,
		"WUU_HOME="+wuuHome, "WUU_EXPERIMENT_CANDIDATE="+candidate.CandidateID,
		"WUU_EXPERIMENT_PACKAGE="+candidate.PackageRoot,
	)
}

type experimentOutput struct {
	file      *os.File
	limit     int64
	written   int64
	truncated bool
}

func (output *experimentOutput) Write(data []byte) (int, error) {
	size := len(data)
	remaining := output.limit - output.written
	if int64(size) > remaining {
		output.truncated = true
		data = data[:remaining]
	}
	n, err := output.file.Write(data)
	output.written += int64(n)
	if err != nil {
		return n, err
	}
	// Drain discarded bytes so a noisy command cannot block the pipe or grow
	// the evidence file without bound.
	return size, nil
}
