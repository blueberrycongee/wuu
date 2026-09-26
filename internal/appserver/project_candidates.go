package appserver

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/worktree"
)

// captureCandidate freezes a worktree session's changes at the end of its
// latest turn. A newer turn supersedes an older one, so an older turn whose
// session moved on is left without a candidate. Each candidate is measured
// from the last applied one so an applied change is never offered again.
func (s *Server) captureCandidate(th *threadState, turn Turn) (*session.Candidate, error) {
	current := func() bool {
		th.mu.Lock()
		defer th.mu.Unlock()
		return !th.running && len(th.Turns) > 0 && th.Turns[len(th.Turns)-1].ID == turn.ID
	}
	th.mu.Lock()
	root, baseHEAD, baseRepo := th.WorktreePath, th.WorktreeBaseHEAD, th.WorktreeBaseRepo
	th.mu.Unlock()
	if root == "" || baseHEAD == "" || !current() {
		return nil, nil
	}
	if existing, found, err := session.FindCandidate(s.rt.SessionDir, th.ID, turn.ID); err != nil {
		return nil, err
	} else if found {
		return &existing, nil
	}
	base := baseHEAD
	if applied, found, err := session.LatestAppliedCandidate(s.rt.SessionDir, th.ID); err != nil {
		return nil, err
	} else if found {
		base = applied.Revision
	}
	ctx := context.Background()
	revision, diff, err := worktree.Snapshot(ctx, root, base, th.ID+"-"+turn.ID)
	if err != nil || strings.TrimSpace(diff) == "" || !current() {
		return nil, err
	}
	names, err := workspaceGitList(ctx, root, "diff", "--name-only", "-z", base, revision, "--")
	if err != nil {
		return nil, err
	}
	candidate := session.Candidate{SessionID: th.ID, TurnID: turn.ID, BaseRepo: baseRepo, BaseRevision: base, Revision: revision}
	for _, name := range strings.Split(strings.TrimRight(string(names), "\x00"), "\x00") {
		if name != "" {
			candidate.ChangedFiles = append(candidate.ChangedFiles, name)
		}
	}
	if err := session.PutCandidate(s.rt.SessionDir, candidate); err != nil {
		return nil, err
	}
	return &candidate, nil
}

func projectCandidateView(candidate session.Candidate) ProjectCandidate {
	return ProjectCandidate{
		SessionID: candidate.SessionID, TurnID: candidate.TurnID, BaseRepo: candidate.BaseRepo,
		BaseRevision: candidate.BaseRevision, Revision: candidate.Revision, ChangedFiles: candidate.ChangedFiles,
		Disposition: candidate.Disposition, CreatedAt: candidate.CreatedAt,
	}
}

// handleProjectCandidate lists, reads and decides candidates. The user's
// decision is the delivery: applying writes only the frozen change into the
// workspace, and a conflict leaves both the workspace and the decision alone.
func (s *Server) handleProjectCandidate(ctx context.Context, req Request) error {
	var p ProjectCandidateParams
	if err := decodeParams(req.Params, &p); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	result, err := s.projectCandidate(ctx, p)
	return s.writeResponse(req.ID, result, err)
}

func (s *Server) projectCandidate(ctx context.Context, p ProjectCandidateParams) (*ProjectCandidateResult, error) {
	if p.Action == "list" {
		ids := []string{p.SessionID}
		if p.ProjectID != "" {
			managed, err := s.projectSessions(p.ProjectID)
			if err != nil {
				return nil, err
			}
			ids = ids[:0]
			for _, metadata := range managed {
				ids = append(ids, metadata.ID)
			}
		} else if p.SessionID == "" {
			return nil, errors.New("list needs a project_id or session_id")
		}
		candidates, err := session.ListCandidates(s.rt.SessionDir, ids)
		if err != nil {
			return nil, err
		}
		result := &ProjectCandidateResult{Candidates: make([]ProjectCandidate, 0, len(candidates))}
		for _, candidate := range candidates {
			result.Candidates = append(result.Candidates, projectCandidateView(candidate))
		}
		return result, nil
	}
	metadata, found, err := session.Find(s.rt.SessionDir, p.SessionID)
	if err != nil {
		return nil, err
	}
	if !found || metadata.Source != projectSessionSource {
		return nil, errors.New("candidate session is not managed by a project")
	}
	s.candidateMu.Lock()
	defer s.candidateMu.Unlock()
	candidate, found, err := session.FindCandidate(s.rt.SessionDir, p.SessionID, p.TurnID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, errors.New("candidate not found")
	}
	switch p.Action {
	case "get":
		diff, err := worktree.SnapshotDiff(ctx, candidate.BaseRepo, candidate.BaseRevision, candidate.Revision)
		if err != nil {
			return nil, err
		}
		view := projectCandidateView(candidate)
		view.Diff = string(diff)
		return &ProjectCandidateResult{Candidate: &view}, nil
	case "apply", "discard":
	default:
		return nil, errors.New("action must be list, get, apply or discard")
	}
	if candidate.Disposition != "" {
		return nil, session.ErrCandidateDecided
	}
	disposition := session.CandidateDiscarded
	if p.Action == "apply" {
		disposition = session.CandidateApplied
		root, _, err := s.resolveSessionWorkspace("", candidate.BaseRepo)
		if err != nil {
			return nil, err
		}
		if err := worktree.ApplySnapshot(ctx, root, candidate.BaseRevision, candidate.Revision); err != nil {
			return nil, err
		}
	}
	decided, err := session.DecideCandidate(s.rt.SessionDir, candidate.SessionID, candidate.TurnID, disposition)
	if err != nil {
		return nil, err
	}
	title := metadata.Title
	notice := fmt.Sprintf("The user applied the candidate from session %q (turn %s) to the workspace.", title, decided.TurnID)
	if disposition == session.CandidateDiscarded {
		notice = fmt.Sprintf("The user discarded the candidate from session %q (turn %s).", title, decided.TurnID)
	}
	s.enqueueProjectInput(metadata.ParentID, metadata.ID, "project-candidate:"+decided.SessionID+":"+decided.TurnID+":"+disposition, notice)
	view := projectCandidateView(decided)
	return &ProjectCandidateResult{Candidate: &view}, nil
}
