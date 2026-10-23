package appserver

import "github.com/blueberrycongee/wuu/internal/session"

func (s *Server) notifyThreadStarted(thread Thread) error {
	var err error
	thread, err = s.withProjectGrouping(thread, nil)
	if err != nil {
		return err
	}
	thread = s.threadWithPersistedMetadata(thread)
	return s.writeNotification(NotificationThreadStarted, ThreadStartedNotification{Thread: thread})
}

func (s *Server) notifyThreadUpdated(thread Thread) error {
	var err error
	thread, err = s.withProjectGrouping(thread, nil)
	if err != nil {
		return err
	}
	thread = s.threadWithPersistedMetadata(thread)
	return s.writeNotification(NotificationThreadUpdated, ThreadUpdatedNotification{Thread: thread})
}

func (s *Server) threadWithPersistedMetadata(thread Thread) Thread {
	if metadata, ok, err := session.Find(s.rt.SessionDir, thread.ID); err == nil && ok {
		if metadata.TitleRevision >= thread.TitleRevision {
			thread.Title = metadata.Title
			thread.TitleRevision = metadata.TitleRevision
			thread.Preview = firstNonEmpty(metadata.Title, thread.Preview, metadata.Summary)
		}
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
