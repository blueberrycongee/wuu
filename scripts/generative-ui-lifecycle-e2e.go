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

type programCheck struct {
	Name     string `json:"name"`
	Accepted bool   `json:"accepted"`
	Passed   bool   `json:"passed"`
}

type lifecycleReport struct {
	RecordedAt          string           `json:"recordedAt"`
	Plugin              string           `json:"plugin"`
	PackageFingerprint  string           `json:"packageFingerprint"`
	HelperSHA256        string           `json:"helperSha256"`
	SchemaNormalization []string         `json:"schemaNormalization"`
	SchemaBytes         map[string]int   `json:"schemaBytes"`
	ProgramChecks       []programCheck   `json:"programChecks"`
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
		SchemaBytes:  make(map[string]int),
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
			if !definitions[0].DirectOnly {
				return errors.New("render_ui must remain directly callable in PTC mode")
			}
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
				if err := checkPrograms(rt, toolName, &report); err != nil {
					return err
				}
				for _, route := range []string{"gpt", "claude", "kimi", "gemini"} {
					schema := providers.ToolInputSchemaForModel(route, definitions[0].InputSchema)
					encodedSchema, err := json.Marshal(schema)
					if err != nil {
						return err
					}
					report.SchemaBytes[route] = len(encodedSchema)
					compiler := jsonschema.NewCompiler()
					if err := compiler.AddResource("tool.json", schema); err != nil {
						return fmt.Errorf("%s schema normalization: %w", route, err)
					}
					compiled, err := compiler.Compile("tool.json")
					if err != nil {
						return fmt.Errorf("%s schema compilation: %w", route, err)
					}
					inputs := append([]json.RawMessage{arguments}, validPrograms()...)
					for index, arguments := range inputs {
						var input any
						if err := json.Unmarshal(arguments, &input); err != nil {
							return err
						}
						if err := compiled.Validate(input); err != nil {
							return fmt.Errorf("%s normalized schema rejected valid input %d: %w", route, index, err)
						}
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

// Exercise the real native helper through the activated extension generation.
// These are public protocol, persistence identity, and failure-boundary cases;
// no model, credential, or isolated replacement of the handler is involved.
func checkPrograms(rt *runtime.Session, toolName string, report *lifecycleReport) error {
	valid := validPrograms()
	refs := make(map[string]bool)
	for index, arguments := range valid {
		fmt.Printf("Checking valid program %d\n", index)
		result, err := rt.PluginHost.ExecuteTool(context.Background(), toolName, pluginhost.ToolExecuteInput{
			ThreadID: "program-thread", TurnID: "program-turn", CallID: fmt.Sprintf("program-%d", index), Arguments: arguments,
		})
		if err != nil || result.IsError || len(result.Content) != 2 {
			return fmt.Errorf("valid program %d failed: %v (%+v)", index, err, result)
		}
		part := result.Content[1]
		if part.Artifact == nil || !strings.HasPrefix(part.Artifact.Ref, "ui-v2-") || refs[part.Artifact.Ref] {
			return fmt.Errorf("program %d lacks a unique versioned presentation identity", index)
		}
		refs[part.Artifact.Ref] = true
		var resource struct {
			MIMEType string `json:"mimeType"`
			Text     string `json:"text"`
		}
		if err := json.Unmarshal(part.Resource, &resource); err != nil {
			return err
		}
		digest := sha256.Sum256([]byte(resource.Text))
		if resource.MIMEType != "application/vnd.wuu.ui+json" || part.Artifact.SHA256 != hex.EncodeToString(digest[:]) || part.Artifact.SizeBytes == nil || *part.Artifact.SizeBytes != int64(len(resource.Text)) {
			return fmt.Errorf("program %d resource integrity did not round trip", index)
		}
		var input map[string]any
		if err := json.Unmarshal(arguments, &input); err != nil {
			return err
		}
		var output any
		if err := json.Unmarshal([]byte(resource.Text), &output); err != nil {
			return err
		}
		before, _ := json.Marshal(input["spec"])
		after, _ := json.Marshal(output)
		if string(before) != string(after) {
			return fmt.Errorf("program %d changed model data in transit", index)
		}
		report.ProgramChecks = append(report.ProgramChecks, programCheck{Name: fmt.Sprintf("valid-program-%d", index), Accepted: true, Passed: true})
	}
	for _, fixture := range invalidPrograms() {
		fmt.Printf("Checking rejection: %s\n", fixture.name)
		result, err := rt.PluginHost.ExecuteTool(context.Background(), toolName, pluginhost.ToolExecuteInput{
			ThreadID: "program-thread", TurnID: "program-turn", CallID: "invalid-" + fixture.name, Arguments: fixture.arguments,
		})
		if fixture.name == "malformed-json" && err != nil && len(result.Content) == 0 {
			report.ProgramChecks = append(report.ProgramChecks, programCheck{Name: fixture.name, Accepted: false, Passed: true})
			continue
		}
		if err != nil || !result.IsError || len(result.Content) != 1 || result.Content[0].Type != "text" || !strings.Contains(result.Content[0].Text, "UI was not rendered:") {
			return fmt.Errorf("invalid program %s did not fail safely: %v (%+v)", fixture.name, err, result)
		}
		report.ProgramChecks = append(report.ProgramChecks, programCheck{Name: fixture.name, Accepted: false, Passed: true})
	}
	return nil
}

func validPrograms() []json.RawMessage {
	unicodeState := []any{}
	for index, value := range []string{"<", "&", "\u2028"} {
		unicodeState = append(unicodeState, map[string]any{"id": fmt.Sprintf("text%d", index), "value": strings.Repeat(value, 2000)})
	}
	unicodeArguments, _ := json.Marshal(map[string]any{"spec": map[string]any{"version": 2, "title": "Unicode budget", "fallback": "Harmless text remains within the state budget.", "state": unicodeState, "body": []any{map[string]any{"id": "text", "type": "text", "text": "Within budget"}}}})
	return []json.RawMessage{
		unicodeArguments,
		json.RawMessage(`{"spec":{"version":2,"title":"Calendar boundaries","fallback":"Optional empty dates and valid calendar boundaries.","state":[{"id":"empty","value":""},{"id":"leap","value":"2000-02-29"},{"id":"first","value":"0001-01-01"},{"id":"last","value":"9999-12-31"}],"body":[{"id":"emptyInput","type":"input","inputType":"date","state":"empty","label":"Optional date"},{"id":"leapInput","type":"input","inputType":"date","state":"leap","label":"Leap date"},{"id":"firstInput","type":"input","inputType":"date","state":"first","label":"Earliest date"},{"id":"lastInput","type":"input","inputType":"date","state":"last","label":"Latest date"}]}}`),
		json.RawMessage(`{"spec":{"version":2,"title":"Keyed repeated items","fallback":"A and B retain their own view identities.","state":[{"id":"items","value":[{"id":"a","label":"A"},{"id":"b","label":"B"}]}],"body":[{"id":"itemsList","type":"each","items":{"op":"state","key":"items"},"key":{"op":"item","path":["id"]},"children":[{"id":"itemLabel","type":"text","text":{"op":"item","path":["label"]}}]}]}}`),
		json.RawMessage(`{"spec":{"version":2,"title":"Numeric mixed series","fallback":"Scatter and line points share a numeric x-axis.","state":[],"body":[{"id":"chart","type":"chart","chartType":"scatter","series":[{"id":"observed","label":"Observed","points":{"op":"literal","value":[{"label":"Observation","value":2,"x":1}]}},{"id":"trend","label":"Trend","chartType":"line","points":{"op":"literal","value":[{"label":"Trend start","value":1},{"label":"Trend end","value":3}]}}]}]}}`),
		json.RawMessage(`{"spec":{"version":2,"title":"Follow-up form","fallback":"Describe the requested follow-up.","state":[{"id":"note","value":""},{"id":"allow","value":true}],"body":[{"id":"request","type":"form","label":"Send follow-up","children":[{"id":"noteInput","type":"textarea","state":"note","label":"Note","required":true,"disabled":{"op":"not","args":[{"op":"state","key":"allow"}]},"invalid":false}],"onSubmit":[{"type":"sendMessage","text":{"op":"state","key":"note"}}]}]}}`),
		json.RawMessage(`{"spec":{"version":2,"title":"Growth simulation","fallback":"A local six-step compound-growth projection.","state":[{"id":"rate","value":1.1}],"body":[{"id":"curve","type":"chart","chartType":"line","points":{"op":"map","args":[{"op":"range","args":[1,7]},{"op":"object","args":["label",{"op":"concat","args":["Year ",{"op":"item"}]},"value",{"op":"multiply","args":[100,{"op":"pow","args":[{"op":"state","key":"rate"},{"op":"item"}]}]}]}]}},{"id":"rows","type":"table","columns":[{"key":"year","label":"Year"},{"key":"value","label":"Multiplier"}],"rows":{"op":"map","args":[{"op":"range","args":[1,7]},{"op":"array","args":[{"op":"item"},{"op":"pow","args":[{"op":"state","key":"rate"},{"op":"item"}]}]}]}}]}}`),
		json.RawMessage(`{"spec":{"version":2,"title":"Local estimator","fallback":"An editable estimate with a conversation follow-up.","state":[{"id":"quantity","value":2},{"id":"enabled","value":true},{"id":"choice","value":"monthly"},{"id":"note","value":""},{"id":"items","value":[{"name":"Design","cost":12},{"name":"Build","cost":24}]}],"body":[{"id":"layout","type":"stack","children":[{"id":"header","type":"heading","text":"Estimate"},{"id":"controls","type":"flow","children":[{"id":"quantityInput","type":"input","state":"quantity","inputType":"number","label":"Quantity","onChange":[{"type":"set","state":"quantity","value":{"op":"event"}}]},{"id":"enabledInput","type":"checkbox","state":"enabled","label":"Include estimate"},{"id":"frequency","type":"segmented","state":"choice","label":"Frequency","options":[{"label":"Monthly","value":"monthly"},{"label":"Yearly","value":"yearly"}]}]},{"id":"conditional","type":"if","when":{"op":"state","key":"enabled"},"then":[{"id":"total","type":"metric","label":"Total","value":{"op":"multiply","args":[{"op":"state","key":"quantity"},{"op":"sum","args":[{"op":"map","args":[{"op":"state","key":"items"},{"op":"item","path":["cost"]}]}]}]}}],"else":[]},{"id":"itemsList","type":"each","items":{"op":"filter","args":[{"op":"state","key":"items"},{"op":"gt","args":[{"op":"item","path":["cost"]},10]}]},"children":[{"id":"itemLabel","type":"text","text":{"op":"item","path":["name"]}}]},{"id":"noteInput","type":"textarea","state":"note","label":"Notes"},{"id":"followUp","type":"button","label":"Continue with this estimate","onClick":[{"type":"sendMessage","text":{"op":"concat","args":["Discuss an estimate for ",{"op":"state","key":"quantity"}," items."]}}]}]}]}}`),
		json.RawMessage(`{"spec":{"version":2,"title":"Mixed chart","fallback":"Plan: 12; actual: 9.","state":[],"body":[{"id":"series","type":"chart","chartType":"bar","series":[{"id":"plan","label":"Plan","points":{"op":"literal","value":[{"label":"A","value":12}]}},{"id":"actual","label":"Actual","chartType":"line","points":{"op":"literal","value":[{"label":"A","value":9}]}}]},{"id":"scatter","type":"chart","chartType":"scatter","points":{"op":"literal","value":[{"label":"A","value":9,"x":3},{"label":"B","value":12,"x":7}]}},{"id":"pie","type":"chart","chartType":"donut","points":{"op":"literal","value":[{"label":"Plan","value":12},{"label":"Actual","value":9}]}}]}}`),
		json.RawMessage(`{"spec":{"version":2,"title":"Bounded controls","fallback":"Date, slider, progress and table controls.","state":[{"id":"date","value":"2026-10-11"},{"id":"amount","value":4}],"body":[{"id":"details","type":"disclosure","title":"Controls","open":true,"children":[{"id":"dateInput","type":"input","inputType":"date","state":"date","label":"Date"},{"id":"amountInput","type":"slider","state":"amount","label":"Amount","min":0,"max":10,"step":1},{"id":"progress","type":"progress","value":{"op":"state","key":"amount"},"max":10},{"id":"table","type":"table","columns":[{"key":"value","label":"Value"}],"rows":{"op":"literal","value":[[1],[2],[3]]}}]}]}}`),
	}
}

type programFailure struct {
	name      string
	arguments json.RawMessage
}

func invalidPrograms() []programFailure {
	base := `{"spec":{"version":2,"title":"Boundary","fallback":"Safe fallback","state":[{"id":"count","value":2}],"body":[{"id":"value","type":"text","text":{"op":"state","key":"count"}}]}}`
	var failures []programFailure
	mutate := func(name string, change func(map[string]any)) {
		var input map[string]any
		if err := json.Unmarshal([]byte(base), &input); err != nil {
			panic(err)
		}
		change(input["spec"].(map[string]any))
		raw, err := json.Marshal(input)
		if err != nil {
			panic(err)
		}
		failures = append(failures, programFailure{name: name, arguments: raw})
	}
	body := func(spec map[string]any, node map[string]any) { spec["body"] = []any{node} }
	text := func(value any) map[string]any { return map[string]any{"id": "value", "type": "text", "text": value} }
	mutate("unsupported-version", func(s map[string]any) { s["version"] = 3 })
	mutate("unknown-script-property", func(s map[string]any) { s["code"] = "alert(1)" })
	mutate("missing-body", func(s map[string]any) { delete(s, "body") })
	mutate("unknown-state", func(s map[string]any) { body(s, text(map[string]any{"op": "state", "key": "missing"})) })
	mutate("duplicate-node-id", func(s map[string]any) { s["body"] = []any{text("One"), text("Two")} })
	mutate("duplicate-state-id", func(s map[string]any) {
		s["state"] = []any{map[string]any{"id": "count", "value": 1}, map[string]any{"id": "count", "value": 2}}
	})
	mutate("wrong-binding-type", func(s map[string]any) {
		body(s, map[string]any{"id": "input", "type": "checkbox", "state": "count", "label": "Count"})
	})
	mutate("slider-range", func(s map[string]any) {
		body(s, map[string]any{"id": "input", "type": "slider", "state": "count", "label": "Count", "min": 3, "max": 10})
	})
	mutate("unscoped-event", func(s map[string]any) { body(s, text(map[string]any{"op": "event"})) })
	mutate("unscoped-item", func(s map[string]any) { body(s, text(map[string]any{"op": "item"})) })
	mutate("change-cannot-send", func(s map[string]any) {
		body(s, map[string]any{"id": "input", "type": "input", "inputType": "number", "state": "count", "label": "Count", "onChange": []any{map[string]any{"type": "sendMessage", "text": "Unrequested"}}})
	})
	mutate("duplicate-send", func(s map[string]any) {
		body(s, map[string]any{"id": "button", "type": "button", "label": "Continue", "onClick": []any{map[string]any{"type": "sendMessage", "text": "One"}, map[string]any{"type": "sendMessage", "text": "Two"}}})
	})
	mutate("unknown-operation", func(s map[string]any) { body(s, text(map[string]any{"op": "eval", "args": []any{"1+1"}})) })
	mutate("wrong-operation-arity", func(s map[string]any) { body(s, text(map[string]any{"op": "add", "args": []any{1}})) })
	mutate("prototype-key", func(s map[string]any) {
		body(s, text(map[string]any{"op": "literal", "value": map[string]any{"__proto__": "blocked"}}))
	})
	mutate("reserved-state-id", func(s map[string]any) { s["state"] = []any{map[string]any{"id": "constructor", "value": 1}} })
	mutate("invalid-table-row", func(s map[string]any) {
		body(s, map[string]any{"id": "table", "type": "table", "columns": []any{map[string]any{"key": "one", "label": "One"}}, "rows": map[string]any{"op": "literal", "value": []any{[]any{1, 2}}}})
	})
	mutate("scatter-needs-x", func(s map[string]any) {
		body(s, map[string]any{"id": "chart", "type": "chart", "chartType": "scatter", "points": map[string]any{"op": "literal", "value": []any{map[string]any{"label": "A", "value": 2}}}})
	})
	mutate("invalid-stacking", func(s map[string]any) {
		body(s, map[string]any{"id": "chart", "type": "chart", "chartType": "line", "stacked": true, "points": map[string]any{"op": "literal", "value": []any{map[string]any{"label": "A", "value": 2}}}})
	})
	mutate("blank-data-key", func(s map[string]any) {
		s["state"] = []any{map[string]any{"id": "count", "value": map[string]any{" ": 1}}}
	})
	mutate("reserved-column-key", func(s map[string]any) {
		body(s, map[string]any{"id": "table", "type": "table", "columns": []any{map[string]any{"key": "constructor", "label": "One"}}, "rows": map[string]any{"op": "literal", "value": []any{[]any{1}}}})
	})
	mutate("irrelevant-stacking-flag", func(s map[string]any) {
		body(s, map[string]any{"id": "chart", "type": "chart", "chartType": "line", "stacked": false, "points": map[string]any{"op": "literal", "value": []any{map[string]any{"label": "A", "value": 2}}}})
	})
	mutate("negative-pie-value", func(s map[string]any) {
		body(s, map[string]any{"id": "chart", "type": "chart", "chartType": "pie", "points": map[string]any{"op": "literal", "value": []any{map[string]any{"label": "A", "value": -1}}}})
	})
	mutate("nested-form", func(s map[string]any) {
		inner := map[string]any{"id": "inner", "type": "form", "label": "Inner", "children": []any{}, "onSubmit": []any{map[string]any{"type": "reset"}}}
		body(s, map[string]any{"id": "outer", "type": "form", "label": "Outer", "children": []any{inner}, "onSubmit": []any{map[string]any{"type": "reset"}}})
	})
	mutate("submit-has-no-change-value", func(s map[string]any) {
		body(s, map[string]any{"id": "form", "type": "form", "label": "Submit", "children": []any{}, "onSubmit": []any{map[string]any{"type": "set", "state": "count", "value": map[string]any{"op": "event"}}}})
	})
	for _, fixture := range []struct {
		name   string
		labels []string
	}{
		{"category-series-shifted-labels", []string{"Feb", "Mar"}},
		{"category-series-reordered-labels", []string{"Feb", "Jan"}},
		{"category-series-different-length", []string{"Jan"}},
	} {
		mutate(fixture.name, func(s map[string]any) {
			series := []any{}
			for index, labels := range [][]string{{"Jan", "Feb"}, fixture.labels} {
				points := []any{}
				for _, label := range labels {
					points = append(points, map[string]any{"label": label, "value": 1})
				}
				series = append(series, map[string]any{"id": fmt.Sprintf("series%d", index), "label": fmt.Sprintf("Series %d", index), "points": map[string]any{"op": "literal", "value": points}})
			}
			body(s, map[string]any{"id": "chart", "type": "chart", "chartType": "line", "series": series})
		})
	}
	for _, value := range []string{"tomorrow", "2026-02-29", "1900-02-29", "2024-02-30", "2026-04-31", "0000-01-01", "10000-01-01", "2026-13-01", "2026-00-10", "2026-10-00", "2026-1-01", "2026-01-01T00:00:00Z"} {
		mutate("invalid-date-"+value, func(s map[string]any) {
			s["state"] = []any{map[string]any{"id": "date", "value": value}}
			body(s, map[string]any{"id": "dateInput", "type": "input", "inputType": "date", "state": "date", "label": "Date"})
		})
	}
	mutate("each-key-cannot-read-event", func(s map[string]any) {
		body(s, map[string]any{"id": "each", "type": "each", "items": map[string]any{"op": "literal", "value": []any{1}}, "key": map[string]any{"op": "event"}, "children": []any{}})
	})
	mutate("each-key-expression-budget", func(s map[string]any) {
		var value any = 1
		for range 17 {
			value = map[string]any{"op": "abs", "args": []any{value}}
		}
		body(s, map[string]any{"id": "each", "type": "each", "items": map[string]any{"op": "literal", "value": []any{1}}, "key": value, "children": []any{}})
	})
	mutate("key-outside-each", func(s map[string]any) { node := text("No key"); node["key"] = "unexpected"; body(s, node) })
	mutate("expression-depth", func(s map[string]any) {
		var value any = true
		for range 17 {
			value = map[string]any{"op": "not", "args": []any{value}}
		}
		body(s, text(value))
	})
	mutate("node-depth", func(s map[string]any) {
		var node any = text("leaf")
		for index := range 13 {
			node = map[string]any{"id": fmt.Sprintf("nested%d", index), "type": "stack", "children": []any{node}}
		}
		s["body"] = []any{node}
	})
	mutate("node-budget", func(s map[string]any) {
		var groups []any
		for group := range 5 {
			var children []any
			for index := range 52 {
				children = append(children, map[string]any{"id": fmt.Sprintf("leaf%d_%d", group, index), "type": "text", "text": "leaf"})
			}
			groups = append(groups, map[string]any{"id": fmt.Sprintf("group%d", group), "type": "stack", "children": children})
		}
		s["body"] = groups
	})
	mutate("expression-budget", func(s map[string]any) {
		var expression any = 1
		for range 6 {
			expression = map[string]any{"op": "add", "args": []any{expression, expression}}
		}
		var nodes []any
		for index := range 17 {
			node := text(expression)
			node["id"] = fmt.Sprintf("expression%d", index)
			nodes = append(nodes, node)
		}
		s["body"] = nodes
	})
	mutate("odd-object-arguments", func(s map[string]any) { body(s, text(map[string]any{"op": "object", "args": []any{"a", 1, "b"}})) })
	mutate("literal-depth", func(s map[string]any) {
		var value any = 0
		for range 9 {
			value = []any{value}
		}
		body(s, text(map[string]any{"op": "literal", "value": value}))
	})
	mutate("state-byte-budget", func(s map[string]any) {
		var values []any
		for range 5 {
			values = append(values, strings.Repeat("x", 4000))
		}
		s["state"] = []any{map[string]any{"id": "count", "value": values}}
	})
	failures = append(failures,
		programFailure{name: "argument-byte-budget", arguments: json.RawMessage(`{"spec":{"title":"` + strings.Repeat("x", 128*1024) + `"}}`)},
		programFailure{name: "json-depth-budget", arguments: json.RawMessage(strings.Repeat("[", 97) + "0" + strings.Repeat("]", 97))},
		programFailure{name: "malformed-json", arguments: json.RawMessage(`{"spec":`)},
	)
	return failures
}
