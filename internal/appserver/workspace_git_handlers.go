package appserver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	MethodWorkspaceGitStatus  = "workspace/git/status"
	MethodWorkspaceGitChanges = "workspace/git/changes"
	MethodWorkspaceGitDiff    = "workspace/git/diff"
	workspaceGitListBytes     = 8 * 1024 * 1024
)

type workspaceGitChange struct {
	Path      string `json:"path"`
	OldPath   string `json:"old_path,omitempty"`
	Status    string `json:"status"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Binary    bool   `json:"binary,omitempty"`
}

type workspaceGitChanges struct {
	IsRepo bool                 `json:"is_repo"`
	Root   string               `json:"root,omitempty"`
	Files  []workspaceGitChange `json:"files"`
}

type workspaceGitDiff struct {
	workspaceGitChange
	IsRepo       bool    `json:"is_repo"`
	Patch        string  `json:"patch"`
	OriginalText *string `json:"original_text,omitempty"`
	ModifiedText *string `json:"modified_text,omitempty"`
	Truncated    bool    `json:"truncated"`
}

type workspaceGitTotals struct {
	Files     int `json:"files"`
	Additions int `json:"additions"`
	Deletions int `json:"deletions"`
}

type workspaceGitStatus struct {
	IsRepo     bool               `json:"is_repo"`
	Branch     string             `json:"branch,omitempty"`
	Branches   []string           `json:"branches,omitempty"`
	Detached   bool               `json:"detached"`
	DirtyCount int                `json:"dirty_count"`
	Diff       workspaceGitTotals `json:"diff"`
	StagedDiff workspaceGitTotals `json:"staged_diff"`
}

type boundedGitOutput struct {
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (b *boundedGitOutput) Write(p []byte) (int, error) {
	n := len(p)
	if remaining := b.limit - b.buffer.Len(); n > remaining {
		p = p[:remaining]
		b.truncated = true
	}
	_, _ = b.buffer.Write(p)
	return n, nil
}

// Disable external diff/text conversion and optional index writes: these RPCs
// are read-only views, not an execution surface for repository configuration.
func workspaceGitRun(ctx context.Context, root string, limit int, args ...string) ([]byte, bool, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"--no-pager", "--literal-pathspecs", "-C", root, "-c", "core.fsmonitor=false", "-c", "core.quotePath=false"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	out, stderr := &boundedGitOutput{limit: limit}, &boundedGitOutput{limit: 4096}
	cmd.Stdout, cmd.Stderr = out, stderr
	err := cmd.Run()
	if err != nil {
		err = fmt.Errorf("git %s: %s: %w", args[0], strings.TrimSpace(stderr.buffer.String()), err)
	}
	return out.buffer.Bytes(), out.truncated, err
}

func workspaceGitList(ctx context.Context, root string, args ...string) ([]byte, error) {
	data, truncated, err := workspaceGitRun(ctx, root, workspaceGitListBytes, args...)
	if err == nil && truncated {
		err = errors.New("repository changes exceed the remote view limit")
	}
	return data, err
}

func (s *Server) handleWorkspaceGit(ctx context.Context, req Request) error {
	var params workspaceViewParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	root, err := s.workspaceViewRoot(params.Root)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	top, err := workspaceGitList(ctx, root, "rev-parse", "--show-toplevel")
	if err != nil {
		// Missing Git, permissions and corrupt repositories must not look clean.
		if strings.Contains(err.Error(), "fatal: not a git repository") {
			switch req.Method {
			case MethodWorkspaceGitStatus:
				return s.writeResponse(req.ID, workspaceGitStatus{}, nil)
			case MethodWorkspaceGitChanges:
				return s.writeResponse(req.ID, workspaceGitChanges{Files: []workspaceGitChange{}}, nil)
			default:
				return s.writeResponse(req.ID, workspaceGitDiff{workspaceGitChange: workspaceGitChange{Path: params.Path, Status: "unknown"}}, nil)
			}
		}
		return s.writeResponse(req.ID, nil, err)
	}
	root = strings.TrimSuffix(string(top), "\n")
	root, err = s.workspaceViewRoot(root)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	baseline, err := workspaceGitBaseline(ctx, root)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	var result any
	switch req.Method {
	case MethodWorkspaceGitStatus:
		result, err = readWorkspaceGitStatus(ctx, root, baseline)
	case MethodWorkspaceGitChanges:
		result, err = readWorkspaceGitChanges(ctx, root, baseline)
	case MethodWorkspaceGitDiff:
		result, err = readWorkspaceGitDiff(ctx, root, baseline, params.Path)
	}
	return s.writeResponse(req.ID, result, err)
}

func workspaceGitBaseline(ctx context.Context, root string) (string, error) {
	if _, err := workspaceGitList(ctx, root, "rev-parse", "--verify", "--quiet", "HEAD"); err == nil {
		return "HEAD", nil
	} else {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 {
			return "", err
		}
	}
	// Hash the empty tree without writing it; works with both Git object formats.
	data, err := workspaceGitList(ctx, root, "hash-object", "-t", "tree", "--stdin")
	return strings.TrimSpace(string(data)), err
}

func workspaceGitStats(data []byte) (map[string]workspaceGitChange, error) {
	result := map[string]workspaceGitChange{}
	records := strings.Split(string(data), "\x00")
	for i := 0; i < len(records); i++ {
		if records[i] == "" {
			continue
		}
		fields := strings.SplitN(records[i], "\t", 3)
		if len(fields) != 3 {
			return nil, errors.New("invalid git numstat response")
		}
		path := fields[2]
		if path == "" {
			if i+2 >= len(records) {
				return nil, errors.New("incomplete git rename statistics")
			}
			path = records[i+2]
			i += 2
		}
		change := workspaceGitChange{Path: path, Binary: fields[0] == "-" || fields[1] == "-"}
		if !change.Binary {
			var err error
			change.Additions, err = strconv.Atoi(fields[0])
			if err != nil {
				return nil, err
			}
			change.Deletions, err = strconv.Atoi(fields[1])
			if err != nil {
				return nil, err
			}
		}
		result[path] = change
	}
	return result, nil
}

func readWorkspaceGitChanges(ctx context.Context, root, baseline string) (workspaceGitChanges, error) {
	result := workspaceGitChanges{IsRepo: true, Root: root, Files: []workspaceGitChange{}}
	names, err := workspaceGitList(ctx, root, "diff", "--no-ext-diff", "--no-textconv", "--name-status", "-z", "--find-renames", baseline, "--")
	if err != nil {
		return result, err
	}
	counts, err := workspaceGitList(ctx, root, "diff", "--no-ext-diff", "--no-textconv", "--numstat", "-z", "--find-renames", baseline, "--")
	if err != nil {
		return result, err
	}
	stats, err := workspaceGitStats(counts)
	if err != nil {
		return result, err
	}
	records := strings.Split(string(names), "\x00")
	for i := 0; i+1 < len(records); {
		code, path := records[i], records[i+1]
		i += 2
		if code == "" {
			break
		}
		change := workspaceGitChange{Path: path, Status: "unknown"}
		switch code[0] {
		case 'M', 'T', 'U':
			change.Status = "modified"
		case 'A':
			change.Status = "added"
		case 'D':
			change.Status = "deleted"
		case 'R', 'C':
			if i >= len(records) {
				return result, errors.New("incomplete git rename")
			}
			change.OldPath, change.Path = path, records[i]
			i++
			change.Status = "renamed"
			if code[0] == 'C' {
				change.Status = "copied"
			}
		}
		stat := stats[change.Path]
		change.Additions, change.Deletions, change.Binary = stat.Additions, stat.Deletions, stat.Binary
		result.Files = append(result.Files, change)
	}
	untracked, err := workspaceGitList(ctx, root, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return result, err
	}
	for path := range strings.SplitSeq(string(untracked), "\x00") {
		if path == "" {
			continue
		}
		counts, cut, statErr := workspaceGitRun(ctx, root, workspaceGitListBytes, "diff", "--no-ext-diff", "--no-textconv", "--no-index", "--numstat", "-z", "--", os.DevNull, path)
		if statErr != nil {
			var exit *exec.ExitError
			if !errors.As(statErr, &exit) || exit.ExitCode() != 1 {
				return result, statErr
			}
		}
		if cut {
			return result, errors.New("file statistics exceed the remote view limit")
		}
		stats, err := workspaceGitStats(counts)
		if err != nil {
			return result, err
		}
		change := workspaceGitChange{Path: path, Status: "untracked"}
		for _, stat := range stats {
			change.Additions += stat.Additions
			change.Deletions += stat.Deletions
			change.Binary = change.Binary || stat.Binary
		}
		result.Files = append(result.Files, change)
	}
	return result, nil
}

func gitTotals(changes []workspaceGitChange) workspaceGitTotals {
	total := workspaceGitTotals{Files: len(changes)}
	for _, change := range changes {
		total.Additions += change.Additions
		total.Deletions += change.Deletions
	}
	return total
}

func readWorkspaceGitStatus(ctx context.Context, root, baseline string) (workspaceGitStatus, error) {
	result := workspaceGitStatus{IsRepo: true}
	branch, err := workspaceGitList(ctx, root, "branch", "--show-current")
	if err != nil {
		return result, err
	}
	result.Branch = strings.TrimSpace(string(branch))
	result.Detached = result.Branch == ""
	if result.Detached {
		head, err := workspaceGitList(ctx, root, "rev-parse", "--short", "HEAD")
		if err != nil {
			return result, err
		}
		result.Branch = strings.TrimSpace(string(head))
	}
	branches, err := workspaceGitList(ctx, root, "for-each-ref", "--sort=-committerdate", "--format=%(refname:short)", "refs/heads/")
	if err != nil {
		return result, err
	}
	for branch := range strings.SplitSeq(strings.TrimSpace(string(branches)), "\n") {
		if branch != "" {
			result.Branches = append(result.Branches, branch)
		}
	}
	changes, err := readWorkspaceGitChanges(ctx, root, baseline)
	if err != nil {
		return result, err
	}
	result.Diff = gitTotals(changes.Files)
	status, err := workspaceGitList(ctx, root, "status", "--porcelain=v1", "-z", "--untracked-files=normal")
	if err != nil {
		return result, err
	}
	records := strings.Split(string(status), "\x00")
	for i := 0; i < len(records); i++ {
		entry := records[i]
		if len(entry) < 3 {
			continue
		}
		result.DirtyCount++
		if strings.ContainsAny(entry[:2], "RC") {
			i++
		}
	}
	staged, err := workspaceGitList(ctx, root, "diff", "--no-ext-diff", "--no-textconv", "--cached", "--numstat", "-z", baseline, "--")
	if err != nil {
		return result, err
	}
	stats, err := workspaceGitStats(staged)
	if err != nil {
		return result, err
	}
	for _, change := range stats {
		result.StagedDiff.Files++
		result.StagedDiff.Additions += change.Additions
		result.StagedDiff.Deletions += change.Deletions
	}
	return result, nil
}

func readWorkspaceGitDiff(ctx context.Context, root, baseline, path string) (workspaceGitDiff, error) {
	result := workspaceGitDiff{IsRepo: true, workspaceGitChange: workspaceGitChange{Status: "unknown"}}
	clean, err := workspaceRelativePath(path, false)
	if err != nil {
		return result, err
	}
	path = filepath.ToSlash(clean)
	result.Path = path
	changes, err := readWorkspaceGitChanges(ctx, root, baseline)
	if err != nil {
		return result, err
	}
	for _, change := range changes.Files {
		if change.Path == path {
			result.workspaceGitChange = change
			break
		}
	}
	if result.Status == "unknown" {
		return result, errors.New("file is not in the current repository changes")
	}
	args := []string{"diff", "--no-ext-diff", "--no-textconv", "--find-renames", baseline, "--", path}
	if result.OldPath != "" {
		args = append(args, result.OldPath)
	}
	if result.Status == "untracked" {
		args = []string{"diff", "--no-ext-diff", "--no-textconv", "--no-index", "--", os.DevNull, path}
	}
	patch, truncated, err := workspaceGitRun(ctx, root, workspacePreviewBytes, args...)
	if err != nil {
		var exit *exec.ExitError
		if result.Status != "untracked" || !errors.As(err, &exit) || exit.ExitCode() != 1 {
			return result, err
		}
	}
	result.Patch, result.Truncated = strings.ToValidUTF8(string(patch), "\ufffd"), truncated

	if result.Binary {
		return result, nil
	}
	if result.Status != "untracked" && result.Status != "added" {
		originalPath := path
		if result.OldPath != "" {
			originalPath = result.OldPath
		}
		original, cut, err := workspaceGitRun(ctx, root, workspacePreviewBytes, "show", baseline+":"+originalPath)
		if err != nil {
			return result, err
		}
		text := strings.ToValidUTF8(string(original), "\ufffd")
		result.OriginalText = &text
		result.Truncated = result.Truncated || cut
	} else {
		empty := ""
		result.OriginalText = &empty
	}
	if result.Status != "deleted" {
		rooted, err := os.OpenRoot(root)
		if err != nil {
			return result, err
		}
		defer rooted.Close()
		info, err := rooted.Lstat(clean)
		if err != nil {
			return result, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			// Review the link itself, never its target outside the repository.
			text, err := rooted.Readlink(clean)
			if err != nil {
				return result, err
			}
			result.ModifiedText = &text
		} else if info.Mode().IsRegular() {
			preview, err := readWorkspacePreview(rooted, clean)
			if err != nil {
				return result, err
			}
			result.ModifiedText = preview.Text
			result.Binary = preview.Binary
			result.Truncated = result.Truncated || preview.Truncated
		}
	} else {
		empty := ""
		result.ModifiedText = &empty
	}
	return result, nil
}
