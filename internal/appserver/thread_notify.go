package appserver

import (
	"github.com/blueberrycongee/wuu/internal/runtime"
)

func (s *Server) notifyThreadStarted(binding *runtime.State, thread Thread) error {
	var err error
	thread, err = s.withProjectGrouping(binding, thread, nil)
	if err != nil {
		return err
	}
	thread = s.threadWithPersistedMetadata(binding, thread)
	return s.writeNotification(NotificationThreadStarted, ThreadStartedNotification{Thread: thread})
}

func (s *Server) notifyThreadUpdated(binding *runtime.State, thread Thread) error {
	var err error
	thread, err = s.withProjectGrouping(binding, thread, nil)
	if err != nil {
		return err
	}
	thread = s.threadWithPersistedMetadata(binding, thread)
	return s.writeNotification(NotificationThreadUpdated, ThreadUpdatedNotification{Thread: thread})
}

func (s *Server) threadWithPersistedMetadata(binding *runtime.State, thread Thread) Thread {
	if metadata, ok, err := binding.Sessions.Find(thread.ID); err == nil && ok {
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

func (s *Server) notifyOutboundBatch(binding *runtime.State, batch []outboundNotification) {
	for _, item := range batch {
		if params, ok := item.params.(ThreadUpdatedNotification); ok {
			_ = s.notifyThreadUpdated(binding, params.Thread)
			continue
		}
		_ = s.writeNotification(item.method, item.params)
	}
}
