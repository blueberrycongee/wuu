package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/appserver"
	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/personalreview"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/statepath"
	"github.com/blueberrycongee/wuu/internal/userspace"
)

func TestPersonalReviewMigrationCLIRequiresStoppedHostAndExplicitApplyDigest(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WUU_HOME", home)
	binding := userspace.Binding{UserSpaceID: "operator", ProductDomain: "personal"}
	source := filepath.Join(binding.State(home), "personal-review.sqlite")
	if err := os.MkdirAll(filepath.Dir(source), 0700); err != nil {
		t.Fatal(err)
	}
	legacy, err := personalreview.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.GetSettings(context.Background(), binding.UserSpaceID); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Composition = composition.DefaultConfig("headless", appserver.NewPersonalActionsConsumer().ID())
	cfg.DefaultProvider = "fixture"
	cfg.Providers = map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "http://127.0.0.1:1/v1", APIKey: "synthetic", Model: "not-called"}}
	raw, _ := json.Marshal(cfg)
	configPath := filepath.Join(home, "config.json")
	if err := os.WriteFile(configPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	out := &bytes.Buffer{}
	if err := runPersonalReviewMigration([]string{"--user-space", binding.UserSpaceID, "--apply"}, out); err == nil || !strings.Contains(err.Error(), "expect-source-digest") {
		t.Fatalf("implicit import allowed: %v", err)
	}
	dir := statepath.SessionsDir(binding.State(home))
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	lease, _, err := session.AcquireAppServerPresence(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	if err := lease.FinalizeStartup(); err != nil {
		t.Fatal(err)
	}
	if err := runPersonalReviewMigration([]string{"--user-space", binding.UserSpaceID, "--config", configPath, "--composition-profile", "personal"}, out); err == nil || !strings.Contains(err.Error(), "stop all") {
		t.Fatalf("live host did not prevent provider boot: %v", err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	// Exercise the operator command against an explicitly selected profile;
	// no model request or UserHost is needed to migrate its review authority.
	args := []string{"--user-space", binding.UserSpaceID, "--config", configPath, "--composition-profile", "headless"}
	if err := runPersonalReviewMigration(args, out); err != nil {
		t.Fatal("inspect stopped host", err)
	}
	var preview struct {
		SourceDigest string `json:"source_digest"`
		Status       string `json:"status"`
	}
	if err := json.Unmarshal(out.Bytes(), &preview); err != nil || preview.SourceDigest == "" || preview.Status != "migration-required" {
		t.Fatalf("preview: %s %v", out.String(), err)
	}
	out.Reset()
	if err := runPersonalReviewMigration(append(args, "--apply", "--expect-source-digest", preview.SourceDigest), out); err != nil {
		t.Fatal("apply stopped host", err)
	}
	if err := json.Unmarshal(out.Bytes(), &preview); err != nil || preview.Status != "migrated" {
		t.Fatalf("result: %s %v", out.String(), err)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatal("migration removed source", err)
	}
}
