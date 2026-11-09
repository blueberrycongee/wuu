package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentprofile"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/statepath"
)

type ListAgentProfilesTool struct{ env *Env }

type agentProfileToolView struct {
	Name           string    `json:"name"`
	Role           string    `json:"role,omitempty"`
	Description    string    `json:"description,omitempty"`
	ProfileDir     string    `json:"profile_dir,omitempty"`
	CreatedAt      time.Time `json:"created_at,omitempty"`
	LastResolvedAt time.Time `json:"last_resolved_at,omitempty"`
}

func NewListAgentProfilesTool(env *Env) *ListAgentProfilesTool {
	return &ListAgentProfilesTool{env: env}
}

func (t *ListAgentProfilesTool) Name() string            { return "list_agent_profiles" }
func (t *ListAgentProfilesTool) IsReadOnly() bool        { return true }
func (t *ListAgentProfilesTool) IsConcurrencySafe() bool { return true }

func (t *ListAgentProfilesTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
		Name: "list_agent_profiles",
		Description: "List named Agent Profiles with saved identity notes for persistent collaboration roles. " +
			"Use this before deciding whether a role should reuse an existing profile, create a new profile, or use a temporary worker without profile memory.",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}
}

func (t *ListAgentProfilesTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	wuuHome, err := statepath.Home("")
	if err != nil {
		return "", err
	}
	profiles, err := agentprofile.List(wuuHome)
	if err != nil {
		return "", err
	}
	return mustJSON(map[string]any{"action": "list_agent_profiles", "profiles": agentProfileToolViews(profiles), "count": len(profiles)})
}

type CreateAgentProfileTool struct{ env *Env }

func NewCreateAgentProfileTool(env *Env) *CreateAgentProfileTool {
	return &CreateAgentProfileTool{env: env}
}

func (t *CreateAgentProfileTool) Name() string            { return "create_agent_profile" }
func (t *CreateAgentProfileTool) IsReadOnly() bool        { return false }
func (t *CreateAgentProfileTool) IsConcurrencySafe() bool { return true }

func (t *CreateAgentProfileTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
		Name: "create_agent_profile",
		Description: "Create or update a named Agent Profile with saved identity notes for persistent collaboration roles. " +
			"Use only when the role is likely to recur or the user or agent policy asks for saved profile memory.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "Stable profile name accepted by compatible delegation plugins.",
				},
				"role": map[string]any{
					"type":        "string",
					"description": "Short role label, for example QA reviewer or release coordinator.",
				},
				"description": map[string]any{
					"type":        "string",
					"description": "Why this profile exists and what recurring work it should remember.",
				},
			},
			"required": []string{"name"},
		},
	}
}

func (t *CreateAgentProfileTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Name        string `json:"name"`
		Role        string `json:"role"`
		Description string `json:"description"`
	}
	if err := decodeArgs(argsJSON, &args); err != nil {
		return "", err
	}
	name := strings.TrimSpace(args.Name)
	if name == "" {
		return "", fmt.Errorf("create_agent_profile requires name")
	}
	wuuHome, err := statepath.Home("")
	if err != nil {
		return "", err
	}
	profile, created, err := agentprofile.Ensure(agentprofile.EnsureOptions{
		WuuHome:     wuuHome,
		Name:        name,
		Role:        args.Role,
		Description: args.Description,
	})
	if err != nil {
		return "", err
	}
	return mustJSON(map[string]any{
		"action":  "create_agent_profile",
		"profile": agentProfileToolViewFromSummary(profile),
		"created": created,
		"next_steps": []string{
			"Pass this profile name to a compatible delegation plugin when this recurring role should perform work.",
			"Use a temporary worker without agent_profile for one-off tasks that should not reuse or write profile memory.",
		},
	})
}

func agentProfileToolViews(profiles []agentprofile.Summary) []agentProfileToolView {
	out := make([]agentProfileToolView, 0, len(profiles))
	for _, profile := range profiles {
		out = append(out, agentProfileToolViewFromSummary(profile))
	}
	return out
}

func agentProfileToolViewFromSummary(profile agentprofile.Summary) agentProfileToolView {
	return agentProfileToolView{
		Name:           profile.Name,
		Role:           profile.Role,
		Description:    profile.Description,
		ProfileDir:     profile.ProfileDir,
		CreatedAt:      profile.CreatedAt,
		LastResolvedAt: profile.LastResolvedAt,
	}
}
