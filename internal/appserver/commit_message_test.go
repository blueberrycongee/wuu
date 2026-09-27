package appserver

import (
	"context"
	"strings"
	"testing"
)

func TestGitCommitMessageUsesConfiguredBYOKRuntime(t *testing.T) {
	client := &fakeClient{response: providersResponse("feat(desktop): add embedded browser proxy")}
	rt := newTestRuntime(t, client)
	rt.StreamRunner.ProviderName = "test-provider"
	rt.StreamRunner.APIModel = "test-api-model"
	rt.StreamRunner.Effort = "low"
	rt.StreamRunner.ProviderOptions = map[string]any{"thinking": "disabled"}
	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(
		`{"id":"commitmsg","method":"git/commit-message","params":{"diff":"diff --git a/a.go b/a.go\n+hello","files":["a.go","a.go"," b.go "]}}`,
	)); err != nil {
		t.Fatal(err)
	}

	result := remarshal[GitCommitMessageResult](
		t,
		waitForResponseByID(t, out, "commitmsg")["result"],
	)
	if result.Message != "feat(desktop): add embedded browser proxy" {
		t.Fatalf("message = %q", result.Message)
	}
	if len(client.requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(client.requests))
	}
	request := client.requests[0]
	if request.Provider != "test-provider" || request.Model != "test-api-model" {
		t.Fatalf("runtime selection = %s/%s", request.Provider, request.Model)
	}
	if request.Effort != "low" || request.ProviderOptions["thinking"] != "disabled" {
		t.Fatalf("runtime options = effort %q, options %#v", request.Effort, request.ProviderOptions)
	}
	if len(request.Messages) != 2 {
		t.Fatalf("messages = %#v", request.Messages)
	}
	user := request.Messages[1].Content
	if !strings.Contains(user, "- a.go\n- b.go\n") || strings.Contains(user, "a.go\n- a.go") {
		t.Fatalf("file list not cleaned/deduped = %q", user)
	}
	if !strings.Contains(user, "Staged diff:\ndiff --git a/a.go b/a.go\n+hello") {
		t.Fatalf("user message = %q", user)
	}
}

func TestCleanGeneratedCommitMessage(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"plain", "fix(desktop): keep search compact", "fix(desktop): keep search compact"},
		{"think block", "<think>reasoning</think>fix: guard nil", "fix: guard nil"},
		{"fenced", "```\nfeat: add proxy\n```", "feat: add proxy"},
		{"quoted", "\"chore: bump deps\"", "chore: bump deps"},
		{"body kept", "fix: guard nil\n\nExplains why.", "fix: guard nil\n\nExplains why."},
		{"empty", "<think>only</think>", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := cleanGeneratedCommitMessage(tc.in); got != tc.want {
				t.Fatalf("cleanGeneratedCommitMessage(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
