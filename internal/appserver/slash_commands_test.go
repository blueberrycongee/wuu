package appserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/skills"
	"github.com/blueberrycongee/wuu/internal/tools"
)

func TestRenderLightweightSlashCommandPrompt(t *testing.T) {
	content, display, ok := renderLightweightSlashCommandPrompt("/debug login failure")
	if !ok {
		t.Fatal("expected /debug to render")
	}
	if display != "/debug login failure" {
		t.Fatalf("display = %q", display)
	}
	if !strings.Contains(content, "login failure") {
		t.Fatalf("rendered prompt missing arguments:\n%s", content)
	}
	if strings.Contains(content, "/debug") {
		t.Fatalf("rendered prompt should not include raw slash command:\n%s", content)
	}
}

func TestRenderLightweightSlashCommandPromptLeavesCompactRaw(t *testing.T) {
	content, display, ok := renderLightweightSlashCommandPrompt("/compact")
	if ok || display != "" || content != "/compact" {
		t.Fatalf("compact slash should not render as prompt, got content=%q display=%q ok=%v", content, display, ok)
	}
}

func TestIsManualCompactPrompt(t *testing.T) {
	cases := []struct {
		prompt string
		want   bool
	}{
		{"/compact", true},
		{"/compact 后续只保留结论", true},
		{"/COMPACT", true},
		{"/compress", true},
		{"//compact", false},
		{"/compaction", false},
		{"compact", false},
		{"/debug compact the logs", false},
		{"  /compact  ", true},
	}
	for _, c := range cases {
		if got := isManualCompactPrompt(c.prompt); got != c.want {
			t.Errorf("isManualCompactPrompt(%q) = %v, want %v", c.prompt, got, c.want)
		}
	}
}

func TestRenderLightweightSlashCommandPromptKeepsSkillSlashRaw(t *testing.T) {
	content, display, ok := renderLightweightSlashCommandPrompt("/slides quarterly roadmap")
	if ok || display != "" || content != "/slides quarterly roadmap" {
		t.Fatalf("skill slash should remain raw, got content=%q display=%q ok=%v", content, display, ok)
	}
}

func TestRenderLightweightSlashCommandPromptIgnoresEscapedSlash(t *testing.T) {
	content, display, ok := renderLightweightSlashCommandPrompt("//debug login failure")
	if ok || display != "" || content != "//debug login failure" {
		t.Fatalf("escaped slash should remain raw, got content=%q display=%q ok=%v", content, display, ok)
	}
}

func TestExplicitSkillIdentityRejectsUnavailableSelection(t *testing.T) {
	skill := skills.Skill{Name: "compact", Source: "user", Path: "skills/compact/SKILL.md", UserInvocable: true, Content: "instructions"}
	for _, tc := range []struct {
		name, prompt string
		invocable    bool
	}{
		{"malformed", "/skill not-json", true},
		{"missing", `/skill {"name":"absent","source":"user","path":"skills/compact/SKILL.md"}`, true},
		{"changed-source", `/skill {"name":"compact","source":"project","path":"skills/compact/SKILL.md"}`, true},
		{"changed-path", `/skill {"name":"compact","source":"user","path":"other/SKILL.md"}`, true},
		{"not-user-invocable", `/skill {"name":"compact","source":"user","path":"skills/compact/SKILL.md"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := newTestRuntime(t, &fakeClient{})
			skill.UserInvocable = tc.invocable
			rt.Skills = []skills.Skill{skill}
			srv := New(rt, &lockedBuffer{})
			if _, err := srv.userMessageWithInputImages("", tc.prompt, nil, nil, nil); err == nil {
				t.Fatal("invalid explicit selection must not fall through to model interpretation")
			}
		})
	}
}

func TestExplicitSkillIdentityLoadsWithoutExecutingInlineShell(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	skill := skills.Skill{Name: "review", Source: "bundled", UserInvocable: true, Content: "`!echo do-not-execute` ${ARGUMENTS}"}
	rt.Skills = []skills.Skill{skill}
	srv := New(rt, &lockedBuffer{})
	identity, _ := json.Marshal(map[string]string{"name": skill.Name, "source": skill.Source, "path": skill.Path})
	prompt := "/skill " + string(identity) + "\nnotes"
	msg, err := srv.userMessageWithInputImages("", prompt, nil, nil, nil)
	if err != nil || !strings.Contains(msg.Content, "`!echo do-not-execute` notes") || msg.DisplayContent != prompt {
		t.Fatalf("loading should only expand instructions: %+v, %v", msg, err)
	}
}

func TestExplicitSkillCatalogHonorsThreadBoundaries(t *testing.T) {
	for _, mode := range []string{"restricted-surface", "external-root", "external-checkout"} {
		t.Run(mode, func(t *testing.T) {
			rt := newTestRuntime(t, &fakeClient{})
			skill := skills.Skill{Name: "review", Source: "user", UserInvocable: true, AllowedTools: []string{"bash"}, Content: "selected instructions"}
			rt.Skills = []skills.Skill{skill}
			kit, err := tools.New(rt.RootDir)
			if err != nil {
				t.Fatal(err)
			}
			rt.Toolkit = kit
			out := &lockedBuffer{}
			srv := New(rt, out)
			dispatchPayload(t, srv, "start", MethodThreadStart, ThreadStartParams{})
			threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "start")["result"]).Thread.ID
			th := srv.thread(threadID)
			if mode == "restricted-surface" {
				thread, err := srv.ensureThreadRuntime(th)
				if err != nil {
					t.Fatal(err)
				}
				thread.Toolkit.ConfigureSurfaceForProviderModel("ollama", "llama-coder", true)
			} else {
				th.EngineID = "external-fixture"
				if mode == "external-checkout" {
					th.CWD = t.TempDir()
					th.WorkspaceID = ""
				}
			}
			dispatchPayload(t, srv, "list", MethodSkillList, SkillListParams{ThreadID: threadID})
			response := responseByID(t, parseOutput(t, out.String()), "list")
			if response["error"] != nil {
				t.Fatalf("skill/list: %+v", response)
			}
			catalog := remarshal[SkillListResult](t, response["result"])
			wantAvailable := mode == "external-root"
			if (len(catalog.Skills) == 1) != wantAvailable {
				t.Fatalf("unexpected catalog: %+v", catalog)
			}
			_, err = srv.userMessageWithInputImages(threadID, `/skill {"name":"review","source":"user","path":""}`, nil, nil, nil)
			if (err == nil) != wantAvailable {
				t.Fatalf("unexpected invocation error: %v", err)
			}
		})
	}
}

func TestExplicitSkillSteeringUsesLoadedInstructions(t *testing.T) {
	client := &fakeClient{responses: []providers.ChatResponse{
		{ToolCalls: []providers.ToolCall{{ID: "wait", Name: "wait_for_steer", Arguments: `{}`}}},
		{Content: "done"},
	}}
	rt := newTestRuntime(t, client)
	rt.Skills = []skills.Skill{{Name: "compact", Source: "user", UserInvocable: true, Content: "selected steering instructions ${ARGUMENTS}"}}
	blocking := newBlockingToolExecutor()
	rt.StreamRunner.Tools = blocking
	out := &lockedBuffer{}
	srv := New(rt, out)
	dispatchPayload(t, srv, "start", MethodThreadStart, ThreadStartParams{})
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "start")["result"]).Thread.ID
	dispatchPayload(t, srv, "turn", MethodTurnStart, TurnStartParams{ThreadID: threadID, Prompt: "start"})
	started := remarshal[TurnStartResult](t, responseByID(t, parseOutput(t, out.String()), "turn")["result"])
	<-blocking.started
	prompt := `/skill {"name":"compact","source":"user","path":""}` + "\nnotes"
	dispatchPayload(t, srv, "steer", MethodTurnSteer, TurnSteerParams{ThreadID: threadID, ExpectedTurnID: started.Turn.ID, Prompt: prompt})
	response := responseByID(t, parseOutput(t, out.String()), "steer")
	if response["error"] != nil {
		t.Fatalf("turn/steer: %+v", response)
	}
	close(blocking.release)
	waitForMethod(t, out, NotificationTurnCompleted)
	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.requests) != 2 {
		t.Fatalf("requests = %d", len(client.requests))
	}
	for _, msg := range client.requests[1].Messages {
		if msg.Steered && msg.DisplayContent == prompt && strings.Contains(msg.Content, "selected steering instructions notes") {
			return
		}
	}
	t.Fatal("steering did not deliver selected instructions")
}

func TestProjectSkillIdentityBoundaries(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	if err := os.Mkdir(filepath.Join(rt.RootDir, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	instructionPath := filepath.Join(rt.RootDir, ".agents", "skills", "review", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(instructionPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(instructionPath, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	rt.Skills = []skills.Skill{{Name: "review", Source: "project", Path: instructionPath, Dir: filepath.Dir(instructionPath), UserInvocable: true, Content: "project instructions ${ARGUMENTS}"}}
	srv := New(rt, &lockedBuffer{})
	summary := srv.skillSummaries(rt.Skills, rt.RootDir)[0]
	if summary.Project == nil {
		t.Fatal("owned skill missing project identity")
	}
	owner := summary.Project.Root
	for _, tc := range []struct {
		name, root, path, source, absolute string
		valid                              bool
	}{
		{"owned", owner, ".agents/skills/review/SKILL.md", "project", "", true},
		{"other-project", t.TempDir(), ".agents/skills/review/SKILL.md", "project", "", false},
		{"traversal", owner, "../.agents/skills/review/SKILL.md", "project", "", false},
		{"normalized-traversal", owner, ".agents/../.agents/skills/review/SKILL.md", "project", "", false},
		{"backslash", owner, `.agents\skills\review\SKILL.md`, "project", "", false},
		{"absolute", owner, instructionPath, "project", "", false},
		{"mixed", owner, ".agents/skills/review/SKILL.md", "project", instructionPath, false},
		{"wrong-source", owner, ".agents/skills/review/SKILL.md", "user", "", false},
		{"missing", owner, ".agents/skills/missing/SKILL.md", "project", "", false},
		{"wrong-name", owner, ".agents/skills/review/SKILL.md", "project", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			identity := map[string]any{"name": "review", "source": tc.source, "project": map[string]string{"root": tc.root, "path": tc.path}}
			if tc.name == "wrong-name" {
				identity["name"] = "missing"
			}
			if tc.absolute != "" {
				identity["path"] = tc.absolute
			}
			encoded, _ := json.Marshal(identity)
			msg, err := srv.userMessageWithInputImages("", "/skill "+string(encoded)+"\nnotes", nil, nil, nil)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v, discovered=%+v", tc.valid, err, projectSkillIdentity(rt.Skills[0], owner, rt.RootDir))
			}
			if tc.valid && !strings.Contains(msg.Content, "project instructions notes") {
				t.Fatalf("wrong instructions: %s", msg.Content)
			}
		})
	}
}

func TestProjectSkillCatalogOwnership(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	if err := os.Mkdir(filepath.Join(rt.RootDir, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(rt.RootDir, ".agents", "skills", "review")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(directory, "SKILL.md")
	if err := os.WriteFile(file, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	skill := skills.Skill{Name: "review", Source: "project", Path: file, Dir: directory, UserInvocable: true, Content: "owned instructions"}
	rt.Skills = []skills.Skill{skill}
	srv := New(rt, &lockedBuffer{})
	for _, source := range []string{"user", "plugin:fixture"} {
		candidate := skill
		candidate.Source = source
		summary := srv.skillSummaries([]skills.Skill{candidate}, rt.RootDir)[0]
		if summary.Project != nil || summary.Path != file {
			t.Fatalf("external source was rebound: %+v", summary)
		}
		rt.Skills = []skills.Skill{candidate}
		identity, _ := json.Marshal(map[string]string{"name": candidate.Name, "source": source, "path": file})
		if _, err := srv.userMessageWithInputImages("", "/skill "+string(identity), nil, nil, nil); err != nil {
			t.Fatalf("absolute external identity failed: %v", err)
		}
	}
	rt.Skills = []skills.Skill{skill}
	subdirectory := filepath.Join(rt.RootDir, "subpackage")
	if err := os.Mkdir(subdirectory, 0700); err != nil {
		t.Fatal(err)
	}
	workspace := rt.RootDir
	rt.RootDir = subdirectory
	summary := srv.skillSummaries(rt.Skills, subdirectory)[0]
	if summary.Project == nil || summary.Project.Root != sessionWorkspacePath(subdirectory) || summary.Project.Path != ".agents/skills/review/SKILL.md" {
		t.Fatalf("ancestor skill lost checkout-relative identity: %+v", summary)
	}
	rt.RootDir = workspace

	foreign := t.TempDir()
	if err := os.Mkdir(filepath.Join(foreign, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(foreign, "SKILL.md")
	if err := os.WriteFile(outside, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(directory, "linked.md")
	if err := os.Symlink(outside, link); err == nil {
		escaped := skill
		escaped.Path = link
		if got := srv.skillSummaries([]skills.Skill{escaped}, workspace)[0]; got.Project != nil {
			t.Fatalf("escaped skill acquired project ownership: %+v", got)
		}
		rt.Skills = []skills.Skill{escaped}
		identity, _ := json.Marshal(map[string]any{"name": "review", "source": "project", "project": map[string]string{"root": sessionWorkspacePath(workspace), "path": ".agents/skills/review/linked.md"}})
		if _, err := srv.userMessageWithInputImages("", "/skill "+string(identity), nil, nil, nil); err == nil {
			t.Fatal("escaped symlink matched a project-relative selection")
		}
		rt.Skills = []skills.Skill{skill}
	}
	kit, err := tools.New(foreign)
	if err != nil {
		t.Fatal(err)
	}
	other := skill
	other.Path = outside
	other.Dir = foreign
	kit.SetSkills([]skills.Skill{other})
	srv.threads["foreign"] = &threadState{ID: "foreign", CWD: foreign, EngineID: "wuu", running: true, execRuntime: &runtime.ThreadRuntime{Toolkit: kit}}
	catalog, root, err := srv.skillCatalog("foreign")
	if err != nil || len(catalog) != 1 || root != "" {
		t.Fatalf("foreign checkout inherited ownership: root=%q catalog=%+v error=%v", root, catalog, err)
	}
	identity, _ := json.Marshal(map[string]any{"name": "review", "source": "project", "project": map[string]string{"root": sessionWorkspacePath(workspace), "path": "SKILL.md"}})
	if _, err := srv.userMessageWithInputImages("foreign", "/skill "+string(identity), nil, nil, nil); err == nil {
		t.Fatal("foreign checkout accepted originating project identity")
	}
}
