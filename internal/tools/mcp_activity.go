package tools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/activity"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

type MCPActivityBinding struct {
	Kind     activity.Kind
	PluginID string
}

type cuaSequenceRequest struct {
	Action           string           `json:"action"`
	App              string           `json:"app"`
	ForegroundPolicy string           `json:"foreground_policy"`
	Steps            []map[string]any `json:"steps"`
	SnapshotID       string           `json:"snapshot_id"`
	Mode             string           `json:"mode"`
	After            string           `json:"after"`
}

func (t *Toolkit) SetActivityRegistry(registry *activity.Registry) {
	if t == nil {
		return
	}
	t.activityRegistry = registry
}

func (t *Toolkit) SetMCPActivityBindings(bindings map[string]MCPActivityBinding) {
	if t == nil {
		return
	}
	t.mcpActivityBindings = cloneMCPActivityBindings(bindings)
}

func cloneMCPActivityBindings(bindings map[string]MCPActivityBinding) map[string]MCPActivityBinding {
	if len(bindings) == 0 {
		return nil
	}
	out := make(map[string]MCPActivityBinding, len(bindings))
	for server, binding := range bindings {
		server = strings.TrimSpace(server)
		binding.PluginID = strings.TrimSpace(binding.PluginID)
		if server != "" && binding.PluginID != "" && binding.Kind != "" {
			out[server] = binding
		}
	}
	return out
}

func (t *Toolkit) executeActivityBoundToolResult(ctx context.Context, call providers.ToolCall, tool Tool, serverName string) (toolresult.Result, error) {
	binding, bound := t.mcpActivityBindings[strings.TrimSpace(serverName)]
	if !bound {
		return t.executeKnownToolResult(ctx, call, tool)
	}
	if cuaActionIsGlobal(call.Arguments) {
		return t.executeKnownToolResult(ctx, call, tool)
	}
	if t.activityRegistry == nil {
		return toolresult.Result{}, errors.New("activity registry is unavailable")
	}
	threadID := ""
	workdir := ""
	if t.env != nil {
		threadID = strings.TrimSpace(t.env.SessionID)
		workdir = strings.TrimSpace(t.env.RootDir)
	}
	if threadID == "" {
		return toolresult.Result{}, errors.New("activity-bound MCP tool requires a thread context")
	}
	target := mcpActivityTarget(call.Arguments)
	if binding.Kind == activity.KindCUA {
		target = t.canonicalCUATarget(ctx, tool, target)
	}
	sequence, isSequence := parseCUASequence(call.Arguments)

	// The lease lifecycle (Acquire, before/after CheckControl, re-target, and the
	// state/preview/error Update) is the shared spine in runActivityBoundAction.
	// Only the CUA-specific derivations stay here as hooks.
	spec := activitySpec{Kind: binding.Kind, ThreadID: threadID, Workdir: workdir, PluginID: binding.PluginID, Target: target}
	hooks := activityHooks{
		run: func(ctx context.Context, session activity.Session, lease activity.Lease) (toolresult.Result, string, error) {
			if isSequence {
				return t.executeCUASequence(ctx, tool, threadID, session.ID, lease.Token, sequence)
			}
			if binding.Kind == activity.KindCUA {
				call.Arguments = t.prepareCUAArguments(tool, call.Arguments, lease.Token)
				if cuaObservationAction(call.Arguments) {
					result, err := t.executeKnownToolResultAllowRepeated(ctx, call, tool)
					return result, "", err
				}
			}
			result, err := t.executeKnownToolResult(ctx, call, tool)
			return result, "", err
		},
		controlState: func(result toolresult.Result) activity.State {
			return cuaActivityControlState(call.Arguments, result)
		},
		identity:    cuaActivityWindowIdentity,
		interaction: cuaActivityInteraction,
		preview: func(session activity.Session, result toolresult.Result) (string, error) {
			return t.persistActivityPreview(session.ID, result)
		},
	}
	return t.runActivityBoundAction(ctx, spec, hooks)
}

func cuaActivityWindowIdentity(result toolresult.Result) (int, uint32) {
	var structured struct {
		ProcessID int    `json:"process_id"`
		WindowID  uint32 `json:"window_id"`
	}
	if json.Unmarshal(result.StructuredContent, &structured) != nil {
		return 0, 0
	}
	return structured.ProcessID, structured.WindowID
}

func cuaActionIsGlobal(arguments string) bool {
	var input struct {
		Action string `json:"action"`
		App    string `json:"app"`
	}
	if json.Unmarshal([]byte(arguments), &input) != nil {
		return false
	}
	switch input.Action {
	case "list_apps":
		return strings.TrimSpace(input.App) == ""
	case "permission_status", "request_permissions":
		return true
	default:
		return false
	}
}

func (t *Toolkit) canonicalCUATarget(ctx context.Context, tool Tool, target string) string {
	target = strings.TrimSpace(target)
	if target == "" {
		return target
	}
	arguments, _ := json.Marshal(map[string]any{"action": "list_apps", "app": target})
	// Target resolution is an internal read-before-action preflight. It must run
	// on every CUA call even when the model reuses the same localized app alias;
	// otherwise the generic repeated-input guard eventually leaks that alias into
	// the activity identity and needlessly restarts the live PiP stream.
	result, err := t.executeKnownToolResultAllowRepeated(ctx, providers.ToolCall{Name: tool.Name(), Arguments: string(arguments)}, tool)
	if err != nil || result.IsError {
		return target
	}
	return canonicalCUATargetFromApps(target, result.StructuredContent)
}

func canonicalCUATargetFromApps(target string, structured json.RawMessage) string {
	var payload struct {
		ResolvedApp *struct {
			ID               string `json:"id"`
			BundleIdentifier string `json:"bundleIdentifier"`
			Path             string `json:"path"`
		} `json:"resolved_app"`
		Apps []struct {
			ID               string `json:"id"`
			DisplayName      string `json:"displayName"`
			BundleIdentifier string `json:"bundleIdentifier"`
			Path             string `json:"path"`
		} `json:"apps"`
	}
	if json.Unmarshal(structured, &payload) != nil {
		return strings.TrimSpace(target)
	}
	if payload.ResolvedApp != nil {
		if canonical := strings.TrimSpace(payload.ResolvedApp.BundleIdentifier); canonical != "" {
			return canonical
		}
		if canonical := strings.TrimSpace(payload.ResolvedApp.Path); canonical != "" {
			return canonical
		}
		if canonical := strings.TrimSpace(payload.ResolvedApp.ID); canonical != "" {
			return canonical
		}
	}
	normalized := strings.ToLower(strings.TrimSpace(target))
	for _, app := range payload.Apps {
		if normalized != strings.ToLower(strings.TrimSpace(app.ID)) &&
			normalized != strings.ToLower(strings.TrimSpace(app.DisplayName)) &&
			normalized != strings.ToLower(strings.TrimSpace(app.BundleIdentifier)) &&
			normalized != strings.ToLower(strings.TrimSpace(app.Path)) {
			continue
		}
		if canonical := strings.TrimSpace(app.BundleIdentifier); canonical != "" {
			return canonical
		}
		if canonical := strings.TrimSpace(app.Path); canonical != "" {
			return canonical
		}
		if canonical := strings.TrimSpace(app.ID); canonical != "" {
			return canonical
		}
	}
	return strings.TrimSpace(target)
}

func cuaActivityInteraction(result toolresult.Result) *activity.Interaction {
	var structured struct {
		Interaction *activity.Interaction `json:"interaction"`
	}
	if json.Unmarshal(result.StructuredContent, &structured) != nil || structured.Interaction == nil {
		return nil
	}
	interaction := structured.Interaction
	if interaction.Kind != "click" && interaction.Kind != "drag" && interaction.Kind != "scroll" && interaction.Kind != "type" {
		return nil
	}
	if interaction.X < 0 || interaction.X > 1 || interaction.Y < 0 || interaction.Y > 1 {
		return nil
	}
	if (interaction.ToX != nil && (*interaction.ToX < 0 || *interaction.ToX > 1)) ||
		(interaction.ToY != nil && (*interaction.ToY < 0 || *interaction.ToY > 1)) {
		return nil
	}
	interaction.Revision = time.Now().UnixNano()
	return interaction
}

func cuaActivityControlState(arguments string, result toolresult.Result) activity.State {
	var input map[string]any
	_ = json.Unmarshal([]byte(arguments), &input)
	policy, _ := input["foreground_policy"].(string)
	// require forcibly activates the target up front (a visible takeover) regardless
	// of which per-action level ultimately runs, so it is always foreground.
	if policy == "require" {
		return activity.StateForegroundControlled
	}
	var structured map[string]any
	if json.Unmarshal(result.StructuredContent, &structured) == nil {
		// A visible focus change means the foreground moved, whatever the mechanism.
		if changed, _ := structured["foreground_changed"].(bool); changed {
			return activity.StateForegroundControlled
		}
		switch mechanism, _ := structured["mechanism"].(string); mechanism {
		case "foreground_native":
			return activity.StateForegroundControlled
		case "background_ax", "background_directed":
			// Directed input (CGEvent.postToPid) reaches the target process without
			// activating it, so it is a background-controlled state like plain AX.
			return activity.StateBackgroundControlled
		}
	}
	// No mechanism reported (observe, permission checks): allow alone does not take
	// the foreground, so the action stayed in the background.
	return activity.StateBackgroundControlled
}

func parseCUASequence(arguments string) (cuaSequenceRequest, bool) {
	var request cuaSequenceRequest
	if json.Unmarshal([]byte(arguments), &request) != nil || request.Action != "sequence" {
		return cuaSequenceRequest{}, false
	}
	return request, true
}

func (t *Toolkit) executeCUASequence(ctx context.Context, tool Tool, threadID, activityID, leaseToken string, request cuaSequenceRequest) (toolresult.Result, string, error) {
	// Track the most-disruptive control level any step actually used so the whole
	// sequence maps to an honest activity state (a background sequence must not look
	// foreground just because foreground_policy was allowed). The generic risk
	// machine lives in runRiskSequence; only the CUA-specific envelope inheritance,
	// mechanism accumulation, per-step interaction publish, and result shape stay.
	control := sequenceControl{}
	latestSnapshot := request.SnapshotID
	supportsSnapshots := cuaSchemaProperty(tool, "snapshot_id")
	hooks := riskSequenceHooks{
		augment: func(step map[string]any) {
			if _, ok := step["app"]; !ok && request.App != "" {
				step["app"] = request.App
			}
			if _, ok := step["foreground_policy"]; !ok && request.ForegroundPolicy != "" {
				step["foreground_policy"] = request.ForegroundPolicy
			}
			if supportsSnapshots {
				if _, ok := step["snapshot_id"]; !ok && latestSnapshot != "" {
					step["snapshot_id"] = latestSnapshot
				}
				if _, ok := step["mode"]; !ok && request.Mode != "" {
					step["mode"] = request.Mode
				}
				if _, ok := step["after"]; !ok && request.After != "" {
					step["after"] = request.After
				}
			}
			encoded, _ := json.Marshal(step)
			prepared := t.prepareCUAArguments(tool, string(encoded), leaseToken)
			_ = json.Unmarshal([]byte(prepared), &step)
		},
		observe: func(stepArgs string, result toolresult.Result) {
			control.observe(stepArgs, result)
			var state struct {
				SnapshotID string `json:"snapshot_id"`
			}
			if json.Unmarshal(result.StructuredContent, &state) == nil && state.SnapshotID != "" {
				latestSnapshot = state.SnapshotID
			} else if !cuaObservationAction(stepArgs) {
				latestSnapshot = ""
			}
		},
		afterStep: func(_ int, stepResult toolresult.Result) (string, error) {
			interaction := cuaActivityInteraction(stepResult)
			if interaction == nil {
				return "", nil
			}
			if err := t.activityRegistry.CheckControl(threadID, activityID, leaseToken); err != nil {
				return "control_revoked", err
			}
			if _, err := t.activityRegistry.UpdateWithLease(activity.Lease{ThreadID: threadID, ActivityID: activityID, Token: leaseToken}, activity.UpdateOptions{Interaction: interaction}); err != nil {
				return "partial", err
			}
			return "", nil
		},
		build: func(status string, completed []map[string]any, nextStep int, lastImage *toolresult.ContentPart) toolresult.Result {
			return cuaSequenceResult(status, completed, nextStep, lastImage, control)
		},
	}
	return t.runRiskSequence(ctx, tool, threadID, activityID, leaseToken, "CUA sequence", request.Steps, hooks)
}

// sequenceControl accumulates the highest control level reached across a CUA
// sequence's steps so the aggregate result can report an accurate mechanism.
type sequenceControl struct {
	mechanism         string
	foregroundChanged bool
}

func mechanismRank(mechanism string) int {
	switch mechanism {
	case "foreground_native":
		return 3
	case "background_directed":
		return 2
	case "background_ax":
		return 1
	default:
		return 0
	}
}

func (c *sequenceControl) observe(arguments string, result toolresult.Result) {
	var structured map[string]any
	if json.Unmarshal(result.StructuredContent, &structured) == nil {
		if mechanism, _ := structured["mechanism"].(string); mechanismRank(mechanism) > mechanismRank(c.mechanism) {
			c.mechanism = mechanism
		}
		if changed, _ := structured["foreground_changed"].(bool); changed {
			c.foregroundChanged = true
		}
	}
	// A require step force-activates the target even when its own mechanism reads as
	// background, so treat it as a foreground change for the sequence.
	var input map[string]any
	if json.Unmarshal([]byte(arguments), &input) == nil {
		if policy, _ := input["foreground_policy"].(string); policy == "require" {
			c.foregroundChanged = true
		}
	}
}

func cuaSequenceResult(status string, completed []map[string]any, nextStep int, imagePart *toolresult.ContentPart, control sequenceControl) toolresult.Result {
	payload := map[string]any{"status": status, "completed_steps": completed, "next_step": nextStep}
	if control.mechanism != "" {
		payload["mechanism"] = control.mechanism
	}
	if control.foregroundChanged {
		payload["foreground_changed"] = true
	}
	structured, _ := json.Marshal(payload)
	content := []toolresult.ContentPart{{Type: toolresult.ContentTypeText, Text: fmt.Sprintf("CUA sequence %s after %d step(s).", status, len(completed))}}
	if imagePart != nil {
		content = append(content, *imagePart)
	}
	return toolresult.Result{Content: content, StructuredContent: structured, IsError: status == "failed" || status == "partial"}
}

func (t *Toolkit) persistActivityPreview(activityID string, result toolresult.Result) (string, error) {
	if t == nil || t.env == nil || strings.TrimSpace(t.env.SessionDir) == "" {
		return "", nil
	}
	for _, part := range result.Content {
		if part.Type != toolresult.ContentTypeImage || strings.TrimSpace(part.Data) == "" {
			continue
		}
		data, err := base64.StdEncoding.DecodeString(part.Data)
		if err != nil {
			return "", fmt.Errorf("decode activity preview: %w", err)
		}
		if len(data) > 32*1024*1024 {
			return "", fmt.Errorf("activity preview exceeds 32 MiB")
		}
		extension := ".img"
		switch strings.ToLower(strings.TrimSpace(part.MIMEType)) {
		case "image/png":
			extension = ".png"
			data = cropTransparentPNG(data)
		case "image/jpeg":
			extension = ".jpg"
		case "image/webp":
			extension = ".webp"
		}
		dir := filepath.Join(t.env.SessionDir, "activities", strings.TrimSpace(activityID))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", fmt.Errorf("create activity preview directory: %w", err)
		}
		path := filepath.Join(dir, "preview"+extension)
		temporary := path + ".tmp"
		if err := os.WriteFile(temporary, data, 0o600); err != nil {
			return "", fmt.Errorf("write activity preview: %w", err)
		}
		if err := os.Rename(temporary, path); err != nil {
			_ = os.Remove(temporary)
			return "", fmt.Errorf("publish activity preview: %w", err)
		}
		return (&url.URL{Scheme: "file", Path: path}).String(), nil
	}
	return "", nil
}

func cropTransparentPNG(data []byte) []byte {
	source, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return data
	}
	bounds := source.Bounds()
	visible := image.Rectangle{Min: bounds.Max, Max: bounds.Min}
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			_, _, _, alpha := source.At(x, y).RGBA()
			if alpha < 0x8000 {
				continue
			}
			if x < visible.Min.X {
				visible.Min.X = x
			}
			if y < visible.Min.Y {
				visible.Min.Y = y
			}
			if x+1 > visible.Max.X {
				visible.Max.X = x + 1
			}
			if y+1 > visible.Max.Y {
				visible.Max.Y = y + 1
			}
		}
	}
	if visible.Empty() || visible == bounds {
		return data
	}
	cropped := image.NewNRGBA(image.Rect(0, 0, visible.Dx(), visible.Dy()))
	draw.Draw(cropped, cropped.Bounds(), source, visible.Min, draw.Src)
	var output bytes.Buffer
	if err := png.Encode(&output, cropped); err != nil {
		return data
	}
	return output.Bytes()
}

func mcpActivityTarget(arguments string) string {
	var values map[string]any
	if json.Unmarshal([]byte(arguments), &values) != nil {
		return ""
	}
	for _, key := range []string{"app", "bundle_id", "target"} {
		if value, ok := values[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func cuaSchemaProperty(tool Tool, name string) bool {
	properties, _ := tool.Definition().InputSchema["properties"].(map[string]any)
	_, ok := properties[name]
	return ok
}

// Capability-driven defaults apply at the shared invocation boundary, including
// Code Mode. Alternative CUA extensions opt in by declaring these schema fields.
func (t *Toolkit) prepareCUAArguments(tool Tool, arguments, leaseToken string) string {
	var args map[string]any
	if json.Unmarshal([]byte(arguments), &args) != nil || args == nil {
		return arguments
	}
	if cuaSchemaProperty(tool, "control_epoch") {
		args["control_epoch"] = fmt.Sprintf("%x", sha256.Sum256([]byte(leaseToken)))
	}
	textOnly := t.env != nil && t.env.ImageInputSupported != nil && !*t.env.ImageInputSupported
	if cuaSchemaProperty(tool, "mode") {
		if _, present := args["mode"]; !present || textOnly {
			if textOnly {
				args["mode"] = "ax"
			} else {
				args["mode"] = "both"
			}
		}
	}
	if textOnly && cuaSchemaProperty(tool, "after") {
		if _, present := args["after"]; present {
			args["after"] = "ax"
		}
	}
	encoded, err := json.Marshal(args)
	if err != nil {
		return arguments
	}
	return string(encoded)
}

func cuaObservationAction(arguments string) bool {
	var input struct {
		Action string `json:"action"`
	}
	if json.Unmarshal([]byte(arguments), &input) != nil {
		return false
	}
	return input.Action == "observe" || input.Action == "query_snapshot" || input.Action == "wait_for_change" || input.Action == "wait_for"
}
