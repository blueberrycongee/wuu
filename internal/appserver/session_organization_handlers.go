package appserver

import (
	"errors"
	"strings"
)

func (s *Server) handleSessionOrganizationList(req Request) error {
	binding, stateErr := s.rt.RetainState()
	if stateErr != nil {
		return s.writeResponse(req.ID, nil, stateErr)
	}
	defer binding.Close()
	organization, err := binding.Sessions.ListOrganization()
	return s.writeResponse(req.ID, SessionOrganizationListResult{Organization: organization}, err)
}

func (s *Server) handleOrganizationGroupCreate(req Request) error {
	binding, stateErr := s.rt.RetainState()
	if stateErr != nil {
		return s.writeResponse(req.ID, nil, stateErr)
	}
	defer binding.Close()

	var params OrganizationGroupCreateParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	group, err := binding.Sessions.CreateFolder(params.Name)
	return s.writeResponse(req.ID, OrganizationGroupResult{Group: group}, err)
}

func (s *Server) handleOrganizationGroupUpdate(req Request) error {
	binding, stateErr := s.rt.RetainState()
	if stateErr != nil {
		return s.writeResponse(req.ID, nil, stateErr)
	}
	defer binding.Close()

	var params OrganizationGroupUpdateParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	group, err := binding.Sessions.RenameFolder(params.ID, params.Name)
	return s.writeResponse(req.ID, OrganizationGroupResult{Group: group}, err)
}

func (s *Server) handleOrganizationGroupDelete(req Request) error {
	binding, stateErr := s.rt.RetainState()
	if stateErr != nil {
		return s.writeResponse(req.ID, nil, stateErr)
	}
	defer binding.Close()

	var params OrganizationGroupDeleteParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	sessions, err := binding.Sessions.List(0)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	affected := make([]string, 0)
	for _, sess := range sessions {
		if sess.FolderID == params.ID {
			affected = append(affected, sess.ID)
		}
	}
	if err := binding.Sessions.DeleteFolder(params.ID); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if err := s.writeResponse(req.ID, map[string]bool{"ok": true}, nil); err != nil {
		return err
	}
	for _, id := range affected {
		metadata, ok, findErr := binding.Sessions.Find(id)
		if findErr != nil || !ok {
			continue
		}
		thread, updateErr := s.threadAfterMetadataUpdate(binding, metadata)
		if updateErr == nil {
			_ = s.notifyThreadUpdated(binding, thread)
		}
	}
	return nil
}

func (s *Server) handleOrganizationGroupReorder(req Request) error {
	binding, stateErr := s.rt.RetainState()
	if stateErr != nil {
		return s.writeResponse(req.ID, nil, stateErr)
	}
	defer binding.Close()

	var params OrganizationGroupReorderParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if err := binding.Sessions.ReorderFolders(params.IDs); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	organization, err := binding.Sessions.ListOrganization()
	return s.writeResponse(req.ID, SessionOrganizationListResult{Organization: organization}, err)
}

func (s *Server) handleThreadOrganizationUpdate(req Request) error {
	binding, stateErr := s.rt.RetainState()
	if stateErr != nil {
		return s.writeResponse(req.ID, nil, stateErr)
	}
	defer binding.Close()

	var params ThreadOrganizationUpdateParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	id := strings.TrimSpace(params.ThreadID)
	if id == "" {
		return s.writeResponse(req.ID, nil, errors.New("thread_id is required"))
	}
	metadata, err := binding.Sessions.UpdateOrganization(id, params.FolderID)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	thread, err := s.threadAfterMetadataUpdate(binding, metadata)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if err := s.writeResponse(req.ID, ThreadOrganizationUpdateResult{Thread: thread}, nil); err != nil {
		return err
	}
	return s.notifyThreadUpdated(binding, thread)
}
