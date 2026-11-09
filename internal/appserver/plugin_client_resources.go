package appserver

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/blueberrycongee/wuu/internal/access"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

// Opaque business input cannot replace the declared package route. The handler
// independently resolves this subject/fingerprint against its retained graph.
func pluginClientRouteResources(req Request) ([]access.Resource, error) {
	var params struct {
		ID          string `json:"id"`
		Fingerprint string `json:"fingerprint"`
		Generation  string `json:"graph_generation"`
		Service     string `json:"service"`
		Method      string `json:"method"`
		RequestID   string `json:"request_id"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, err
	}
	resources := []access.Resource{{Kind: "plugin-subject", ID: strings.TrimSpace(params.ID)}, {Kind: "plugin-fingerprint", ID: strings.TrimSpace(params.Fingerprint)}, {Kind: "plugin-generation", ID: strings.TrimSpace(params.Generation)}}
	for _, resource := range resources {
		if resource.ID == "" {
			return nil, errors.New("plugin subject, fingerprint and graph generation required")
		}
	}
	if req.Method == MethodPluginClientRequest {
		method := strings.TrimSpace(params.Method)
		if method == "" || len(method) > 128 {
			return nil, errors.New("plugin client method required")
		}
		resources = append(resources, access.Resource{Kind: "plugin-method", ID: method})
		service := strings.TrimSpace(params.Service)
		if len(service) > 128 || (params.Service != "" && service == "") {
			return nil, errors.New("invalid plugin service")
		}
		if service != "" {
			resources = append(resources, access.Resource{Kind: "plugin-service", ID: service})
		} else {
			resources = append(resources, access.Resource{Kind: "plugin-capability", ID: pluginhost.CapabilityPluginClientRequest})
		}
	}
	requestID := strings.TrimSpace(params.RequestID)
	if params.RequestID != "" || req.Method == MethodPluginClientCancel {
		if requestID == "" || len(requestID) > 128 {
			return nil, errors.New("plugin client request id required")
		}
		resources = append(resources, access.Resource{Kind: "plugin-client-request", ID: requestID})
	}
	return resources, nil
}
