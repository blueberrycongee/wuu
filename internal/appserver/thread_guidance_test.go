package appserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/providers"
	wuuRuntime "github.com/blueberrycongee/wuu/internal/runtime"
)

func TestWorktreeProtocolGuidanceUsesSelectedCheckout(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	var worktreeID string
	if err := os.MkdirAll(repo, 0700); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=Audit Fixture", "-c", "user.email=audit@example.invalid", "-c", "core.hooksPath=/dev/null"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	write := func(path, body string) {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-b", "main")
	write(filepath.Join(repo, "AGENTS.md"), "# Project rules\nFIXTURE_SELECTED_CHECKOUT_RULE: Use checkout B instructions.\n")
	write(filepath.Join(repo, ".agents/skills/release-check/SKILL.md"), "---\nname: release-check\ndescription: Check this checkout release\n---\nFIXTURE_SELECTED_CHECKOUT_SKILL: Use checkout B release policy.\nResource base: ${CLAUDE_SKILL_DIR}\n")
	git("add", "AGENTS.md", ".agents/skills/release-check/SKILL.md")
	git("commit", "-m", "test: create selected checkout rules")
	git("branch", "selected-rules")
	write(filepath.Join(repo, "AGENTS.md"), "# Project rules\nFIXTURE_ORIGINAL_WORKSPACE_RULE: Use workspace A instructions.\n")
	write(filepath.Join(repo, ".agents/skills/release-check/SKILL.md"), "---\nname: release-check\ndescription: Check this workspace release\n---\nFIXTURE_ORIGINAL_WORKSPACE_SKILL: Use workspace A release policy.\nResource base: ${CLAUDE_SKILL_DIR}\n")
	git("add", "AGENTS.md", ".agents/skills/release-check/SKILL.md")
	git("commit", "-m", "test: create original workspace rules")

	home := filepath.Join(root, "home")
	for _, item := range []struct{ key, value string }{
		{"HOME", home}, {"USERPROFILE", home}, {"WUU_HOME", filepath.Join(home, ".wuu")},
		{"CODEX_HOME", filepath.Join(home, ".codex")}, {"XDG_CONFIG_HOME", filepath.Join(home, ".config")},
	} {
		t.Setenv(item.key, item.value)
	}
	write(filepath.Join(home, ".wuu/AGENTS.md"), "GLOBAL_GUIDANCE_MARKER")
	write(filepath.Join(home, ".agents/skills/global-policy/SKILL.md"), "---\nname: global-policy\ndescription: Global policy\n---\nGLOBAL_SKILL_MARKER")
	cfg := config.Config{DefaultProvider: "audit", Providers: map[string]config.ProviderConfig{
		"audit": {Type: "openai-compatible", BaseURL: "http://127.0.0.1:1/v1", APIKey: "synthetic-unused", Model: "gpt-test"},
	}}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(home, "config.json"), string(data))
	rt, err := wuuRuntime.NewSession(wuuRuntime.Options{
		RootDir: repo, HomeDir: home, ConfigPath: filepath.Join(home, "config.json"), SafeMode: true,
		Config: cfg,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Cleanup()
	out := &lockedBuffer{}
	srv := New(rt, out)
	defer srv.Close()

	for _, tc := range []struct {
		name, cwd    string
		wantSelected bool
	}{
		{"same-root-control", repo, false}, {"selected-worktree", "", true},
	} {
		params := ThreadStartParams{}
		if tc.wantSelected {
			params.Workspace = "worktree"
			params.BaseRevision = "selected-rules"
		}
		if err := srv.handleLine(context.Background(), startThreadRequest(t, tc.name, params)); err != nil {
			t.Fatal(err)
		}
		response := responseByID(t, parseOutput(t, out.String()), tc.name)
		if response["error"] != nil {
			t.Fatalf("thread/start: %+v", response["error"])
		}
		started := remarshal[ThreadStartResult](t, response["result"])
		tc.cwd = started.Thread.CWD
		if tc.wantSelected {
			worktreeID = started.Thread.ID
		}
		thread, err := srv.ensureThreadRuntime(srv.thread(started.Thread.ID))
		if err != nil {
			t.Fatal(err)
		}
		actualFile, err := os.ReadFile(filepath.Join(tc.cwd, "AGENTS.md"))
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s actual checkout AGENTS.md=%q", tc.name, string(actualFile))
		if tc.wantSelected && !strings.Contains(string(actualFile), "FIXTURE_SELECTED_CHECKOUT_RULE") {
			t.Fatal("worktree does not contain selected revision")
		}
		result, err := thread.Toolkit.Execute(context.Background(), providers.ToolCall{Name: "load_skill", Arguments: `{"name":"release-check"}`})
		if err != nil {
			t.Fatalf("%s load_skill: %v", tc.name, err)
		}
		prompt := thread.StreamRunner.SystemPrompt
		canonical, err := filepath.EvalSymlinks(tc.cwd)
		if err != nil {
			t.Fatal(err)
		}
		if thread.Toolkit.RootDir() != canonical {
			t.Fatal("wrong tool root")
		}
		rule, skill := "FIXTURE_ORIGINAL_WORKSPACE_RULE", "FIXTURE_ORIGINAL_WORKSPACE_SKILL"
		if tc.wantSelected {
			rule, skill = "FIXTURE_SELECTED_CHECKOUT_RULE", "FIXTURE_SELECTED_CHECKOUT_SKILL"
		}
		if !strings.Contains(prompt, rule) {
			t.Errorf("%s: missing checkout instruction %s", tc.name, rule)
		}
		if !strings.Contains(result, skill) || !strings.Contains(result, canonical) {
			t.Errorf("%s: wrong skill content or resource root: %s", tc.name, result)
		}
		listID := "skills-" + tc.name
		dispatchPayload(t, srv, listID, MethodSkillList, SkillListParams{ThreadID: started.Thread.ID})
		catalog := remarshal[SkillListResult](t, responseByID(t, parseOutput(t, out.String()), listID)["result"])
		var selected SkillSummary
		for _, item := range catalog.Skills {
			if item.Name == "release-check" {
				selected = item
			}
		}
		if selected.Path != filepath.Join(canonical, ".agents", "skills", "release-check", "SKILL.md") {
			t.Fatalf("%s catalog used the wrong checkout: %+v", tc.name, selected)
		}
		identity, _ := json.Marshal(map[string]string{"name": selected.Name, "source": selected.Source, "path": selected.Path})
		explicit := "/skill " + string(identity) + "\n\nrelease notes"
		queueID := "explicit-" + tc.name
		dispatchPayload(t, srv, queueID, MethodTurnQueue, TurnQueueParams{
			ThreadID: started.Thread.ID, ClientID: queueID, Prompt: explicit, Hold: true,
			Images:       []TurnStartImage{{MediaType: "image/jpeg", Data: base64.StdEncoding.EncodeToString(encodeTestJPEG(t, 2, 2, 90))}},
			ContentParts: []providers.MessageContentPart{{Type: "text", Text: explicit}},
		})
		held, found, err := srv.findHeldUserTurn(started.Thread.ID, queueID)
		if err != nil || !found {
			t.Fatalf("explicit skill queue failed: %v", err)
		}
		if !strings.Contains(held.msg.Content, skill) || held.msg.DisplayContent != explicit || len(held.msg.Images) != 1 || len(held.msg.ContentParts) != 1 || held.msg.ContentParts[0].Text != explicit {
			t.Fatalf("%s queued skill lost instructions or presentation: %+v", tc.name, held.msg)
		}
		if tc.wantSelected {
			wrong, _ := json.Marshal(map[string]string{"name": selected.Name, "source": selected.Source, "path": filepath.Join(repo, ".agents", "skills", "release-check", "SKILL.md")})
			if _, err := srv.userMessageWithInputImages(started.Thread.ID, "/skill "+string(wrong), nil, nil, nil); err == nil {
				t.Fatal("worktree invocation accepted workspace-root identity")
			}
		}
		if strings.Count(prompt, "GLOBAL_GUIDANCE_MARKER") != 1 {
			t.Errorf("global instructions missing or duplicated")
		}
		global, err := thread.Toolkit.Execute(context.Background(), providers.ToolCall{Name: "load_skill", Arguments: `{"name":"global-policy"}`})
		if err != nil || !strings.Contains(global, "GLOBAL_SKILL_MARKER") {
			t.Errorf("global skill lost: %s %v", global, err)
		}
		side, err := rt.NewSideThreadRunner("side-"+tc.name, tc.cwd, wuuRuntime.ThreadModelSelection{})
		if err != nil {
			t.Fatal(err)
		}
		loaded, err := side.Tools.Execute(context.Background(), providers.ToolCall{Name: "load_skill", Arguments: `{"name":"release-check"}`})
		if err != nil || !strings.Contains(loaded, skill) {
			t.Errorf("side thread has wrong skill: %s %v", loaded, err)
		}

		for _, selection := range []wuuRuntime.ThreadModelSelection{{}, {Provider: "audit", Model: "gpt-other"}} {
			rebuilt, err := rt.NewThreadRuntimeForRootModel("rebuilt-"+tc.name+selection.Model, tc.cwd, selection)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(rebuilt.StreamRunner.SystemPrompt, rule) {
				t.Errorf("rebuilt runtime has wrong instructions")
			}
			loaded, err := rebuilt.Toolkit.Execute(context.Background(), providers.ToolCall{Name: "load_skill", Arguments: `{"name":"release-check"}`})
			if err != nil || !strings.Contains(loaded, skill) {
				t.Errorf("rebuilt runtime has wrong skill: %s %v", loaded, err)
			}
			if rebuilt.AgentControl != nil {
				rebuilt.AgentControl.Close()
			}
			rt.ReleasePluginGeneration(rebuilt.PluginGeneration)
		}
	}
	srv.Close()
	resumedOut := &lockedBuffer{}
	resumedServer := New(rt, resumedOut)
	defer resumedServer.Close()
	dispatchPayload(t, resumedServer, "resume", MethodThreadResume, ThreadResumeParams{SessionID: worktreeID})
	response := responseByID(t, parseOutput(t, resumedOut.String()), "resume")
	if response["error"] != nil {
		t.Fatalf("resume: %+v", response["error"])
	}
	resumed, err := resumedServer.ensureThreadRuntime(resumedServer.thread(worktreeID))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resumed.StreamRunner.SystemPrompt, "FIXTURE_SELECTED_CHECKOUT_RULE") {
		t.Error("resumed worktree lost its instructions")
	}
	loaded, err := resumed.Toolkit.Execute(context.Background(), providers.ToolCall{Name: "load_skill", Arguments: `{"name":"release-check"}`})
	if err != nil || !strings.Contains(loaded, "FIXTURE_SELECTED_CHECKOUT_SKILL") {
		t.Errorf("resumed worktree lost its skill: %s %v", loaded, err)
	}

}
