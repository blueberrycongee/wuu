package appserver

import "github.com/blueberrycongee/wuu/internal/session"

func (s *Server) notifyThreadStarted(thread Thread) error {
	var err error
	thread, err = s.withProjectGrouping(thread, nil)
	if err != nil {
		return err
	}
	thread = s.threadWithPersistedOrganizationIdentity(thread)
	return s.writeNotification(NotificationThreadStarted, ThreadStartedNotification{Thread: thread})
}

func (s *Server) notifyThreadUpdated(thread Thread) error {
	var err error
	thread, err = s.withProjectGrouping(thread, nil)
	if err != nil {
		return err
	}
	thread = s.threadWithPersistedOrganizationIdentity(thread)
	return s.writeNotification(NotificationThreadUpdated, ThreadUpdatedNotification{Thread: thread})
}

func (s *Server) threadWithPersistedOrganizationIdentity(thread Thread) Thread {
	if metadata, ok, err := session.Find(s.rt.SessionDir, thread.ID); err == nil && ok {
		thread.Pinned = metadata.PinnedAt != nil
		thread.FolderID = metadata.FolderID
		thread.WorkspaceID = metadata.WorkspaceID
	}
	return thread
}

func (s *Server) notifyOutboundBatch(batch []outboundNotification) {
	for _, item := range batch {
		if params, ok := item.params.(ThreadUpdatedNotification); ok {
			_ = s.notifyThreadUpdated(params.Thread)
			continue
		}
		_ = s.writeNotification(item.method, item.params)
	}
}
