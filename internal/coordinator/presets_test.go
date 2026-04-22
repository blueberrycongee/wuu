package coordinator

import (
	"strings"
	"testing"
)

// The presets are constants meant to be pasted into worker prompts
// verbatim. These tests pin the load-bearing lines so a casual edit
// can't accidentally weaken the failure-mode-specific guarantees
// the prompts encode (see presets.go for the rationale on each).

func TestVerificationPreset_HasFrameInversion(t *testing.T) {
	// The "NOT to confirm... TRY TO BREAK IT" line is what flips the
	// model out of helpful-confirmer mode. If this disappears the
	// preset stops doing its main job.
	if !strings.Contains(VerificationPreset, "NOT to confirm") {
		t.Error("VerificationPreset is missing the frame-inversion line")
	}
	if !strings.Contains(VerificationPreset, "TRY TO BREAK IT") {
		t.Error("VerificationPreset is missing the TRY TO BREAK IT directive")
	}
}

func TestVerificationPreset_RequiresEvidence(t *testing.T) {
	// "Never write PASS without evidence" is the rule that prevents
	// the verifier from rubber-stamping. Without it the model will
	// declare PASS based on plausibility instead of command output.
	if !strings.Contains(VerificationPreset, `Never write "PASS"`) {
		t.Error("VerificationPreset must require evidence for PASS verdicts")
	}
}

func TestVerificationPreset_VerdictFormat(t *testing.T) {
	// The coordinator may grep for VERDICT lines when synthesizing
	// across multiple verifiers, so the format strings must stay
	// stable. Pin all three.
	for _, want := range []string{"VERDICT: PASS", "VERDICT: FAIL", "VERDICT: PARTIAL"} {
		if !strings.Contains(VerificationPreset, want) {
			t.Errorf("VerificationPreset missing verdict literal %q", want)
		}
	}
}

func TestResearchPreset_IsReadOnly(t *testing.T) {
	// The hard "do not mutate" rule is what makes this preset safe
	// to use in a worker that has full write tools — without it
	// the model will sometimes run installs or commits "as part of
	// the investigation".
	if !strings.Contains(ResearchPreset, "Do NOT modify") {
		t.Error("ResearchPreset must forbid modifications")
	}
	if !strings.Contains(ResearchPreset, "do not mutate") &&
		!strings.Contains(ResearchPreset, "mutate state") {
		t.Error("ResearchPreset must forbid state-mutating commands")
	}
}

func TestResearchPreset_RequiresFileLineCitations(t *testing.T) {
	// The coordinator turns research output into follow-up specs;
	// without file:line refs it has nothing concrete to act on.
	if !strings.Contains(ResearchPreset, "file:line") {
		t.Error("ResearchPreset must require file:line citations")
	}
}

func TestResearchPreset_OutputShape(t *testing.T) {
	// The three-section shape (Answer / Evidence / Notes) is what
	// the coordinator relies on when reading multiple parallel
	// research results. Pin the headers.
	for _, want := range []string{"## Answer", "## Evidence", "## Notes"} {
		if !strings.Contains(ResearchPreset, want) {
			t.Errorf("ResearchPreset missing required section %q", want)
		}
	}
}

func TestSystemPromptPreamble_TeachesThreePlanes(t *testing.T) {
	// The three-plane discipline (filesystem for data, send_message
	// for control, trajectories for history) is the load-bearing
	// instruction in the new preamble. Without it the model defaults
	// to chat-era habits and stuffs everything into messages.
	preamble := SystemPromptPreamble()
	for _, want := range []string{
		".wuu/shared/", // the shared filesystem region
		"send_message", // the control channel
		"trajector",    // trajectories as history (matches "trajectory" / "trajectories")
	} {
		if !strings.Contains(preamble, want) {
			t.Errorf("SystemPromptPreamble missing three-plane reference %q", want)
		}
	}
}

func TestSystemPromptPreamble_TeachesSpawnVsFork(t *testing.T) {
	// The preamble must give the model enough to choose between
	// spawn (clean room) and fork (state inheritance). The
	// "context fidelity vs signal-to-noise" framing is the
	// load-bearing concept; pin it so a casual edit doesn't
	// quietly drop it.
	preamble := SystemPromptPreamble()
	if !strings.Contains(preamble, "spawn") || !strings.Contains(preamble, "fork") {
		t.Error("SystemPromptPreamble must teach spawn vs fork")
	}
	if !strings.Contains(preamble, "context fidelity") {
		t.Error("SystemPromptPreamble missing the context-fidelity spawn/fork framing")
	}
	if !strings.Contains(preamble, "context-independent") || !strings.Contains(preamble, "context-sensitive") {
		t.Error("SystemPromptPreamble missing the context-independent / context-sensitive decision criteria")
	}
}

func TestSystemPromptPreamble_StatesMainAgentToolLimits(t *testing.T) {
	preamble := SystemPromptPreamble()
	for _, want := range []string{
		"read-oriented",
		"does NOT have direct `write_file`, `edit_file`, or `run_shell` tools",
		"delegate that step to a worker",
		"create or update that file via a worker",
		"Read file contents with `read_file`",
		"Search for files with `glob`",
		"Search file contents with `grep`",
		"Use `run_shell` only for work that genuinely requires a shell",
		"If multiple tool calls are independent, make them in parallel",
	} {
		if !strings.Contains(preamble, want) {
			t.Errorf("SystemPromptPreamble missing main-agent constraint %q", want)
		}
	}
}

func TestSystemPromptPreamble_TeachesNonInteractiveGit(t *testing.T) {
	preamble := SystemPromptPreamble()
	for _, want := range []string{
		"non-interactive environment",
		"`git commit -m`",
		"`git commit -e`",
		"`git rebase -i`",
		"`git add -i`",
	} {
		if !strings.Contains(preamble, want) {
			t.Errorf("SystemPromptPreamble missing non-interactive git guidance %q", want)
		}
	}
}

func TestSystemPromptPreamble_TeachesWorkerResultSynthesisDiscipline(t *testing.T) {
	preamble := SystemPromptPreamble()
	for _, want := range []string{
		"Workers cannot see your main conversation",
		"standalone spec",
		"self-contained",
		"based on your findings",
		"based on the research",
		"specific file paths, specific line numbers, exactly what to change, what constraints must hold, and what counts as done",
	} {
		if !strings.Contains(preamble, want) {
			t.Errorf("SystemPromptPreamble missing worker-result synthesis guidance %q", want)
		}
	}
}

func TestSystemPromptPreamble_TeachesReadEnoughBeforeActing(t *testing.T) {
	preamble := SystemPromptPreamble()
	for _, want := range []string{
		"Read enough to understand before acting",
		"Do not propose changes",
		"delegate execution before you have that grounding",
		"Keep it proportionate",
		"For a very small, very explicit, local task in one obvious file, a light scan is enough",
		"once you've done the minimum reading needed",
	} {
		if !strings.Contains(preamble, want) {
			t.Errorf("SystemPromptPreamble missing read-before-acting guidance %q", want)
		}
	}
}

func TestSystemPromptPreamble_TeachesUserCommunicationDiscipline(t *testing.T) {
	preamble := SystemPromptPreamble()
	for _, want := range []string{
		"Before your first tool call, give one short sentence",
		"send short updates at meaningful moments",
		"rejoin cold",
		"No fluff",
		"Don't force headers, lists, or tables",
	} {
		if !strings.Contains(preamble, want) {
			t.Errorf("SystemPromptPreamble missing user-communication guidance %q", want)
		}
	}
}

func TestSystemPromptPreamble_TeachesAlignmentBeforeDelegation(t *testing.T) {
	// The preamble must require alignment before delegation, with a clear
	// exception for preliminary / draft work so the model doesn't block
	// on alignment when the user explicitly asked for a rough pass.
	preamble := SystemPromptPreamble()
	for _, want := range []string{
		"Intent alignment",
		"Context alignment",
		"Before spawning any worker",
		"preliminary result",
	} {
		if !strings.Contains(preamble, want) {
			t.Errorf("SystemPromptPreamble missing alignment-before-delegation phrase %q", want)
		}
	}
}

func TestComposeWorkerSystemPrompt_OverridesInheritedMainAgentLimits(t *testing.T) {
	wt, err := LookupWorkerType("worker")
	if err != nil {
		t.Fatalf("LookupWorkerType(worker): %v", err)
	}
	got := composeWorkerSystemPrompt("You are wuu, a pragmatic CLI coding assistant. Use tools to make real changes.", wt, "/tmp/repo", IsolationInplace)
	if !strings.Contains(got, "Worker override:") {
		t.Fatalf("worker system prompt missing inherited-limit override: %q", got)
	}
	if !strings.Contains(got, "If a tool is in your tool list") {
		t.Fatalf("worker system prompt must restore access to worker tools: %q", got)
	}
}

func TestComposeWorkerSystemPrompt_TeachesNonInteractiveGit(t *testing.T) {
	wt, err := LookupWorkerType("worker")
	if err != nil {
		t.Fatalf("LookupWorkerType(worker): %v", err)
	}
	got := composeWorkerSystemPrompt("", wt, "/tmp/repo", IsolationInplace)
	for _, want := range []string{
		"Treat shell commands as non-interactive",
		"`git commit -m`",
		"`git commit -e`",
		"`git rebase -i`",
		"`git add -i`",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("worker system prompt missing non-interactive git guidance %q", want)
		}
	}
}
