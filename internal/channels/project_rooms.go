package channels

// Existing conversations remain unbound until the user opens a project DM.
// The pair index becomes a (human, identity, project) index without deleting data.
func (s *Service) migrateProjectRooms() error {
	exists, err := s.tableHasColumn("direct_messages", "workspace_root")
	if err != nil || exists {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`CREATE TABLE direct_messages_projects (
 human_id TEXT NOT NULL,agent_id TEXT NOT NULL,workspace_root TEXT NOT NULL DEFAULT '',room_id TEXT NOT NULL UNIQUE,
 PRIMARY KEY(human_id,agent_id,workspace_root),
 FOREIGN KEY(agent_id) REFERENCES named_agents(id) ON DELETE CASCADE,
 FOREIGN KEY(room_id) REFERENCES rooms(id) ON DELETE CASCADE);
 INSERT INTO direct_messages_projects(human_id,agent_id,room_id) SELECT human_id,agent_id,room_id FROM direct_messages;
 DROP TABLE direct_messages;
 ALTER TABLE direct_messages_projects RENAME TO direct_messages;`)
	if err != nil {
		return err
	}
	return tx.Commit()
}
