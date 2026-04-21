package tools

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/blueberrycongee/wuu/internal/cron"
	"github.com/blueberrycongee/wuu/internal/providers"
)

func taskStorePath(rootDir string) string {
	return filepath.Join(rootDir, ".wuu", "scheduled_tasks.json")
}

type ScheduleCronTool struct{ env *Env }

func NewScheduleCronTool(env *Env) *ScheduleCronTool { return &ScheduleCronTool{env: env} }
func (t *ScheduleCronTool) Name() string             { return "schedule_cron" }
func (t *ScheduleCronTool) IsReadOnly() bool          { return false }
func (t *ScheduleCronTool) IsConcurrencySafe() bool   { return false }

func (t *ScheduleCronTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
		Name: "schedule_cron",
		Description: "Create a scheduled task that runs a prompt at cron intervals. " +
			"The task can be recurring (runs repeatedly until deleted or expired) or one-shot (runs once).",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"cron": map[string]any{
					"type":        "string",
					"description": "5-field cron expression in local time (min hour dom month dow). Example: */5 * * * *",
				},
				"prompt": map[string]any{
					"type":        "string",
					"description": "The prompt to execute each time the task fires.",
				},
				"recurring": map[string]any{
					"type":        "boolean",
					"description": "If true, the task repeats until deleted or it expires (7 days). If false, it runs once.",
				},
				"durable": map[string]any{
					"type":        "boolean",
					"description": "If true (default), persist to disk. If false, session-only.",
				},
			},
			"required": []string{"cron", "prompt"},
		},
	}
}

func (t *ScheduleCronTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Cron      string `json:"cron"`
		Prompt    string `json:"prompt"`
		Recurring bool   `json:"recurring"`
		Durable   bool   `json:"durable"`
	}
	if err := decodeArgs(argsJSON, &args); err != nil {
		return "", err
	}
	if args.Cron == "" || args.Prompt == "" {
		return "", fmt.Errorf("schedule_cron requires cron and prompt")
	}

	ce, err := cron.ParseCronExpression(args.Cron)
	if err != nil {
		return "", fmt.Errorf("invalid cron expression: %w", err)
	}

	next, err := ce.NextRun(time.Now())
	if err != nil {
		return "", fmt.Errorf("cron has no valid future run: %w", err)
	}
	if next.After(time.Now().AddDate(1, 0, 0)) {
		return "", fmt.Errorf("cron next run is more than 1 year away")
	}

	store := cron.NewTaskStore(taskStorePath(t.env.RootDir))
	tasks, _ := store.List()
	if len(tasks) >= cron.MaxJobs {
		return "", fmt.Errorf("maximum number of scheduled tasks reached (%d)", cron.MaxJobs)
	}

	task := cron.Task{
		ID:        cron.GenerateTaskID(),
		Cron:      args.Cron,
		Prompt:    args.Prompt,
		CreatedAt: time.Now().UnixMilli(),
		Recurring: args.Recurring,
	}

	// Default durable = true.
	if err := store.Add(task); err != nil {
		return "", fmt.Errorf("failed to save task: %w", err)
	}

	result := map[string]any{
		"id":       task.ID,
		"schedule": args.Cron,
		"prompt":   args.Prompt,
		"type":     map[bool]string{true: "recurring", false: "one-shot"}[args.Recurring],
	}
	return mustJSON(result)
}

type CancelCronTool struct{ env *Env }

func NewCancelCronTool(env *Env) *CancelCronTool { return &CancelCronTool{env: env} }
func (t *CancelCronTool) Name() string            { return "cancel_cron" }
func (t *CancelCronTool) IsReadOnly() bool         { return false }
func (t *CancelCronTool) IsConcurrencySafe() bool  { return false }

func (t *CancelCronTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
		Name:        "cancel_cron",
		Description: "Cancel (delete) a scheduled task by its ID.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "The task ID to cancel.",
				},
			},
			"required": []string{"id"},
		},
	}
}

func (t *CancelCronTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		ID string `json:"id"`
	}
	if err := decodeArgs(argsJSON, &args); err != nil {
		return "", err
	}
	if args.ID == "" {
		return "", fmt.Errorf("cancel_cron requires id")
	}

	store := cron.NewTaskStore(taskStorePath(t.env.RootDir))
	if err := store.Remove(args.ID); err != nil {
		return "", fmt.Errorf("failed to cancel task: %w", err)
	}

	result := map[string]any{"cancelled": args.ID}
	return mustJSON(result)
}

type ListCronTool struct{ env *Env }

func NewListCronTool(env *Env) *ListCronTool { return &ListCronTool{env: env} }
func (t *ListCronTool) Name() string             { return "list_cron" }
func (t *ListCronTool) IsReadOnly() bool          { return true }
func (t *ListCronTool) IsConcurrencySafe() bool   { return true }

func (t *ListCronTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
		Name:        "list_cron",
		Description: "List all scheduled tasks with their IDs, schedules, and prompts.",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}
}

func (t *ListCronTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	store := cron.NewTaskStore(taskStorePath(t.env.RootDir))
	tasks, err := store.List()
	if err != nil {
		return "", fmt.Errorf("failed to list tasks: %w", err)
	}

	now := time.Now().UnixMilli()
	var items []map[string]any
	for _, task := range tasks {
		typeLabel := "one-shot"
		if task.Recurring {
			typeLabel = "recurring"
		}
		if cron.IsExpired(task, now) {
			typeLabel += " [expired]"
		}
		items = append(items, map[string]any{
			"id":       task.ID,
			"schedule": task.Cron,
			"type":     typeLabel,
			"prompt":   task.Prompt,
		})
	}

	return mustJSON(map[string]any{"tasks": items, "count": len(items)})
}
