package channels

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (s *Service) GetRoomRuntime(ctx context.Context, id string) (AgentRuntime, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return AgentRuntime{}, errors.New("room runtime id is required")
	}
	var runtime AgentRuntime
	var autostart int
	var createdAt int64
	err := s.db.QueryRowContext(ctx, `
		SELECT runtime.id, runtime.room_id, room.name, runtime.memory_dir, runtime.autostart, runtime.created_at
		FROM room_runtimes runtime JOIN rooms room ON room.id = runtime.room_id
		WHERE runtime.id = ?`, id).Scan(&runtime.ID, &runtime.RoomID, &runtime.Name, &runtime.MemoryDir, &autostart, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return AgentRuntime{}, ErrNotFound
	}
	if err != nil {
		return AgentRuntime{}, fmt.Errorf("get room runtime: %w", err)
	}
	runtime.Kind = PrincipalRoomRuntime
	runtime.EngineOverride = "wuu"
	runtime.Autostart = autostart != 0
	runtime.CreatedAt = fromMillis(createdAt)
	return runtime, nil
}

func (s *Service) GetAgentRuntime(ctx context.Context, id string) (AgentRuntime, error) {
	agent, err := s.GetNamedAgent(ctx, id)
	if err != nil {
		return AgentRuntime{}, err
	}
	return runtimeFromNamedAgent(agent), nil
}

func runtimeFromNamedAgent(agent NamedAgent) AgentRuntime {
	return AgentRuntime{
		ID: agent.ID, Kind: PrincipalNamedAgent, Name: agent.Name, Role: agent.Role, MemoryDir: agent.MemoryDir,
		EngineOverride: agent.EngineOverride, ProviderOverride: agent.ProviderOverride,
		ModelOverride: agent.ModelOverride, EffortOverride: agent.EffortOverride,
		Autostart: agent.Autostart, CreatedAt: agent.CreatedAt,
	}
}

func (s *Service) ListAgentRuntimes(ctx context.Context) ([]AgentRuntime, error) {
	agents, err := s.ListNamedAgents(ctx)
	if err != nil {
		return nil, err
	}
	runtimes := make([]AgentRuntime, 0, len(agents)+4)
	for _, agent := range agents {
		runtimes = append(runtimes, runtimeFromNamedAgent(agent))
	}
	return runtimes, nil
}

func (s *Service) AuthenticatePrincipal(ctx context.Context, id, token string) (AgentRuntime, error) {
	id, token = strings.TrimSpace(id), strings.TrimSpace(token)
	if id == "" || token == "" {
		return AgentRuntime{}, ErrUnauthorized
	}
	if agent, err := s.AuthenticateAgent(ctx, id, token); err == nil {
		return runtimeFromNamedAgent(agent), nil
	} else if !errors.Is(err, ErrUnauthorized) {
		return AgentRuntime{}, err
	}
	return AgentRuntime{}, ErrUnauthorized
}

func (s *Service) loadPrincipalToken(ctx context.Context, id string) (string, error) {
	runtime, err := s.GetAgentRuntime(ctx, id)
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(runtime.MemoryDir), agentTokenFile))
	if err != nil {
		return "", fmt.Errorf("read collaboration principal token: %w", err)
	}
	token := strings.TrimSpace(string(raw))
	if _, err := s.AuthenticatePrincipal(ctx, id, token); err != nil {
		return "", err
	}
	return token, nil
}

func requireRoomPrincipalAccessTx(ctx context.Context, tx *sql.Tx, roomID, principalID string) error {
	var exists int
	err := tx.QueryRowContext(ctx, `
		SELECT 1 FROM room_members
		WHERE room_id = ? AND member_type = 'agent' AND member_id = ?
		LIMIT 1`, roomID, principalID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: principal %q cannot access room %q", ErrUnauthorized, principalID, roomID)
	}
	if err != nil {
		return fmt.Errorf("validate room principal access: %w", err)
	}
	return nil
}
