package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/blueberrycongee/wuu/internal/channels"
	"github.com/blueberrycongee/wuu/internal/securefs"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/worktree"
)

type harnessReport struct {
	Result          string                        `json:"result"`
	ImplicitChoices []string                      `json:"implicit_choices"`
	EvidenceRefs    []string                      `json:"evidence_refs"`
	UnresolvedItems []string                      `json:"unresolved_items"`
	Decision        channels.VerificationDecision `json:"decision,omitempty"`
}

type workCandidate struct {
	SessionID    string        `json:"session_id"`
	TurnID       string        `json:"turn_id"`
	WorkID       string        `json:"work_id"`
	GoalRevision int           `json:"goal_revision"`
	Root         string        `json:"root"`
	BaseRepo     string        `json:"base_repo"`
	BaseRevision string        `json:"base_revision"`
	Revision     string        `json:"revision"`
	Diff         string        `json:"diff"`
	Report       harnessReport `json:"report"`
}

func (s *Server) completeHarnessWork(ctx context.Context, link channels.HarnessSessionLink, op channels.HarnessOperation, turn Turn, text string) error {
	if op.RunID == "" {
		return nil
	}
	client, err := s.channelService.BindAgent(ctx, link.AgentID)
	if err != nil {
		return err
	}
	work, err := client.GetWork(ctx, op.Actor.WorkID)
	if err != nil {
		return err
	}
	if work.GoalRevision != op.Actor.GoalRevision || work.State == channels.WorkCancelled {
		return nil
	}
	// A host timeout or goal correction already settled this execution.
	for _, run := range work.Runs {
		if run.ID == op.RunID && run.State != channels.WorkRunQueued && run.State != channels.WorkRunRunning && run.State != channels.WorkRunCompleted && run.State != channels.WorkRunFailed {
			return nil
		}
	}
	var report harnessReport
	state := channels.WorkRunCompleted
	if turn.Status != TurnStatusCompleted {
		state = channels.WorkRunState(turn.Status)
	}
	if state == channels.WorkRunCompleted {
		decoder := json.NewDecoder(strings.NewReader(text))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&report); err != nil || strings.TrimSpace(report.Result) == "" || report.ImplicitChoices == nil || report.EvidenceRefs == nil || report.UnresolvedItems == nil || decoder.Decode(new(any)) != io.EOF {
			state = channels.WorkRunFailed
			text = "Execution finished without the required result, implicit_choices, evidence_refs and unresolved_items report. Inspect the session before retrying."
		}
	}
	metadata, err := s.sharedHarnessSession(link.SessionID)
	if err != nil {
		return err
	}
	var artifact channels.WorkArtifact
	if state == channels.WorkRunCompleted && link.Purpose != channels.CollaborationSessionVerification {
		for _, existing := range work.Artifacts {
			if existing.RunID == op.RunID && existing.Kind == channels.WorkArtifactCandidate {
				artifact = existing
				break
			}
		}
		if artifact.ID == "" {
			candidate := workCandidate{SessionID: link.SessionID, TurnID: turn.ID, WorkID: work.ID, GoalRevision: work.GoalRevision, Root: metadata.CWD, BaseRepo: firstNonEmpty(metadata.WorktreeBaseRepo, metadata.CWD), BaseRevision: link.BaseRevision, Report: report}
			if candidate.BaseRevision != "" {
				candidate.Revision, candidate.Diff, err = worktree.Snapshot(ctx, metadata.CWD, link.BaseRevision, op.RunID)
				if err != nil {
					return err
				}
			}
			directory := filepath.Join(s.rt.SessionDir, link.SessionID, "candidates")
			path := filepath.Join(directory, op.RunID+".json")
			data, err := json.Marshal(candidate)
			if err != nil {
				return err
			}
			if err := securefs.WriteFileAtomic(path, data); err != nil {
				return err
			}
			artifact, err = client.AddWorkArtifact(ctx, channels.WorkArtifactAddParams{WorkID: work.ID, RunID: op.RunID, Kind: channels.WorkArtifactCandidate, URI: path, Label: work.Title, Summary: report.Result, WorkspaceRevision: candidate.Revision})
			if err != nil {
				return err
			}
		}
	}
	run, err := s.channelService.FinishHarnessWorkRun(ctx, link.SessionID, turn.ID, channels.WorkRunFinishParams{RunID: op.RunID, State: state, Outcome: text, Provider: turn.ModelProvider, Model: turn.Model, InputTokens: int64(turn.InputTokens), OutputTokens: int64(turn.OutputTokens), Qualified: artifact.ID != ""})
	if err != nil {
		return err
	}
	if state != channels.WorkRunCompleted {
		return nil
	}
	if link.Purpose == channels.CollaborationSessionVerification {
		if report.Decision != "pass" && report.Decision != "block" && report.Decision != "unknown" {
			report.Decision = channels.VerificationUnknown
			report.UnresolvedItems = append(report.UnresolvedItems, "Verifier did not provide a valid decision.")
		}
		// A persisted verification receipt is the idempotency boundary on replay.
		if existing, err := s.channelService.GetTaskVerification(ctx, work.ID); err == nil && existing.RunRef == run.ID {
			return nil
		} else if err != nil && !errors.Is(err, channels.ErrNotFound) {
			return err
		}
		_, err = client.SubmitTaskVerification(ctx, channels.TaskVerificationSubmitParams{RoomID: work.RoomID, TaskID: work.ID, GoalRevision: run.GoalRevision, CandidateRevision: run.CandidateRevision, Decision: report.Decision, Report: report.Result + "\n" + strings.Join(report.UnresolvedItems, "\n"), EvidenceRefs: report.EvidenceRefs, RunRef: run.ID})
		return err
	}
	if err := s.channelService.RecordHarnessDecisions(ctx, link.SessionID, run.ID, report.ImplicitChoices); err != nil {
		return err
	}
	// Preserve concurrently completed routes as reviewable alternatives. They
	// must not invalidate the independent review of the canonical candidate.
	if work.CandidateArtifactRef != "" && run.CandidateRevision != work.CandidateRevision && work.CandidateArtifactRef != artifact.ID {
		return nil
	}
	work, err = client.PromoteWorkCandidate(ctx, channels.WorkCandidatePromoteParams{WorkID: work.ID, RunID: run.ID, ArtifactRef: artifact.ID, RequestID: "harness-candidate:" + run.ID, SelectionReason: "Completed execution candidate ready for independent review"})
	if err != nil {
		return err
	}
	_, err = client.UpdateWorkEvidence(ctx, channels.WorkEvidenceUpdateParams{WorkID: work.ID, ChecksSummary: strings.Join(report.EvidenceRefs, "\n"), UnresolvedItems: strings.Join(report.UnresolvedItems, "\n")})
	if err != nil || !work.VerificationRequired {
		return err
	}
	p := channels.HarnessSessionParams{Action: "create", Purpose: channels.CollaborationSessionVerification, BaseRevision: artifact.WorkspaceRevision, WorkID: work.ID, WorkspaceRoot: firstNonEmpty(metadata.WorktreeBaseRepo, metadata.CWD), WorkspaceID: metadata.WorkspaceID, Title: "Verify: " + work.Title, Prompt: fmt.Sprintf("Independently review Work %s goal revision %d candidate revision %d against its requirements. Candidate revision: %s. Read the candidate and recorded check evidence. Do not repair it. Report pass, block or unknown with supporting evidence.\n\nProducer report:\n%s", work.ID, work.GoalRevision, work.CandidateRevision, artifact.WorkspaceRevision, text), OperationID: "verify:" + run.ID}
	if artifact.WorkspaceRevision != "" {
		p.Workspace = "worktree"
	}
	_, _, err = s.channelService.ReserveHarnessOperation(ctx, op.Actor, p, session.NewID(), 0)
	return err
}
