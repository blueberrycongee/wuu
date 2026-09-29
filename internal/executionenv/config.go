// Package executionenv owns execution environment selection and transport.
package executionenv

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// Config is user-owned configuration. Projects cannot supply executable profiles.
type Config struct {
	Default  string             `json:"default,omitempty"`
	Profiles map[string]Profile `json:"profiles,omitempty"`
}

// Profile describes the environment in which workspace tools execute. Shared
// opts conversations into one filesystem; their tool state remains separate.
type Profile struct {
	Python          string   `json:"python,omitempty"`
	HostWorkspace   string   `json:"host_workspace,omitempty"`
	MountReadOnly   bool     `json:"mount_read_only,omitempty"`
	Backend         string   `json:"backend"`
	Image           string   `json:"image,omitempty"`
	Host            string   `json:"host,omitempty"`
	Port            int      `json:"port,omitempty"`
	KnownHostsFile  string   `json:"known_hosts_file,omitempty"`
	IdentityFile    string   `json:"identity_file,omitempty"`
	Workspace       string   `json:"workspace,omitempty"`
	Worker          string   `json:"worker,omitempty"`
	Shared          bool     `json:"shared,omitempty"`
	Persistent      bool     `json:"persistent,omitempty"`
	Network         string   `json:"network,omitempty"`
	CPUs            float64  `json:"cpus,omitempty"`
	MemoryMB        int      `json:"memory_mb,omitempty"`
	LifetimeSeconds int      `json:"lifetime_seconds,omitempty"`
	ForwardEnv      []string `json:"forward_env,omitempty"`
	// Command is an explicitly installed transport adapter using the worker protocol.
	Command []string `json:"command,omitempty"`
}

var profileName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}$`)
var envName = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

func (c Config) Validate() error {
	if c.Default != "" && c.Default != "local" {
		if _, ok := c.Profiles[c.Default]; !ok {
			return fmt.Errorf("execution environment %q does not exist", c.Default)
		}
	}
	for name, p := range c.Profiles {
		if !profileName.MatchString(name) || name == "local" {
			return fmt.Errorf("invalid execution environment name %q", name)
		}
		if err := p.Validate(); err != nil {
			return fmt.Errorf("execution environment %q: %w", name, err)
		}
	}
	return nil
}

func (p Profile) Validate() error {
	if p.HostWorkspace != "" {
		if p.Backend != "docker" && p.Backend != "singularity" {
			return fmt.Errorf("host workspace mounts require a local container backend")
		}
		if !filepath.IsAbs(p.HostWorkspace) || strings.ContainsAny(p.HostWorkspace, ",:\n\r\x00") {
			return fmt.Errorf("host workspace must be an absolute path without mount separators")
		}
	}
	if (p.Backend == "daytona" || p.Backend == "vercel_sandbox") && p.CPUs != float64(int(p.CPUs)) {
		return fmt.Errorf("this backend requires a whole number of CPU cores")
	}

	if p.Backend == "ssh" && (p.CPUs > 0 || p.MemoryMB > 0) {
		return fmt.Errorf("SSH resource limits must be configured on the remote server")
	}
	switch p.Backend {
	case "docker", "singularity":
		if strings.TrimSpace(p.Image) == "" || strings.HasPrefix(p.Image, "-") {
			return fmt.Errorf("%s requires an image", p.Backend)
		}
	case "ssh":
		if p.Host == "" || strings.HasPrefix(p.Host, "-") || strings.ContainsAny(p.Host, " \t\r\n\x00") {
			return fmt.Errorf("SSH requires a host or configured host alias")
		}
	case "modal", "daytona", "vercel_sandbox":
		if len(p.Command) == 0 && strings.TrimSpace(p.Image) == "" {
			return fmt.Errorf("%s requires an image containing the execution worker", p.Backend)
		}
		if p.Backend == "vercel_sandbox" && p.MemoryMB > 0 {
			return fmt.Errorf("this backend derives memory from CPU allocation")
		}
	case "command":
		if len(p.Command) == 0 || strings.TrimSpace(p.Command[0]) == "" {
			return fmt.Errorf("command backend requires an execution transport command")
		}
	default:
		return fmt.Errorf("unknown execution backend %q", p.Backend)
	}
	if p.Workspace != "" && (!path.IsAbs(p.Workspace) || path.Clean(p.Workspace) != p.Workspace || strings.ContainsAny(p.Workspace, "\x00\r\n")) {
		return fmt.Errorf("workspace must be an absolute, normalized environment path")
	}
	if p.Port < 0 || p.Port > 65535 {
		return fmt.Errorf("invalid SSH port")
	}
	if p.Network != "" && p.Network != "enabled" && p.Network != "none" {
		return fmt.Errorf("network must be enabled or none")
	}
	if p.Network == "none" && p.Backend == "ssh" {
		return fmt.Errorf("SSH transport cannot enforce network isolation")
	}
	if p.CPUs < 0 || p.MemoryMB < 0 || p.LifetimeSeconds < 0 {
		return fmt.Errorf("resource limits cannot be negative")
	}
	for _, name := range p.ForwardEnv {
		if !envName.MatchString(name) {
			return fmt.Errorf("invalid forwarded environment variable %q", name)
		}
	}
	return nil
}

func (p Profile) WorkingDirectory() string {
	if p.Workspace != "" {
		return p.Workspace
	}
	return "/workspace"
}

func (p Profile) WorkerExecutable() string {
	if p.Worker != "" {
		return p.Worker
	}
	return "wuu"
}

// Identity includes the full policy so a changed profile cannot silently attach
// to an environment created with different mounts, credentials or network access.
func Identity(store, session, name string, p Profile) string {
	if p.Shared {
		session = "shared"
	}
	data, _ := json.Marshal(struct {
		Store, Session, Name string
		Profile              Profile
	}{store, session, name, p})
	sum := sha256.Sum256(data)
	return fmt.Sprintf("wuu-env-%x", sum[:16])
}
