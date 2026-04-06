package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// Config defines runtime dependencies for the interactive UI.
type Config struct {
	Provider   string
	Model      string
	ConfigPath string
	MemoryPath string
	RunPrompt  func(ctx context.Context, prompt string) (string, error)
}

// Run starts the interactive terminal UI.
func Run(cfg Config) error {
	if cfg.RunPrompt == nil {
		return errors.New("run prompt function is required")
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
