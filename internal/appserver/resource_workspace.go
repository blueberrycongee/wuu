package appserver

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"

	"github.com/blueberrycongee/wuu/internal/access"
)

func resolvedWorkspaceResource(path string) (access.Resource, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return access.Resource{}, errors.New("workspace identity required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return access.Resource{}, err
	}
	real, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return access.Resource{}, err
	}
	return access.Resource{Kind: "workspace", ID: filepath.Clean(real)}, nil
}

// Workspace ids are actual host-local roots, not caller aliases. Resolve the
// omitted default too: a global read/create grant cannot name a private root.
func (s *Server) requestResources(req Request) ([]access.Resource, error) {
	resources, err := requestResources(req)
	if err != nil {
		return nil, err
	}
	rule := resourceMethods[req.Method]
	if rule != "workspace" && req.Method != MethodThreadStart {
		return resources, nil
	}
	var params struct {
		Root string `json:"root"`
		CWD  string `json:"cwd"`
	}
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return nil, err
		}
	}
	root := params.Root
	if req.Method == MethodThreadStart {
		root = params.CWD
	}
	if strings.TrimSpace(root) == "" {
		if s.rt == nil {
			return nil, errors.New("runtime workspace required")
		}
		root = s.rt.RootDir
	}
	workspace, err := resolvedWorkspaceResource(root)
	if err != nil {
		return nil, err
	}
	filtered := make([]access.Resource, 0, len(resources)+1)
	for _, resource := range resources {
		if rule == "workspace" && (resource.Kind == "workspace" || resource.Kind == "global") {
			continue
		}
		filtered = append(filtered, resource)
	}
	return append(filtered, workspace), nil
}
