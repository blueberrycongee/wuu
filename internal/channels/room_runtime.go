package channels

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/blueberrycongee/wuu/internal/securefs"
)

type roomRuntimeCredential struct {
	Runtime AgentRuntime
	Token   string
}

func (s *Service) prepareRoomRuntime(roomID, roomName string) (roomRuntimeCredential, error) {
	id, err := randomID("runtime", 12)
	if err != nil {
		return roomRuntimeCredential{}, err
	}
	token, err := randomID("chat", 32)
	if err != nil {
		return roomRuntimeCredential{}, err
	}
	now := fromMillis(toMillis(s.now()))
	runtime := AgentRuntime{
		ID: id, Kind: PrincipalRoomRuntime, RoomID: roomID,
		Name: roomAgentName(roomName), MemoryDir: filepath.Join(s.dir, "runtimes", id, "memory"),
		EngineOverride: "wuu", Autostart: true, CreatedAt: now,
	}
	if err := securefs.Mkdir(runtime.MemoryDir); err != nil {
		return roomRuntimeCredential{}, fmt.Errorf("create room runtime directory: %w", err)
	}
	if err := securefs.PreCreateFile(filepath.Join(runtime.MemoryDir, agentMemoryIndexFile)); err != nil {
		return roomRuntimeCredential{}, fmt.Errorf("initialize room runtime memory: %w", err)
	}
	if err := securefs.WriteFileAtomic(filepath.Join(filepath.Dir(runtime.MemoryDir), agentTokenFile), []byte(token+"\n")); err != nil {
		return roomRuntimeCredential{}, fmt.Errorf("persist room runtime token: %w", err)
	}
	return roomRuntimeCredential{Runtime: runtime, Token: token}, nil
}

func insertRoomRuntimeTx(ctx context.Context, tx *sql.Tx, credential roomRuntimeCredential) error {
	runtime := credential.Runtime
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO collaboration_principals(id, kind) VALUES (?, 'room_runtime')`, runtime.ID); err != nil {
		return fmt.Errorf("insert room runtime principal: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO room_runtimes(id, room_id, memory_dir, token_hash, autostart, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`, runtime.ID, runtime.RoomID, runtime.MemoryDir,
		tokenHash(credential.Token), boolInt(runtime.Autostart), toMillis(runtime.CreatedAt)); err != nil {
		return fmt.Errorf("insert room runtime: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO agent_wake_state(agent_id, outstanding, pending, updated_at)
		VALUES (?, 0, 0, ?)`, runtime.ID, toMillis(runtime.CreatedAt)); err != nil {
		return fmt.Errorf("initialize room runtime wake state: %w", err)
	}
	return nil
}

func (s *Service) createRoomRuntime(ctx context.Context, roomID, roomName string) (AgentRuntime, error) {
	credential, err := s.prepareRoomRuntime(roomID, roomName)
	if err != nil {
		return AgentRuntime{}, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(filepath.Dir(credential.Runtime.MemoryDir))
		}
	}()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AgentRuntime{}, fmt.Errorf("begin room runtime create: %w", err)
	}
	defer tx.Rollback()
	if err := insertRoomRuntimeTx(ctx, tx, credential); err != nil {
		return AgentRuntime{}, err
	}
	if err := tx.Commit(); err != nil {
		return AgentRuntime{}, fmt.Errorf("commit room runtime create: %w", err)
	}
	keep = true
	return credential.Runtime, nil
}

func (s *Service) ensureRoomRuntimes(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT room.id, room.name
		FROM rooms room
		LEFT JOIN room_runtimes runtime ON runtime.room_id = room.id
		WHERE room.kind = 'channel' AND runtime.id IS NULL
		ORDER BY room.created_at, room.id`)
	if err != nil {
		return fmt.Errorf("list rooms missing runtimes: %w", err)
	}
	type record struct{ id, name string }
	var records []record
	for rows.Next() {
		var item record
		if err := rows.Scan(&item.id, &item.name); err != nil {
			rows.Close()
			return fmt.Errorf("scan room missing runtime: %w", err)
		}
		records = append(records, item)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close rooms missing runtimes: %w", err)
	}
	for _, item := range records {
		if _, err := s.createRoomRuntime(ctx, item.id, item.name); err != nil {
			return fmt.Errorf("backfill room runtime: %w", err)
		}
	}
	return nil
}

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
	if runtime, err := s.GetRoomRuntime(ctx, id); err == nil {
		if !runtime.Autostart {
			return AgentRuntime{}, ErrNotFound
		}
		return runtime, nil
	} else if !errors.Is(err, ErrNotFound) {
		return AgentRuntime{}, err
	}
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
	rows, err := s.db.QueryContext(ctx, `
		SELECT runtime.id, runtime.room_id, room.name, runtime.memory_dir, runtime.autostart, runtime.created_at
		FROM room_runtimes runtime JOIN rooms room ON room.id = runtime.room_id
		WHERE runtime.autostart = 1 ORDER BY runtime.created_at, runtime.id`)
	if err != nil {
		return nil, fmt.Errorf("list room runtimes: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var runtime AgentRuntime
		var autostart int
		var createdAt int64
		if err := rows.Scan(&runtime.ID, &runtime.RoomID, &runtime.Name, &runtime.MemoryDir, &autostart, &createdAt); err != nil {
			return nil, fmt.Errorf("scan room runtime: %w", err)
		}
		runtime.Kind = PrincipalRoomRuntime
		runtime.EngineOverride = "wuu"
		runtime.Autostart = autostart != 0
		runtime.CreatedAt = fromMillis(createdAt)
		runtimes = append(runtimes, runtime)
	}
	return runtimes, rows.Err()
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
	var storedHash string
	if err := s.db.QueryRowContext(ctx, `SELECT token_hash FROM room_runtimes WHERE id = ?`, id).Scan(&storedHash); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AgentRuntime{}, ErrUnauthorized
		}
		return AgentRuntime{}, fmt.Errorf("authenticate room runtime: %w", err)
	}
	actual := tokenHash(token)
	if len(actual) != len(storedHash) || subtle.ConstantTimeCompare([]byte(actual), []byte(storedHash)) != 1 {
		return AgentRuntime{}, ErrUnauthorized
	}
	runtime, err := s.GetRoomRuntime(ctx, id)
	if err == nil && !runtime.Autostart {
		return AgentRuntime{}, ErrUnauthorized
	}
	return runtime, err
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
		UNION ALL
		SELECT 1 FROM room_runtimes WHERE room_id = ? AND id = ? AND autostart = 1
		LIMIT 1`, roomID, principalID, roomID, principalID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: principal %q cannot access room %q", ErrUnauthorized, principalID, roomID)
	}
	if err != nil {
		return fmt.Errorf("validate room principal access: %w", err)
	}
	return nil
}
