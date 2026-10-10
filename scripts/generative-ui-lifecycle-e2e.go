//go:build ignore

// Run from the repository root after building the plugin helpers:
// go run scripts/generative-ui-lifecycle-e2e.go
//
// This driver uses the real bundled package, native helper and runtime lifecycle
// with synthetic input. It creates a temporary home and never invokes a model.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/extensions"
	"github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

type lifecycleCheck struct {
	Stage             string `json:"stage"`
	ToolCount         int    `json:"toolCount"`
	Tool              string `json:"tool,omitempty"`
	ContentParts      int    `json:"contentParts,omitempty"`
	StaleCallRejected bool   `json:"staleCallRejected,omitempty"`
	Passed            bool   `json:"passed"`
}

type lifecycleReport struct {
	RecordedAt          string           `json:"recordedAt"`
	Plugin              string           `json:"plugin"`
	PackageFingerprint  string           `json:"packageFingerprint"`
	HelperSHA256        string           `json:"helperSha256"`
	SchemaNormalization []string         `json:"schemaNormalization"`
	Checks              []lifecycleCheck `json:"checks"`
}

func main() {
	helperDefault := os.Getenv("WUU_GENERATIVE_UI_PLUGIN_HELPER")
	if helperDefault == "" {
		helperDefault = "desktop/build/bin/wuu-generative-ui-plugin"
	}
	outputDefault := os.Getenv("WUU_GENUI_LIFECYCLE_OUTPUT")
	if outputDefault == "" {
		outputDefault = "desktop/out/e2e/generative-ui/lifecycle-results.json"
	}
	helper := flag.String("helper", helperDefault, "path to the built generative UI helper")
	output := flag.String("output", outputDefault, "path for the JSON evidence file")
	flag.Parse()
	if err := runLifecycle(*helper, *output); err != nil {
		fmt.Fprintln(os.Stderr, "Generative UI lifecycle E2E:", err)
		os.Exit(1)
	}
}

func runLifecycle(helperPath, outputPath string) error {
	helper, err := filepath.Abs(helperPath)
	if err != nil {
		return err
	}
	output, err := filepath.Abs(outputPath)
	if err != nil {
		return err
	}
	file, err := os.Open(helper)
	if err != nil {
		return fmt.Errorf("open built helper (run npm --prefix desktop run build:core first): %w", err)
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, file)
	if err := errors.Join(copyErr, file.Close()); err != nil {
		return err
	}
	report := lifecycleReport{
		RecordedAt:   time.Now().UTC().Format(time.RFC3339),
		HelperSHA256: hex.EncodeToString(hash.Sum(nil)),
	}
	dir, err := os.MkdirTemp("", "wuu-generative-ui-e2e-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	home := filepath.Join(dir, "wuu")
	for _, path := range []string{home, filepath.Join(dir, "state"), filepath.Join(dir, "sessions")} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return err
		}
	}
	// Isolate both explicit runtime paths and fallback home/cwd discovery.
	for key, value := range map[string]string{
		"HOME": dir, "USERPROFILE": dir, "XDG_CONFIG_HOME": filepath.Join(dir, "config"),
		"WUU_HOME": home, "WUU_GENERATIVE_UI_PLUGIN_HELPER": helper,
	} {
		if err := os.Setenv(key, value); err != nil {
			return err
		}
	}
	if err := os.Chdir(dir); err != nil {
		return err
	}
	settings := &extensions.Settings{}
	var target plugin.Plugin
	for _, item := range plugin.Discover(dir, home) {
		if item.ID == "generative-ui" {
			target = item
		} else {
			settings.SetEnabled(item.SubjectID, false)
		}
	}
	if target.ID == "" {
		return errors.New("bundled generative-ui plugin was not discovered; verify that the helper is executable")
	}
	if target.EnabledByDefault() {
		return errors.New("bundled generative-ui plugin must be disabled by default")
	}
	report.Plugin, report.PackageFingerprint = target.ID, target.Fingerprint
	rt := &runtime.Session{
		RootDir: dir, HomeDir: dir, WuuHome: home,
		StateDir: filepath.Join(dir, "state"), SessionDir: filepath.Join(dir, "sessions"),
		ProviderName: "synthetic", Model: "no-inference",
	}
	defer rt.Cleanup()
	cfg := config.Config{Extensions: settings}
	var toolName string
	arguments := json.RawMessage(`{"spec":{"version":1,"title":"Lifecycle check","fallback":"Hello","blocks":[{"id":"hello","type":"text","text":"Hello"}]}}`)
	for _, stage := range []string{"default-disabled", "enabled", "disabled", "reenabled", "safe-mode"} {
		switch stage {
		case "enabled", "reenabled":
			settings.SetEnabled(target.SubjectID, true)
		case "disabled":
			settings.SetEnabled(target.SubjectID, false)
		case "safe-mode":
			rt.SafeMode = true
		}
		candidate, err := rt.PreflightExtensions(cfg)
		if err != nil {
			return fmt.Errorf("%s preflight: %w", stage, err)
		}
		if err := rt.ActivatePluginGeneration(candidate, nil); err != nil {
			return fmt.Errorf("%s activation: %w", stage, err)
		}
		definitions := rt.PluginHost.ToolDefinitions()
		wantCount := 0
		if stage == "enabled" || stage == "reenabled" {
			wantCount = 1
		}
		if len(definitions) != wantCount {
			return fmt.Errorf("%s exposed %d tools, want %d; statuses: %+v", stage, len(definitions), wantCount, rt.PluginHost.Statuses())
		}
		check := lifecycleCheck{Stage: stage, ToolCount: len(definitions), Passed: true}
		if wantCount == 1 {
			toolName = definitions[0].Name
			result, err := rt.PluginHost.ExecuteTool(context.Background(), toolName, pluginhost.ToolExecuteInput{
				ThreadID: "synthetic-thread", TurnID: "synthetic-turn", CallID: "call-" + stage, Arguments: arguments,
			})
			if err != nil {
				return fmt.Errorf("%s tool call: %w", stage, err)
			}
			if result.IsError || len(result.Content) != 2 || result.Content[0].Text != "Hello" ||
				result.Content[1].MIMEType != "application/vnd.wuu.ui+json" || result.Content[1].Artifact == nil ||
				result.Content[1].Artifact.Placement != "inline" {
				return fmt.Errorf("%s returned an unexpected UI result", stage)
			}
			check.Tool, check.ContentParts = toolName, len(result.Content)
			if stage == "enabled" {
				for _, route := range []string{"gpt", "claude", "kimi", "gemini"} {
					schema := providers.ToolInputSchemaForModel(route, definitions[0].InputSchema)
					compiler := jsonschema.NewCompiler()
					if err := compiler.AddResource("tool.json", schema); err != nil {
						return fmt.Errorf("%s schema normalization: %w", route, err)
					}
					compiled, err := compiler.Compile("tool.json")
					if err != nil {
						return fmt.Errorf("%s schema compilation: %w", route, err)
					}
					var input any
					if err := json.Unmarshal(arguments, &input); err != nil {
						return err
					}
					if err := compiled.Validate(input); err != nil {
						return fmt.Errorf("%s normalized schema rejected valid input: %w", route, err)
					}
					if err := compiled.Validate(map[string]any{"spec": true}); err == nil {
						return fmt.Errorf("%s normalized schema lost its spec reference", route)
					}
					report.SchemaNormalization = append(report.SchemaNormalization, route)
				}
			}
		} else if toolName != "" {
			_, err := rt.PluginHost.ExecuteTool(context.Background(), toolName, pluginhost.ToolExecuteInput{Arguments: arguments})
			if err == nil {
				return fmt.Errorf("%s left the stale tool binding executable", stage)
			}
			check.StaleCallRejected = true
		}
		report.Checks = append(report.Checks, check)
	}
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(output, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	fmt.Printf("Native lifecycle passed: %s\nSchema normalization passed: %s (no provider API calls)\nEvidence: %s\n", "default-disabled, enabled, disabled, reenabled, safe-mode", strings.Join(report.SchemaNormalization, ", "), output)
	return nil
}
