package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/hooks"
)

// Config defines runtime dependencies for the interactive UI.
type Config struct {
	Provider         string
	Model            string
	ConfigPath       string
	MemoryPath       string
	SessionDir       string // .wuu/sessions/ directory for session isolation
	ResumeID         string // session ID to resume (empty = new session)
	MaxContextTokens int
	RequestTimeout   time.Duration
	RunPrompt        func(ctx context.Context, prompt string) (string, error)
	StreamRunner     *agent.StreamRunner // optional, used when available
	HookDispatcher   *hooks.Dispatcher   // optional, dispatches lifecycle hooks
	OnSessionID      func(string)        // optional, called when the session ID changes
}

// Run starts the interactive terminal UI.
func Run(cfg Config) error {
	if cfg.RunPrompt == nil && cfg.StreamRunner == nil {
		return errors.New("run prompt function or stream runner is required")
	}
	if strings.TrimSpace(cfg.Provider) == "" {
		return errors.New("provider is required")
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return errors.New("model is required")
	}
	if strings.TrimSpace(cfg.ConfigPath) == "" {
		return errors.New("config path is required")
	}

	m := NewModel(cfg)
	program := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseAllMotion())
	finalModel, err := program.Run()
	if err != nil {
		return fmt.Errorf("run tui: %w", err)
	}

	// Print resume hint after exiting alt screen — only if conversation happened.
	if fm, ok := finalModel.(Model); ok && fm.sessionID != "" && fm.sessionCreated && len(fm.entries) > 0 {
		fmt.Println()
		fmt.Printf("To resume this session:\n")
		fmt.Printf("  wuu --resume %s\n", fm.sessionID)
		fmt.Println()
	}

	return nil
}
