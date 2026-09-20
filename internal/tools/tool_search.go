package tools

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/storagecodec"
	"github.com/blueberrycongee/wuu/internal/stringutil"
	"github.com/blueberrycongee/wuu/internal/toolctx"
)

const (
	maxGrepMatchContentBytes = 4 * 1024
	grepPageSize             = 250
	globPageSize             = 500
	maxRGRecordBytes         = 1024 * 1024
)

var errRipgrepUnavailable = errors.New("ripgrep not available")

// ---------------------------------------------------------------------------
// grep
// ---------------------------------------------------------------------------

type GrepTool struct{ env *Env }

func NewGrepTool(env *Env) *GrepTool { return &GrepTool{env: env} }

func (t *GrepTool) Name() string            { return "grep" }
func (t *GrepTool) IsReadOnly() bool        { return true }
func (t *GrepTool) IsConcurrencySafe() bool { return true }

func (t *GrepTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
		Name:        "grep",
		Description: "Search file contents with a regex. Offset 0 searches current files; use page.next with the same arguments for snapshot-bound continuation. If stale, restart at offset 0 without expected_revision. Use read_file to inspect match ranges.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{
					"type":        "string",
					"description": "Regex pattern to search for.",
				},
				"path": map[string]any{
					"type":        "string",
					"description": "Directory or file to search in. Default is workspace root.",
				},
				"include": map[string]any{
					"type":        "string",
					"description": "Glob pattern to filter files (e.g. '*.go', '*.ts').",
				},
				"output_mode": map[string]any{
					"type":        "string",
					"description": "content (default), files_with_matches, or count.",
				},
				"context": map[string]any{
					"type":        "integer",
					"description": "Number of context lines before and after each match.",
				},
				"before": map[string]any{
					"type":        "integer",
					"description": "Number of lines to show before each match.",
				},
				"after": map[string]any{
					"type":        "integer",
					"description": "Number of lines to show after each match.",
				},
				"ignore_case": map[string]any{
					"type":        "boolean",
					"description": "Case insensitive search.",
				},
				"offset": map[string]any{
					"type":        "integer",
					"description": "Zero-based record offset for continuation. Use page.next.offset from the previous result.",
				},
				"expected_revision": map[string]any{
					"type":        "string",
					"description": "Required with non-zero offset; use page.next.expected_revision to reject stale continuation.",
				},
			},
			"required": []string{"pattern"},
		},
	}
}

func (t *GrepTool) ValidateInput(argsJSON string) error {
	var args struct {
		Pattern    string `json:"pattern"`
		IgnoreCase bool   `json:"ignore_case"`
		Offset     int    `json:"offset"`
	}
	if err := decodeArgs(argsJSON, &args); err != nil {
		return err
	}
	if strings.TrimSpace(args.Pattern) == "" {
		return errors.New("grep requires pattern")
	}
	if args.Offset < 0 {
		return errors.New("grep offset must be non-negative")
	}
	validationPattern := args.Pattern
	if args.IgnoreCase {
		validationPattern = "(?i)" + args.Pattern
	}
	if _, err := regexp.Compile(validationPattern); err != nil {
		return fmt.Errorf("invalid regex: %w", err)
	}
	return nil
}

func (t *GrepTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Pattern          string `json:"pattern"`
		Path             string `json:"path"`
		Include          string `json:"include"`
		OutputMode       string `json:"output_mode"`
		Context          int    `json:"context"`
		Before           int    `json:"before"`
		After            int    `json:"after"`
		IgnoreCase       bool   `json:"ignore_case"`
		Offset           int    `json:"offset"`
		ExpectedRevision string `json:"expected_revision"`
	}
	if err := decodeArgs(argsJSON, &args); err != nil {
		return "", err
	}
	if strings.TrimSpace(args.Pattern) == "" {
		return "", errors.New("grep requires pattern")
	}

	// For regex validation, prepend (?i) if ignore_case so the compiled regex
	// matches the same way ripgrep will.
	validationPattern := args.Pattern
	if args.IgnoreCase {
		validationPattern = "(?i)" + args.Pattern
	}
	if _, err := regexp.Compile(validationPattern); err != nil {
		return "", fmt.Errorf("invalid regex: %w", err)
	}

	// Worktree-bound execution: the search root switches to the checkout
	// after the ordinary sandbox check accepted the workspace path.
	execRoot, err := t.env.ExecRootDir(ctx)
	if err != nil {
		return "", err
	}
	searchRoot := execRoot
	if strings.TrimSpace(args.Path) != "" {
		resolved, err := t.env.ResolvePath(args.Path)
		if err != nil {
			return "", err
		}
		resolved, err = t.env.ExecPath(ctx, resolved)
		if err != nil {
			return "", err
		}
		searchRoot = resolved
	}

	opts := grepOptions{
		outputMode: args.OutputMode,
		context:    args.Context,
		before:     args.Before,
		after:      args.After,
		ignoreCase: args.IgnoreCase,
	}
	if opts.outputMode == "" {
		opts.outputMode = "content"
	}
	revisionKey := searchWorkspaceRevision(ctx, t.env)

	switch opts.outputMode {
	case "files_with_matches":
		key := searchCursorKey("grep:files", execRoot, searchRoot, args.Pattern, args.Include, fmt.Sprintf("%t", opts.ignoreCase), revisionKey)
		cachePath := searchCursorPath(t.env, key)
		files, ok := loadSearchCursor[string](cachePath, args.Offset)
		if !ok {
			var err error
			files, err = grepFilesWithMatches(ctx, execRoot, args.Pattern, searchRoot, args.Include, opts, 0)
			if err != nil {
				return "", err
			}
			_ = saveSearchCursor(cachePath, files)
		}
		revision := continuationSnapshotRevision(revisionKey, "grep:files", files)
		if err := validateStableOffset("grep", args.Offset, args.ExpectedRevision, revision); err != nil {
			return "", err
		}
		page, hasMore := pageWindow(files, args.Offset, grepPageSize)
		result := map[string]any{
			"action":                 "grep",
			"pattern":                args.Pattern,
			"continuation_supported": true,
			"workspace_revision":     revision,
			"total":                  len(files),
			"record_total":           len(files),
			"offset":                 args.Offset,
			"returned_count":         len(page),
			"has_more":               hasMore,
			"page":                   continuationPage(args.Offset, len(page), hasMore, revision),
			"truncated":              hasMore,
			"files":                  page,
			"next_suggestions":       searchNextSuggestions("grep", "files_with_matches", len(files), hasMore),
		}
		return mustJSON(result)

	case "count":
		key := searchCursorKey("grep:count", execRoot, searchRoot, args.Pattern, args.Include, fmt.Sprintf("%t", opts.ignoreCase), revisionKey)
		cachePath := searchCursorPath(t.env, key)
		counts, ok := loadSearchCursor[grepCountResult](cachePath, args.Offset)
		var total int
		if !ok {
			var err error
			counts, total, err = grepCountMatches(ctx, execRoot, args.Pattern, searchRoot, args.Include, opts, 0)
			if err != nil {
				return "", err
			}
			_ = saveSearchCursor(cachePath, counts)
		} else {
			for _, count := range counts {
				total += count.Count
			}
		}
		revision := continuationSnapshotRevision(revisionKey, "grep:count", counts)
		if err := validateStableOffset("grep", args.Offset, args.ExpectedRevision, revision); err != nil {
			return "", err
		}
		page, hasMore := pageWindow(counts, args.Offset, grepPageSize)
		result := map[string]any{
			"action":                 "grep",
			"pattern":                args.Pattern,
			"continuation_supported": true,
			"workspace_revision":     revision,
			"total":                  total,
			"record_total":           len(counts),
			"offset":                 args.Offset,
			"returned_count":         len(page),
			"has_more":               hasMore,
			"page":                   continuationPage(args.Offset, len(page), hasMore, revision),
			"truncated":              hasMore,
			"counts":                 page,
			"next_suggestions":       searchNextSuggestions("grep", "count", total, hasMore),
		}
		return mustJSON(result)

	default: // "content"
		key := searchCursorKey("grep:content", execRoot, searchRoot, args.Pattern, args.Include, fmt.Sprintf("%d:%d:%d:%t", opts.context, opts.before, opts.after, opts.ignoreCase), revisionKey)
		cachePath := searchCursorPath(t.env, key)
		matches, ok := loadSearchCursor[grepMatch](cachePath, args.Offset)
		if !ok {
			var err error
			matches, err = grepWithRipgrep(ctx, t.env, execRoot, args.Pattern, searchRoot, args.Include, opts, 0)
			if err != nil {
				if !errors.Is(err, errRipgrepUnavailable) {
					return "", err
				}
				matches, err = grepWithFallback(t.env, execRoot, args.Pattern, searchRoot, args.Include, opts, 0)
				if err != nil {
					return "", err
				}
			}
			_ = saveSearchCursor(cachePath, matches)
		}
		revision := continuationSnapshotRevision(revisionKey, "grep:content", matches)
		if err := validateStableOffset("grep", args.Offset, args.ExpectedRevision, revision); err != nil {
			return "", err
		}
		return grepContentResultJSON(args.Pattern, matches, args.Offset, revision)
	}
}

// ---------------------------------------------------------------------------
// glob
// ---------------------------------------------------------------------------

type GlobTool struct{ env *Env }

func NewGlobTool(env *Env) *GlobTool { return &GlobTool{env: env} }

func (t *GlobTool) Name() string            { return "glob" }
func (t *GlobTool) IsReadOnly() bool        { return true }
func (t *GlobTool) IsConcurrencySafe() bool { return true }

func (t *GlobTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
		Name:        "glob",
		Description: "Find files by glob pattern. Offset 0 searches current files; use page.next with the same arguments for snapshot-bound continuation. If stale, restart at offset 0 without expected_revision. Use grep for content search.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{
					"type":        "string",
					"description": "Glob pattern (e.g. '**/*.go', 'src/**/*.ts', '*.json').",
				},
				"path": map[string]any{
					"type":        "string",
					"description": "Directory to search in. Default is workspace root.",
				},
				"offset": map[string]any{
					"type":        "integer",
					"description": "Zero-based record offset for continuation. Use page.next.offset from the previous result.",
				},
				"expected_revision": map[string]any{
					"type":        "string",
					"description": "Required with non-zero offset; use page.next.expected_revision to reject stale continuation.",
				},
			},
			"required": []string{"pattern"},
		},
	}
}

func (t *GlobTool) ValidateInput(argsJSON string) error {
	var args struct {
		Pattern string `json:"pattern"`
		Offset  int    `json:"offset"`
	}
	if err := decodeArgs(argsJSON, &args); err != nil {
		return err
	}
	if strings.TrimSpace(args.Pattern) == "" {
		return errors.New("glob requires pattern")
	}
	if args.Offset < 0 {
		return errors.New("glob offset must be non-negative")
	}
	return nil
}

func (t *GlobTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Pattern          string `json:"pattern"`
		Path             string `json:"path"`
		Offset           int    `json:"offset"`
		ExpectedRevision string `json:"expected_revision"`
	}
	if err := decodeArgs(argsJSON, &args); err != nil {
		return "", err
	}
	if strings.TrimSpace(args.Pattern) == "" {
		return "", errors.New("glob requires pattern")
	}

	// Worktree-bound execution: the search root switches to the checkout
	// after the ordinary sandbox check accepted the workspace path.
	execRoot, err := t.env.ExecRootDir(ctx)
	if err != nil {
		return "", err
	}
	searchRoot := execRoot
	if strings.TrimSpace(args.Path) != "" {
		resolved, err := t.env.ResolvePath(args.Path)
		if err != nil {
			return "", err
		}
		resolved, err = t.env.ExecPath(ctx, resolved)
		if err != nil {
			return "", err
		}
		searchRoot = resolved
	}

	revisionKey := searchWorkspaceRevision(ctx, t.env)
	key := searchCursorKey("glob", execRoot, searchRoot, args.Pattern, revisionKey)
	cachePath := searchCursorPath(t.env, key)
	matches, ok := loadSearchCursor[string](cachePath, args.Offset)
	if !ok {
		var err error
		matches, err = globWithRipgrep(ctx, execRoot, searchRoot, args.Pattern, 0)
		if err != nil {
			if !errors.Is(err, errRipgrepUnavailable) {
				return "", err
			}
			matches, err = globWithFallback(execRoot, searchRoot, args.Pattern, 0)
			if err != nil {
				return "", err
			}
		}
		_ = saveSearchCursor(cachePath, matches)
	}
	revision := continuationSnapshotRevision(revisionKey, "glob", matches)
	if err := validateStableOffset("glob", args.Offset, args.ExpectedRevision, revision); err != nil {
		return "", err
	}
	page, hasMore := pageWindow(matches, args.Offset, globPageSize)

	result := map[string]any{
		"action":                 "glob",
		"pattern":                args.Pattern,
		"continuation_supported": true,
		"workspace_revision":     revision,
		"total":                  len(matches),
		"record_total":           len(matches),
		"offset":                 args.Offset,
		"returned_count":         len(page),
		"has_more":               hasMore,
		"page":                   continuationPage(args.Offset, len(page), hasMore, revision),
		"truncated":              hasMore,
		"files":                  page,
		"next_suggestions":       searchNextSuggestions("glob", "", len(matches), hasMore),
	}
	return mustJSON(result)
}

func searchNextSuggestions(toolName, outputMode string, total int, truncated bool) []string {
	if truncated {
		switch toolName {
		case "glob":
			return []string{"use page.next with the same glob arguments for more files, or narrow the path or pattern"}
		default:
			switch outputMode {
			case "count":
				return []string{"use page.next with the same grep arguments for more count records, or narrow the search"}
			case "files_with_matches":
				return []string{"use page.next with the same grep arguments for more files, or narrow the search"}
			default:
				return []string{"use page.next with the same grep arguments for more matches, or narrow the search"}
			}
		}
	}
	if total == 0 {
		switch toolName {
		case "glob":
			return []string{"try a broader glob pattern or inspect the directory with list_files"}
		default:
			return []string{"try a broader or case-insensitive grep pattern, or use glob to find candidate files first"}
		}
	}
	if toolName == "glob" {
		return []string{"use read_file for specific candidate files or grep within the matched set before editing"}
	}
	switch outputMode {
	case "files_with_matches":
		return []string{"use grep output_mode=content or read_file on the most relevant matched files"}
	case "count":
		return []string{"rerun grep with output_mode=content for the highest-value files before editing"}
	default:
		return []string{"read_file the relevant match ranges before editing or making a conclusion"}
	}
}

func grepContentResultJSON(pattern string, matches []grepMatch, offset int, revision string) (string, error) {
	page, _ := pageWindow(matches, offset, grepPageSize)
	truncatedPage := make([]grepMatch, len(page))
	contentTruncated := false
	for i, match := range page {
		if match.ContentTruncated {
			contentTruncated = true
		}
		if len(match.Content) > maxGrepMatchContentBytes {
			match.Content = stringutil.HeadTail(match.Content, maxGrepMatchContentBytes/2, maxGrepMatchContentBytes/4, "\n...[line truncated]...\n")
			match.ContentTruncated = true
			contentTruncated = true
		}
		truncatedPage[i] = match
	}

	// Encode every match exactly once. The previous loop re-marshaled the
	// whole page after each dropped record, so a full page of long matches
	// cost hundreds of megabyte-scale JSON passes.
	encoded := make([][]byte, len(truncatedPage))
	for i := range truncatedPage {
		data, err := json.Marshal(truncatedPage[i])
		if err != nil {
			return "", err
		}
		encoded[i] = data
	}

	upper := grepEnvelopeUpperBound(pattern, len(matches), offset, len(page), contentTruncated, revision)
	// Keep the longest prefix of matches whose exact JSON array size fits.
	// Replacing the empty array (2 bytes) with k elements costs
	// sum(len(encoded[:k])) + k + 1 bytes, i.e. sum + k - 1 extra.
	kept := 0
	sum := 0
	for i := 0; i < len(encoded); i++ {
		sum += len(encoded[i])
		if upper+sum+i > maxGrepOutputBytes {
			break
		}
		kept = i + 1
	}

	omitted := len(truncatedPage) - kept
	hasMore := offset+kept < len(matches)
	result := grepContentResultMap(
		pattern,
		len(matches),
		offset,
		kept,
		omitted,
		truncatedPage[:kept],
		contentTruncated,
		hasMore,
		revision,
	)
	return mustJSON(result)
}

// grepEnvelopeUpperBound returns a conservative size for the grep content
// result minus its matches array. Both has_more states and max-width counts
// are considered so the bound covers every candidate page length.
func grepEnvelopeUpperBound(pattern string, total, offset, pageLen int, contentTruncated bool, revision string) int {
	largest := 0
	for _, hasMore := range []bool{true, false} {
		out, err := mustJSON(grepContentResultMap(pattern, total, offset, pageLen, pageLen, nil, contentTruncated, hasMore, revision))
		if err != nil {
			return maxGrepOutputBytes + 1
		}
		if len(out) > largest {
			largest = len(out)
		}
	}
	return largest
}

func grepContentResultMap(pattern string, total, offset, shown, omitted int, matchesValue any, contentTruncated, hasMore bool, revision string) map[string]any {
	truncated := hasMore || contentTruncated
	return map[string]any{
		"action":                 "grep",
		"pattern":                pattern,
		"continuation_supported": true,
		"workspace_revision":     revision,
		"total":                  total,
		"record_total":           total,
		"offset":                 offset,
		"has_more":               hasMore,
		"truncated":              truncated,
		"matches":                matchesValue,
		"omitted_match_count":    omitted,
		"content_truncated":      contentTruncated,
		"next_suggestions":       searchNextSuggestions("grep", "content", total, hasMore),
		"result_budget_bytes":    maxGrepOutputBytes,
		"returned_match_count":   shown,
		"returned_count":         shown,
		"page":                   continuationPage(offset, shown, hasMore, revision),
	}
}

// ---------------------------------------------------------------------------
// Shared grep/glob implementation (extracted from old Toolkit methods)
// ---------------------------------------------------------------------------

// searchWorkspaceRevision returns the revision of the root search executes
// in. The toolkit computes the workspace revision once per tool call and
// stashes it in the context; search tools reuse that value when the stash was
// computed for the same root, which is always true except for threads bound
// to an isolated worktree checkout (those compute their own).
func searchWorkspaceRevision(ctx context.Context, env *Env) string {
	root := env.RevisionRoot(ctx)
	if stashedRoot, revision, ok := toolctx.WorkspaceRevision(ctx); ok && stashedRoot == root {
		return revision
	}
	return workspaceRevision(ctx, root)
}

func searchResultCapacity(limit int) int {
	if limit <= 0 || limit > 16 {
		return 16
	}
	return limit
}

// searchCursorKey hashes the full result identity so a session can reuse one
// materialized search result across offset pages without re-running ripgrep.
// It is internal to the search tools and never surfaces to the model.
func searchCursorKey(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		_, _ = h.Write([]byte(part))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func searchCursorPath(env *Env, key string) string {
	if env == nil || strings.TrimSpace(env.SessionDir) == "" {
		return ""
	}
	return filepath.Join(env.SessionDir, "tool-results", "search-cursors", key+".json")
}

func saveSearchCursor(path string, records any) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(records)
	if err != nil {
		return err
	}
	stored, err := storagecodec.Encode(data)
	if err != nil {
		return err
	}
	return os.WriteFile(path, stored, 0o644)
}

// Only continuation can reuse a materialized result. Workspace revisions are
// best-effort summaries, not proof that current search results are unchanged.
// Refreshing offset 0 replaces this cache; the result hash in the continuation
// token rejects old pages if the refreshed records differ, even at the same
// workspace revision. Cache misses still rescan and validate that exact token.
func loadSearchCursor[T any](path string, offset int) ([]T, bool) {
	if offset <= 0 || path == "" {
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	data, err = storagecodec.Decode(data)
	if err != nil {
		return nil, false
	}
	var records []T
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, false
	}
	return records, true
}

func boundedSearchPage(returned int, hasMore bool) map[string]any {
	return map[string]any{
		"offset":   0,
		"returned": returned,
		"has_more": hasMore,
	}
}

func grepWithRipgrep(ctx context.Context, env *Env, rootDir, pattern, searchRoot, include string, opts grepOptions, limit int) ([]grepMatch, error) {
	relSearchRoot, err := filepath.Rel(rootDir, searchRoot)
	if err != nil {
		return nil, err
	}
	if relSearchRoot == "." {
		relSearchRoot = ""
	}
	cmd := buildRGGrepCommand(ctx, pattern, relSearchRoot, include, opts)
	if cmd == nil {
		return nil, errRipgrepUnavailable
	}
	cmd.Dir = rootDir
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	matches := make([]grepMatch, 0, searchResultCapacity(limit))
	earlyStop := false
	reader := bufio.NewReaderSize(stdout, 64*1024)
	currentFile := ""
	var readErr error
	for {
		line, oversized, err := readBoundedRGRecord(reader, maxRGRecordBytes)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			readErr = err
			_ = cmd.Process.Kill()
			break
		}
		if len(line) == 0 {
			continue
		}
		if oversized {
			if currentFile != "" && bytes.Contains(line, []byte(`"type":"match"`)) {
				matches = append(matches, grepMatch{
					File:             currentFile,
					Content:          "[match omitted: line exceeds 1 MiB; use read_file on this file to inspect]",
					ContentTruncated: true,
				})
				if limit > 0 && len(matches) >= limit {
					earlyStop = true
					_ = cmd.Process.Kill()
					break
				}
			}
			continue
		}
		var event rgJSONEvent
		if err := json.Unmarshal(line, &event); err != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return nil, fmt.Errorf("parse ripgrep output: %w", err)
		}
		switch event.Type {
		case "begin":
			currentFile = relativeRGPath(rootDir, event.Data.Path.Text)
			continue
		case "end":
			currentFile = ""
			continue
		case "match":
		default:
			continue
		}
		rel := relativeRGPath(rootDir, event.Data.Path.Text)
		if rel == "" {
			continue
		}
		matches = append(matches, grepMatch{
			File:    rel,
			Line:    event.Data.LineNumber,
			Content: grepMatchContentForPath(env, rel, strings.TrimRight(event.Data.Lines.Text, "\r\n")),
		})
		if limit > 0 && len(matches) >= limit {
			earlyStop = true
			_ = cmd.Process.Kill()
			break
		}
	}
	waitErr := cmd.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if readErr != nil && !earlyStop {
		return nil, fmt.Errorf("read ripgrep output: %w", readErr)
	}
	if waitErr != nil && !earlyStop {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) && exitErr.ExitCode() == 1 {
			return []grepMatch{}, nil
		}
		message := strings.TrimSpace(stderr.String())
		if message != "" {
			return nil, fmt.Errorf("ripgrep failed: %s", message)
		}
		return nil, waitErr
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].File == matches[j].File {
			return matches[i].Line < matches[j].Line
		}
		return matches[i].File < matches[j].File
	})
	return matches, nil
}

func readBoundedRGRecord(reader *bufio.Reader, maxBytes int) ([]byte, bool, error) {
	record := make([]byte, 0, min(maxBytes, 64*1024))
	oversized := false
	for {
		fragment, err := reader.ReadSlice('\n')
		if !oversized {
			remaining := maxBytes - len(record)
			if len(fragment) > remaining {
				record = append(record, fragment[:remaining]...)
				oversized = true
			} else {
				record = append(record, fragment...)
			}
		}

		switch err {
		case nil:
			return bytes.TrimSuffix(record, []byte{'\n'}), oversized, nil
		case bufio.ErrBufferFull:
			continue
		case io.EOF:
			if len(record) == 0 && !oversized {
				return nil, false, io.EOF
			}
			return record, oversized, nil
		default:
			return nil, oversized, err
		}
	}
}

func relativeRGPath(rootDir, matchPath string) string {
	if matchPath == "" {
		return ""
	}
	if !filepath.IsAbs(matchPath) {
		matchPath = filepath.Join(rootDir, matchPath)
	}
	rel, err := filepath.Rel(rootDir, matchPath)
	if err != nil {
		return ""
	}
	return filepath.ToSlash(rel)
}

func grepWithFallback(env *Env, rootDir, pattern, searchRoot, include string, opts grepOptions, limit int) ([]grepMatch, error) {
	compilePattern := pattern
	if opts.ignoreCase {
		compilePattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(compilePattern)
	if err != nil {
		return nil, fmt.Errorf("invalid regex: %w", err)
	}

	matches := make([]grepMatch, 0, searchResultCapacity(limit))
	walkErr := filepath.Walk(searchRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if isSkippedDir(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if limit > 0 && len(matches) >= limit {
			return filepath.SkipAll
		}

		rel, err := filepath.Rel(rootDir, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if include != "" && !matchGlob(include, rel) {
			return nil
		}
		if isBinaryFile(path) {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		scanner := bufio.NewScanner(bytes.NewReader(data))
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			line := scanner.Text()
			if re.MatchString(line) {
				matches = append(matches, grepMatch{
					File:    rel,
					Line:    lineNum,
					Content: grepMatchContentForPath(env, rel, line),
				})
				if limit > 0 && len(matches) >= limit {
					break
				}
			}
		}
		if err := scanner.Err(); err != nil {
			return fmt.Errorf("scan %s: %w", rel, err)
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].File == matches[j].File {
			return matches[i].Line < matches[j].Line
		}
		return matches[i].File < matches[j].File
	})
	return matches, nil
}

func grepMatchContentForPath(env *Env, path, content string) string {
	if !isSensitivePath(path) {
		return content
	}
	// Unconfined lifts the read gate but not secret redaction: the match
	// reaches the model with credential values masked.
	if env.BypassToolHardProtections() {
		return redactToolOutput(content)
	}
	return "[REDACTED: sensitive file content]"
}

func globWithRipgrep(ctx context.Context, rootDir, searchRoot, pattern string, limit int) ([]string, error) {
	cmd := buildRGFilesCommand(ctx, pattern)
	if cmd == nil {
		return nil, errRipgrepUnavailable
	}
	cmd.Dir = searchRoot
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	matches := make([]string, 0, searchResultCapacity(limit))
	earlyStop := false
	scanner := bufio.NewScanner(stdout)
	scanner.Split(splitNullTerminated)
	scanner.Buffer(make([]byte, 64*1024), maxRGRecordBytes)
	for scanner.Scan() {
		entry := scanner.Bytes()
		if len(entry) == 0 {
			continue
		}
		p := string(entry)
		// rg outputs paths relative to cmd.Dir (searchRoot).
		// Convert to absolute then back to rootDir-relative.
		if !filepath.IsAbs(p) {
			p = filepath.Join(searchRoot, p)
		}
		rel, err := filepath.Rel(rootDir, p)
		if err != nil {
			continue
		}
		matches = append(matches, filepath.ToSlash(rel))
		if limit > 0 && len(matches) >= limit {
			earlyStop = true
			_ = cmd.Process.Kill()
			break
		}
	}
	scanErr := scanner.Err()
	if scanErr != nil {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if scanErr != nil && !earlyStop {
		return nil, fmt.Errorf("read ripgrep output: %w", scanErr)
	}
	if waitErr != nil && !earlyStop {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) && exitErr.ExitCode() == 1 {
			return []string{}, nil
		}
		message := strings.TrimSpace(stderr.String())
		if message != "" {
			return nil, fmt.Errorf("ripgrep failed: %s", message)
		}
		return nil, waitErr
	}
	sort.Strings(matches)
	return matches, nil
}

func splitNullTerminated(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if i := bytes.IndexByte(data, 0); i >= 0 {
		return i + 1, data[:i], nil
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

func globWithFallback(rootDir, searchRoot, pattern string, limit int) ([]string, error) {
	matches := make([]string, 0, searchResultCapacity(limit))
	_ = filepath.Walk(searchRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if isSkippedDir(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}

		rel, err := filepath.Rel(rootDir, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if matchGlob(pattern, rel) {
			matches = append(matches, rel)
		}
		if limit > 0 && len(matches) >= limit {
			return filepath.SkipAll
		}
		return nil
	})
	sort.Strings(matches)
	return matches, nil
}

// ---------------------------------------------------------------------------
// grep output_mode: files_with_matches
// ---------------------------------------------------------------------------

func grepFilesWithMatches(ctx context.Context, rootDir, pattern, searchRoot, include string, opts grepOptions, limit int) ([]string, error) {
	files, err := grepFilesWithMatchesRG(ctx, rootDir, pattern, searchRoot, include, opts, limit)
	if err != nil {
		if !errors.Is(err, errRipgrepUnavailable) {
			return nil, err
		}
		return grepFilesWithMatchesFallback(rootDir, pattern, searchRoot, include, opts, limit)
	}
	return files, nil
}

func grepFilesWithMatchesRG(ctx context.Context, rootDir, pattern, searchRoot, include string, opts grepOptions, limit int) ([]string, error) {
	name := lookupRG()
	if name == "" {
		return nil, errRipgrepUnavailable
	}

	relSearchRoot, err := filepath.Rel(rootDir, searchRoot)
	if err != nil {
		return nil, err
	}
	if relSearchRoot == "." {
		relSearchRoot = ""
	}

	args := []string{"--no-config", "--files-with-matches", "--hidden", "-H"}
	if opts.ignoreCase {
		args = append(args, "-i")
	}
	if include != "" {
		args = append(args, "--glob", include)
	}
	args = append(args, "--", pattern)
	if strings.TrimSpace(relSearchRoot) != "" {
		args = append(args, relSearchRoot)
	} else {
		args = append(args, ".")
	}

	cmd := rgCommand(ctx, name, args...)
	cmd.Dir = rootDir
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	files := make([]string, 0, searchResultCapacity(limit))
	earlyStop := false
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), maxRGRecordBytes)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		p := string(line)
		if !filepath.IsAbs(p) {
			p = filepath.Join(rootDir, p)
		}
		rel, err := filepath.Rel(rootDir, p)
		if err != nil {
			continue
		}
		files = append(files, filepath.ToSlash(rel))
		if limit > 0 && len(files) >= limit {
			earlyStop = true
			_ = cmd.Process.Kill()
			break
		}
	}
	scanErr := scanner.Err()
	if scanErr != nil {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if scanErr != nil && !earlyStop {
		return nil, fmt.Errorf("read ripgrep output: %w", scanErr)
	}
	if waitErr != nil && !earlyStop {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) && exitErr.ExitCode() == 1 {
			return []string{}, nil
		}
		message := strings.TrimSpace(stderr.String())
		if message != "" {
			return nil, fmt.Errorf("ripgrep failed: %s", message)
		}
		return nil, waitErr
	}
	sort.Strings(files)
	return files, nil
}

func grepFilesWithMatchesFallback(rootDir, pattern, searchRoot, include string, opts grepOptions, limit int) ([]string, error) {
	compilePattern := pattern
	if opts.ignoreCase {
		compilePattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(compilePattern)
	if err != nil {
		return nil, fmt.Errorf("invalid regex: %w", err)
	}

	files := make([]string, 0, searchResultCapacity(limit))
	walkErr := filepath.Walk(searchRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if isSkippedDir(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if limit > 0 && len(files) >= limit {
			return filepath.SkipAll
		}

		rel, err := filepath.Rel(rootDir, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if include != "" && !matchGlob(include, rel) {
			return nil
		}
		if isBinaryFile(path) {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		if re.Match(data) {
			files = append(files, rel)
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	sort.Strings(files)
	return files, nil
}

// ---------------------------------------------------------------------------
// grep output_mode: count
// ---------------------------------------------------------------------------

func grepCountMatches(ctx context.Context, rootDir, pattern, searchRoot, include string, opts grepOptions, limit int) ([]grepCountResult, int, error) {
	counts, total, err := grepCountMatchesRG(ctx, rootDir, pattern, searchRoot, include, opts, limit)
	if err != nil {
		if !errors.Is(err, errRipgrepUnavailable) {
			return nil, 0, err
		}
		return grepCountMatchesFallback(rootDir, pattern, searchRoot, include, opts, limit)
	}
	return counts, total, nil
}

func grepCountMatchesRG(ctx context.Context, rootDir, pattern, searchRoot, include string, opts grepOptions, limit int) ([]grepCountResult, int, error) {
	name := lookupRG()
	if name == "" {
		return nil, 0, errRipgrepUnavailable
	}

	relSearchRoot, err := filepath.Rel(rootDir, searchRoot)
	if err != nil {
		return nil, 0, err
	}
	if relSearchRoot == "." {
		relSearchRoot = ""
	}

	args := []string{"--no-config", "--count", "--hidden", "-H"}
	if opts.ignoreCase {
		args = append(args, "-i")
	}
	if include != "" {
		args = append(args, "--glob", include)
	}
	args = append(args, "--", pattern)
	if strings.TrimSpace(relSearchRoot) != "" {
		args = append(args, relSearchRoot)
	} else {
		args = append(args, ".")
	}

	cmd := rgCommand(ctx, name, args...)
	cmd.Dir = rootDir

	output, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return []grepCountResult{}, 0, nil
		}
		return nil, 0, err
	}

	counts := make([]grepCountResult, 0, searchResultCapacity(limit))
	total := 0
	for _, line := range bytes.Split(bytes.TrimSpace(output), []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		// rg --count output: "file:count"
		parts := bytes.SplitN(line, []byte{':'}, 2)
		if len(parts) != 2 {
			continue
		}
		p := string(parts[0])
		if !filepath.IsAbs(p) {
			p = filepath.Join(rootDir, p)
		}
		rel, err := filepath.Rel(rootDir, p)
		if err != nil {
			continue
		}
		var count int
		if _, err := fmt.Sscanf(string(parts[1]), "%d", &count); err != nil {
			continue
		}
		total += count
		if limit <= 0 || len(counts) < limit {
			counts = append(counts, grepCountResult{
				File:  filepath.ToSlash(rel),
				Count: count,
			})
		}
	}
	sort.SliceStable(counts, func(i, j int) bool {
		return counts[i].File < counts[j].File
	})
	return counts, total, nil
}

func grepCountMatchesFallback(rootDir, pattern, searchRoot, include string, opts grepOptions, limit int) ([]grepCountResult, int, error) {
	compilePattern := pattern
	if opts.ignoreCase {
		compilePattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(compilePattern)
	if err != nil {
		return nil, 0, fmt.Errorf("invalid regex: %w", err)
	}

	counts := make([]grepCountResult, 0, searchResultCapacity(limit))
	total := 0
	walkErr := filepath.Walk(searchRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if isSkippedDir(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}

		rel, err := filepath.Rel(rootDir, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if include != "" && !matchGlob(include, rel) {
			return nil
		}
		if isBinaryFile(path) {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		matches := re.FindAll(data, -1)
		if len(matches) > 0 {
			total += len(matches)
			if limit <= 0 || len(counts) < limit {
				counts = append(counts, grepCountResult{
					File:  rel,
					Count: len(matches),
				})
			}
		}
		return nil
	})
	if walkErr != nil {
		return nil, 0, walkErr
	}
	sort.SliceStable(counts, func(i, j int) bool {
		return counts[i].File < counts[j].File
	})
	return counts, total, nil
}
