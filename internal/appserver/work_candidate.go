package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/blueberrycongee/wuu/internal/channels"
	"github.com/blueberrycongee/wuu/internal/worktree"
)

const MethodChannelWorkCandidate = "channel/work/candidate"

type ChannelWorkCandidateParams struct {
	WorkID           string `json:"work_id"`
	ArtifactID       string `json:"artifact_id"`
	Action           string `json:"action"`
	ExpectedRevision int    `json:"expected_revision,omitempty"`
}

func (s *Server) handleChannelWorkCandidate(ctx context.Context, req Request) error {
	if s.channelService == nil {
		return s.writeResponse(req.ID, nil, errors.New("collaboration is unavailable"))
	}
	var p ChannelWorkCandidateParams
	if err := decodeParams(req.Params, &p); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	s.controlMu.Lock()
	defer s.controlMu.Unlock()
	work, err := s.channelService.GetWork(ctx, p.WorkID)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	var artifact channels.WorkArtifact
	for _, a := range work.Artifacts {
		if a.ID == p.ArtifactID && a.Kind == channels.WorkArtifactCandidate {
			artifact = a
			break
		}
	}
	if artifact.ID == "" {
		return s.writeResponse(req.ID, nil, channels.ErrNotFound)
	}
	var run channels.WorkRun
	for _, r := range work.Runs {
		if r.ID == artifact.RunID {
			run = r
			break
		}
	}
	// Only host-created snapshots have the session/run storage contract.
	if run.SessionRef == "" || artifact.URI != filepath.Join(s.rt.SessionDir, run.SessionRef, "candidates", run.ID+".json") {
		return s.writeResponse(req.ID, nil, errors.New("candidate has no host snapshot"))
	}
	data, err := os.ReadFile(artifact.URI)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	var candidate workCandidate
	if err := json.Unmarshal(data, &candidate); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if candidate.WorkID != work.ID || candidate.SessionID != run.SessionRef || candidate.Revision != artifact.WorkspaceRevision {
		return s.writeResponse(req.ID, nil, channels.ErrConflict)
	}
	if p.Action != "get" {
		if p.ExpectedRevision < 1 || p.ExpectedRevision != work.Revision || candidate.GoalRevision != work.GoalRevision {
			return s.writeResponse(req.ID, nil, errors.New("Work changed; review the current candidate before deciding"))
		}
		if artifact.Disposition != "" {
			return s.writeResponse(req.ID, nil, channels.ErrConflict)
		}
		switch p.Action {
		case "apply":
			if work.State == channels.WorkCancelled || work.State == channels.WorkFailed {
				return s.writeResponse(req.ID, nil, errors.New("Work is no longer active"))
			}
			root, _, resolveErr := s.resolveSessionWorkspace("", candidate.BaseRepo)
			if resolveErr != nil {
				return s.writeResponse(req.ID, nil, resolveErr)
			}
			err = s.channelService.SetCandidateDisposition(ctx, work.ID, artifact.ID, "applied", p.ExpectedRevision, func() error {
				return worktree.ApplySnapshot(ctx, root, candidate.BaseRevision, candidate.Revision)
			})
			artifact.Disposition = "applied"
		case "discard":
			err = s.channelService.SetCandidateDisposition(ctx, work.ID, artifact.ID, "discarded", p.ExpectedRevision, nil)
			artifact.Disposition = "discarded"
		default:
			err = errors.New("candidate action must be get, apply or discard")
		}
		if err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
	}
	work, err = s.channelService.GetWork(ctx, p.WorkID)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	return s.writeResponse(req.ID, map[string]any{"candidate": candidate, "artifact": artifact, "work_revision": work.Revision, "stale": candidate.GoalRevision != work.GoalRevision}, nil)
}
