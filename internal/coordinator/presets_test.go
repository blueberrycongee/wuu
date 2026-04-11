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
		".wuu/shared/",       // the shared filesystem region
		"send_message",       // the control channel
		"trajector",          // trajectories as history (matches "trajectory" / "trajectories")
	} {
		if !strings.Contains(preamble, want) {
			t.Errorf("SystemPromptPreamble missing three-plane reference %q", want)
		}
	}
}

func TestSystemPromptPreamble_TeachesSpawnVsFork(t *testing.T) {
	// The preamble must give the model enough to choose between
	// spawn (clean room) and fork (state inheritance). The
	// "100-word rule" is the load-bearing heuristic; pin it so a
	// casual edit doesn't quietly drop it.
	preamble := SystemPromptPreamble()
	if !strings.Contains(preamble, "spawn") || !strings.Contains(preamble, "fork") {
		t.Error("SystemPromptPreamble must teach spawn vs fork")
	}
	if !strings.Contains(preamble, "100 words") && !strings.Contains(preamble, "100-word") {
		t.Error("SystemPromptPreamble missing the 100-word spawn/fork heuristic")
	}
}
