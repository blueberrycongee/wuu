package runtime

import (
	"fmt"
	"strings"
)

// HostKind identifies where the Wuu core process is running. It describes the
// process host, not the agent's product identity or the sandbox implementation.
type HostKind string

const (
	HostLocal HostKind = "local"
	HostCloud HostKind = "cloud"
)

// Host describes the immutable process identity supplied by the shell or cloud
// control plane when it starts an agent runtime.
type Host struct {
	Kind       HostKind
	InstanceID string
}

// ResolveHost validates and normalizes host metadata. The zero value preserves
// the existing local runtime behavior.
func ResolveHost(host Host) (Host, error) {
	host.Kind = HostKind(strings.TrimSpace(string(host.Kind)))
	host.InstanceID = strings.TrimSpace(host.InstanceID)
	if host.Kind == "" {
		host.Kind = HostLocal
	}
	switch host.Kind {
	case HostLocal:
		if host.InstanceID != "" {
			return Host{}, fmt.Errorf("local host does not accept an instance id")
		}
		return host, nil
	case HostCloud:
		if host.InstanceID == "" {
			return Host{}, fmt.Errorf("cloud host requires an instance id")
		}
		return host, nil
	default:
		return Host{}, fmt.Errorf("unsupported runtime host %q (want %q or %q)", host.Kind, HostLocal, HostCloud)
	}
}

// HostInfo returns the session host with the backward-compatible local default.
// NewSession always stores a validated value; the fallback also keeps manually
// assembled test and embedding sessions on the historical local behavior.
func (s *Session) HostInfo() Host {
	if s == nil || s.Host.Kind == "" {
		return Host{Kind: HostLocal}
	}
	return s.Host
}
