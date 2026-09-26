package appserver

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/worktree"
)

// captureCandidate freezes a worktree session's changes at the end of its
// latest turn. An older turn whose session moved on gets no candidate, and a
// new candidate supersedes the session's undecided ones: each is measured
// from the last delivered (applied or published) candidate, so it holds every
// change not yet delivered and a delivered change is never offered again.
func (s *Server) captureCandidate(th *threadState, turn Turn) (*session.Candidate, error) {
	current := func() bool {
		th.mu.Lock()
		defer th.mu.Unlock()
		return !th.running && len(th.Turns) > 0 && th.Turns[len(th.Turns)-1].ID == turn.ID
	}
	th.mu.Lock()
	root, baseHEAD, baseRepo, projectID := th.WorktreePath, th.WorktreeBaseHEAD, th.WorktreeBaseRepo, th.ProjectID
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
	if delivered, found, err := session.LatestDeliveredCandidate(s.rt.SessionDir, th.ID); err != nil {
		return nil, err
	} else if found {
		base = delivered.Revision
	}
	ctx := context.Background()
	revision, diff, err := worktree.Snapshot(ctx, root, base, th.ID+"-"+turn.ID)
	if err != nil || !current() {
		return nil, err
	}
	if strings.TrimSpace(diff) == "" {
		// Nothing is left to deliver, so an older proposal no longer matches
		// the session's worktree.
		if superseded, err := session.SupersedeCandidates(s.rt.SessionDir, th.ID); err != nil || superseded == 0 {
			return nil, err
		}
		s.publishProjectReview(projectID, th.ID)
		return nil, nil
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
	s.publishProjectReview(projectID, th.ID)
	return &candidate, nil
}

// publishProjectReview refreshes the pending counts of a managed session and
// its project and announces both, so clients show a candidate as soon as it
// is frozen or decided.
func (s *Server) publishProjectReview(projectID, sessionID string) {
	bySession, byProject, err := session.PendingCandidateCounts(s.rt.SessionDir, projectSessionSource)
	if err != nil {
		providers.DebugLogf("count pending candidates for project %q: %v", projectID, err)
		return
	}
	for id, count := range map[string]int{sessionID: bySession[sessionID], projectID: byProject[projectID]} {
		th := s.thread(id)
		if th == nil {
			continue
		}
		th.mu.Lock()
		th.PendingCandidates = count
		snapshot := th.snapshotLocked()
		th.mu.Unlock()
		_ = s.notifyThreadUpdated(snapshot)
	}
}

func projectCandidateView(candidate session.Candidate) ProjectCandidate {
	return ProjectCandidate{
		SessionID: candidate.SessionID, TurnID: candidate.TurnID, BaseRepo: candidate.BaseRepo,
		BaseRevision: candidate.BaseRevision, Revision: candidate.Revision, ChangedFiles: candidate.ChangedFiles,
		Disposition: candidate.Disposition, URL: candidate.URL, CreatedAt: candidate.CreatedAt,
	}
}

// handleProjectCandidate lists, reads and decides candidates. The user's
// decision is the delivery: applying writes only the frozen change into the
// workspace, and a conflict leaves both the workspace and the decision alone.
// Publishing happens in an extension; publish records its URL.
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
	case "apply", "discard", "publish":
	default:
		return nil, errors.New("action must be list, get, apply, discard or publish")
	}
	switch candidate.Disposition {
	case "":
	case session.CandidateSuperseded:
		return nil, session.ErrCandidateSuperseded
	default:
		return nil, session.ErrCandidateDecided
	}
	title := metadata.Title
	var disposition, cause, notice string
	switch p.Action {
	case "apply":
		root, _, err := s.resolveSessionWorkspace("", candidate.BaseRepo)
		if err != nil {
			return nil, err
		}
		if err := worktree.ApplySnapshot(ctx, root, candidate.BaseRevision, candidate.Revision); err != nil {
			return nil, err
		}
		disposition, cause = session.CandidateApplied, projectCauseApplied
		notice = fmt.Sprintf("The user applied the proposal from session %q (turn %s) to the workspace.", title, candidate.TurnID)
	case "discard":
		disposition, cause = session.CandidateDiscarded, projectCauseDiscarded
		notice = fmt.Sprintf("The user rejected the proposal from session %q (turn %s). Its changes stay in the session's worktree and will be in its next proposal unless the session reverts them.", title, candidate.TurnID)
	case "publish":
		url := strings.TrimSpace(p.URL)
		if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "http://") {
			return nil, errors.New("publish needs the http(s) URL the candidate was published to")
		}
		disposition, cause = session.CandidatePublished, projectCausePublished
		notice = fmt.Sprintf("The user published the proposal from session %q (turn %s) for review at %s. The session's later proposals start from it.", title, candidate.TurnID, url)
	}
	decided, err := session.DecideCandidate(s.rt.SessionDir, candidate.SessionID, candidate.TurnID, disposition, p.URL)
	if err != nil {
		return nil, err
	}
	s.publishProjectReview(metadata.ParentID, metadata.ID)
	s.enqueueProjectInput(metadata.ParentID, metadata.ID, "project-candidate:"+decided.SessionID+":"+decided.TurnID+":"+disposition, cause, notice, true)
	view := projectCandidateView(decided)
	return &ProjectCandidateResult{Candidate: &view}, nil
}
