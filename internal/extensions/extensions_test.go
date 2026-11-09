package extensions

import (
	"testing"
	"time"
)

func TestFingerprintIsDeterministicAndRedactsSecretValues(t *testing.T) {
	first := ExecutableSpec{
		Command: "node",
		Args:    []string{"server.js", "--mode", "stdio"},
		Env: map[string]string{
			"API_TOKEN": "first-secret",
			"MODE":      "${MODE:-safe}",
		},
		Headers: map[string]string{
			"Authorization": "Bearer first-secret",
			"X-Tenant":      "${TENANT}",
		},
		Permissions: []string{"network.connect", "file.read"},
	}
	second := ExecutableSpec{
		Command: "node",
		Args:    []string{"server.js", "--mode", "stdio"},
		Env: map[string]string{
			"MODE":      "${MODE:-safe}",
			"API_TOKEN": "different-secret",
		},
		Headers: map[string]string{
			"X-Tenant":      "${TENANT}",
			"Authorization": "Bearer different-secret",
		},
		Permissions: []string{"file.read", "network.connect"},
	}

	firstFingerprint, err := Fingerprint(first)
	if err != nil {
		t.Fatal(err)
	}
	secondFingerprint, err := Fingerprint(second)
	if err != nil {
		t.Fatal(err)
	}
	if firstFingerprint != secondFingerprint {
		t.Fatalf("fingerprints differ: %q != %q", firstFingerprint, secondFingerprint)
	}
	if firstFingerprint == "" {
		t.Fatal("fingerprint is empty")
	}
}

func TestFingerprintChangesForExecutableBehavior(t *testing.T) {
	base := ExecutableSpec{
		Command:     "node",
		Args:        []string{"server.js"},
		URL:         "https://example.test/mcp",
		Permissions: []string{"network.connect"},
	}
	baseFingerprint, err := Fingerprint(base)
	if err != nil {
		t.Fatal(err)
	}

	cases := map[string]ExecutableSpec{
		"command":     {Command: "bun", Args: base.Args, URL: base.URL, Permissions: base.Permissions},
		"arguments":   {Command: base.Command, Args: []string{"other.js"}, URL: base.URL, Permissions: base.Permissions},
		"url":         {Command: base.Command, Args: base.Args, URL: "https://other.test/mcp", Permissions: base.Permissions},
		"permissions": {Command: base.Command, Args: base.Args, URL: base.URL, Permissions: []string{"network.connect", "file.write"}},
	}
	for name, candidate := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := Fingerprint(candidate)
			if err != nil {
				t.Fatal(err)
			}
			if got == baseFingerprint {
				t.Fatalf("fingerprint did not change for %s", name)
			}
		})
	}
}

func TestPackageFingerprintPreservesArgumentOrderAndHookOptions(t *testing.T) {
	base := PackageSpec{
		ID:      "ordered",
		Runtime: &RuntimeSpec{Protocol: "wuu-plugin-v1", Command: "plugin", Args: []string{"--from", "one"}},
		Hooks: map[string][]HookEntry{
			"PreToolUse": {{Type: "prompt", Prompt: "check", Model: "model-one", Timeout: 10}},
		},
	}
	baseFingerprint, err := ComputeFingerprint(base)
	if err != nil {
		t.Fatal(err)
	}
	reordered := base
	reordered.Runtime = &RuntimeSpec{Protocol: "wuu-plugin-v1", Command: "plugin", Args: []string{"one", "--from"}}
	reorderedFingerprint, err := ComputeFingerprint(reordered)
	if err != nil {
		t.Fatal(err)
	}
	if reorderedFingerprint == baseFingerprint {
		t.Fatal("runtime argument order did not change package fingerprint")
	}
	hookChanged := base
	hookChanged.Hooks = map[string][]HookEntry{
		"PreToolUse": {{Type: "prompt", Prompt: "check", Model: "model-two", Timeout: 20}},
	}
	hookFingerprint, err := ComputeFingerprint(hookChanged)
	if err != nil {
		t.Fatal(err)
	}
	if hookFingerprint == baseFingerprint {
		t.Fatal("hook model and timeout did not change package fingerprint")
	}
}

func TestGrantMatchesExactFingerprint(t *testing.T) {
	grant := Grant{
		SubjectID:   "mcp:project:docs",
		Fingerprint: "abc123",
		Scope:       GrantScopeProject,
		Permissions: []string{"network.connect"},
		ApprovedAt:  time.Date(2026, 7, 10, 8, 0, 0, 0, time.UTC),
	}
	settings := Settings{Grants: map[string]Grant{grant.SubjectID: grant}}

	got, ok := settings.FindGrant(grant.SubjectID, "abc123")
	if !ok || got.SubjectID != grant.SubjectID {
		t.Fatalf("FindGrant = %+v, %v", got, ok)
	}
	if _, ok := settings.FindGrant(grant.SubjectID, "changed"); ok {
		t.Fatal("changed fingerprint reused an old grant")
	}
	if _, ok := settings.FindGrant("mcp:project:missing", "abc123"); ok {
		t.Fatal("missing subject matched a grant")
	}
}
