package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentcontrol"
	"github.com/blueberrycongee/wuu/internal/agentthread"
	"github.com/blueberrycongee/wuu/internal/process"
	"github.com/blueberrycongee/wuu/internal/subagent"
	"github.com/blueberrycongee/wuu/internal/tools"
)

const fusionLeadPrompt = `You are the Lead in Fusion mode. Own the user conversation, planning, difficult decisions, and final acceptance. Use fusion_delegate for substantial bounded implementation work, with a self-contained brief and explicit acceptance checks. The Sidekick has separate persistent context and shares your workspace. Further calls send feedback or new work to the same Sidekick. Small tasks may be completed directly.
Delegation waits for the Sidekick to stop before returning. Do not combine delegation with other tool calls or leave background writers active. Treat its report as a claim: inspect actual files/diffs and run appropriate checks before acceptance. For blocked, needs_decision, failed, or an invalid report, use the evidence to provide specific feedback or take over with your own tools. Do not repeat an unchanged brief indefinitely. Never expose internal JSON reports as the final user answer; summarize the verified outcome and any remaining limitations.`

type fusionReport struct {
	Outcome      string   `json:"outcome"`
	Summary      string   `json:"summary"`
	ChangedFiles []string `json:"changed_files,omitempty"`
	Checks       []string `json:"checks,omitempty"`
	Blockers     []string `json:"blockers,omitempty"`
	Questions    []string `json:"questions,omitempty"`
	NextSteps    []string `json:"next_steps,omitempty"`
}

type fusionResult struct {
	fusionReport
	AgentID             string `json:"agent_id"`
	Provider            string `json:"provider"`
	Model               string `json:"model"`
	DurationMS          int64  `json:"duration_ms"`
	InputTokens         int    `json:"input_tokens"`
	OutputTokens        int    `json:"output_tokens"`
	CacheReadTokens     int    `json:"cache_read_tokens"`
	CacheCreationTokens int    `json:"cache_creation_tokens"`
}

// delegateFusion is called under the Lead toolkit's exclusive execution gate.
// Only final reports cross the context boundary; worker history stays private
// to the existing durable worker runtime.
func (rt *ThreadRuntime) delegateFusion(ctx context.Context, brief string) (output string, err error) {
	control := rt.AgentControl
	const taskPath = agentthread.RootPath + "/" + agentcontrol.FusionSidekickType
	var id string
	var before subagent.SubAgentSnapshot
	started := time.Now()
	meta, found, err := control.FindThread(taskPath)
	if err != nil {
		return "", err
	}
	if found {
		if meta.AgentProfile != "" || meta.Role != agentcontrol.FusionSidekickType {
			return "", fmt.Errorf("Fusion Sidekick path is owned by another worker")
		}
		id = meta.ID
		if err := rt.settleFusionSidekick(ctx, id); err != nil {
			return "", &tools.FusionHandoffError{Err: err}
		}
		if worker := control.Manager().Get(id); worker != nil {
			before = worker.Snapshot()
		}
	}
	// Followup deliberately detaches cancellation in the generic worker API.
	// Fusion restores ownership here: stop and drain before releasing the gate.
	defer func() {
		if id == "" {
			return
		}
		if settleErr := rt.settleFusionSidekick(ctx, id); settleErr != nil {
			err = errors.Join(err, &tools.FusionHandoffError{Err: settleErr})
		}
	}()
	if id == "" {
		spawned, spawnErr := control.Spawn(ctx, agentcontrol.SpawnRequest{
			Type: agentcontrol.FusionSidekickType, TaskName: agentcontrol.FusionSidekickType, Description: "Fusion Sidekick",
			Prompt: brief, ParentID: control.SessionID(), ParentPath: agentthread.RootPath,
			Isolation: string(agentcontrol.IsolationInplace),
		})
		if spawnErr != nil {
			return "", spawnErr
		}
		id = spawned.AgentID
	} else {
		resumed, resumeErr := control.FollowupTask(ctx, id, brief)
		if resumeErr != nil {
			return "", resumeErr
		}
		// Restored workers are loaded lazily by FollowupTask. Counters remain
		// cumulative, so read that baseline before waiting for the new run.
		if before.ID == "" {
			before = resumed
		}
	}
	if _, err := control.AwaitFrom(agentthread.RootPath, ctx, []string{id}); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	snap, err := control.Wait(ctx, id)
	if err != nil {
		return "", err
	}
	result := fusionResult{
		AgentID: id, Provider: snap.ResolvedProvider, Model: snap.ResolvedModel,
		DurationMS:  time.Since(started).Milliseconds(),
		InputTokens: snap.InputTokens - before.InputTokens, OutputTokens: snap.OutputTokens - before.OutputTokens,
		CacheReadTokens: snap.CacheReadTokens - before.CacheReadTokens, CacheCreationTokens: snap.CacheCreationTokens - before.CacheCreationTokens,
	}
	if parseErr := json.Unmarshal([]byte(strings.TrimSpace(snap.Result)), &result.fusionReport); parseErr != nil || !validFusionOutcome(result.Outcome) || strings.TrimSpace(result.Summary) == "" {
		result.fusionReport = fusionReport{Outcome: "failed", Summary: "Sidekick returned an invalid report. Inspect its work before accepting or taking over.", Blockers: []string{snap.Result}}
	}
	if snap.Status != subagent.StatusCompleted {
		result.Outcome = "failed"
		if snap.Error != nil {
			result.Blockers = append(result.Blockers, snap.Error.Error())
		}
	}
	data, err := json.Marshal(result)
	return string(data), err
}

// SettleFusion drains work left by a cancelled or interrupted delegation before
// the next Lead turn. The caller must hold the conversation execution lease.
func (rt *ThreadRuntime) SettleFusion(ctx context.Context) error {
	meta, found, err := rt.AgentControl.FindThread(agentthread.RootPath + "/" + agentcontrol.FusionSidekickType)
	if err != nil {
		return err
	}
	if found {
		if meta.AgentProfile != "" || meta.Role != agentcontrol.FusionSidekickType {
			return fmt.Errorf("Fusion Sidekick path is owned by another worker")
		}
		if err := rt.settleFusionSidekick(ctx, meta.ID); err != nil {
			return err
		}
	}
	rt.Toolkit.ClearFusionHandoff()
	return nil
}

func (rt *ThreadRuntime) settleFusionSidekick(ctx context.Context, id string) error {
	rt.AgentControl.Stop(id)
	var err error
	if rt.AgentControl.Manager().Get(id) != nil {
		_, err = rt.AgentControl.Wait(context.WithoutCancel(ctx), id)
	}
	for _, snap := range rt.AgentControl.List() {
		if snap.ID == id && !subagent.IsTerminal(snap.Status) {
			err = errors.Join(err, fmt.Errorf("Sidekick is still %s", snap.Status))
		}
	}
	if rt.ProcessManager != nil {
		processes, listErr := rt.ProcessManager.List()
		err = errors.Join(err, listErr)
		for _, p := range processes {
			if p.OwnerKind == process.OwnerSubagent && p.OwnerID == id {
				_, stopErr := rt.ProcessManager.Stop(p.ID)
				err = errors.Join(err, stopErr)
			}
		}
	}
	return err
}

func validFusionOutcome(outcome string) bool {
	switch outcome {
	case "completed", "blocked", "needs_decision", "failed":
		return true
	default:
		return false
	}
}
