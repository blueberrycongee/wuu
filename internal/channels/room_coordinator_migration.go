package channels

import "context"

// Retain legacy transcripts, but never restore hidden model executions.
func (s *Service) initializeRoomScheduling(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.migrateRoomTurns(); err != nil {
		return err
	}
	return s.retireRoomRuntimes(ctx)
}
