package channels

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/blueberrycongee/wuu/internal/securefs"
	"os"
	"path/filepath"
)

// Fixtures for databases created before deterministic room scheduling.
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
