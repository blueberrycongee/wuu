package pluginhost

// ClientInvalidation is a content-free hint to re-read plugin-owned state.
// Identity and routing fields are supplied by the host, never plugin input.
type ClientInvalidation struct {
	PluginID      string `json:"plugin_id"`
	Fingerprint   string `json:"fingerprint"`
	UserSpaceID   string `json:"user_space_id"`
	ProductDomain string `json:"product_domain"`
	Topic         string `json:"topic"`
}

func KernelClientInvalidationDescriptor() ServiceDescriptor {
	return ServiceDescriptor{Name: "host.client.invalidate", Version: "1.0.0", Methods: []ServiceMethodDescriptor{{Name: "call", InputSchema: "host.client.invalidate.input.v1", OutputSchema: "host.client.invalidate.output.v1"}}}
}
