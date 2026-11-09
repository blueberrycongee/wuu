package pluginhost

// ArtifactImportParams asks the host to copy one plugin-produced file into
// storage owned by the live tool execution's thread. Exactly one of Path or
// Data must be set. Data is standard base64.
type ArtifactImportParams struct {
	Path     string `json:"path,omitempty"`
	Data     string `json:"data,omitempty"`
	Name     string `json:"name,omitempty"`
	MIMEType string `json:"mime_type,omitempty"`
}

// ArtifactImportResult describes a thread-owned artifact. ID is its opaque,
// durable identity. URI is a digest-bound renderer capability and must not be
// stored independently or compared as the artifact identity; it never grants
// access to arbitrary local paths.
type ArtifactImportResult struct {
	ID       string `json:"id"`
	URI      string `json:"uri"`
	Name     string `json:"name"`
	MIMEType string `json:"mime_type"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
}
