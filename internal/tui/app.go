package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/blueberrycongee/wuu/internal/agent"
)

// Config defines runtime dependencies for the interactive UI.
type Config struct {
	Provider     string
	Model        string
	ConfigPath   string
	MemoryPath   string
	SessionDir   string // .wuu/sessions/ directory for session isolation
	ResumeID     string // session ID to resume (empty = new session)
	RunPrompt    func(ctx context.Context, prompt string) (string, error)
	StreamRunner *agent.StreamRunner // optional, used when available
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
	program := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := program.Run(); err != nil {
		return fmt.Errorf("run tui: %w", err)
	}
	return nil
}
