package agentthread

import (
	"fmt"
	"strings"
)

const (
	RootPath = "/root"
	rootName = "root"
)

// AgentPath is the canonical address for an agent in a root thread tree.
// /root is the tree root, and each child segment is a task name made of
// lowercase ASCII letters, digits, or "_".
type AgentPath string

func RootAgentPath() AgentPath {
	return AgentPath(RootPath)
}

func ParseAgentPath(path string) (AgentPath, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("agent path must not be empty")
	}
	if !strings.HasPrefix(path, "/") {
		return "", fmt.Errorf("absolute agent paths must start with /root")
	}
	if path != RootPath && !strings.HasPrefix(path, RootPath+"/") {
		return "", fmt.Errorf("absolute agent paths must start with /root")
	}
	if strings.HasSuffix(path, "/") {
		return "", fmt.Errorf("agent path must not end with /")
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] != rootName {
		return "", fmt.Errorf("absolute agent paths must start with /root")
	}
	for _, part := range parts[1:] {
		if err := ValidateAgentName(part); err != nil {
			return "", err
		}
	}
	return AgentPath(path), nil
}

func JoinAgentPath(parent AgentPath, agentName string) (AgentPath, error) {
	if err := ValidateAgentName(agentName); err != nil {
		return "", err
	}
	if parent == "" {
		parent = RootAgentPath()
	}
	if _, err := ParseAgentPath(string(parent)); err != nil {
		return "", err
	}
	return AgentPath(string(parent) + "/" + agentName), nil
}

func ResolveAgentPath(current AgentPath, reference string) (AgentPath, error) {
	reference = strings.TrimSpace(reference)
	if reference == "" {
		return "", fmt.Errorf("agent path reference must not be empty")
	}
	if strings.HasPrefix(reference, "/") {
		return ParseAgentPath(reference)
	}
	if current == "" {
		current = RootAgentPath()
	}
	if _, err := ParseAgentPath(string(current)); err != nil {
		return "", err
	}
	if strings.HasSuffix(reference, "/") {
		return "", fmt.Errorf("relative agent path must not end with /")
	}
	path := current
	for _, part := range strings.Split(reference, "/") {
		next, err := JoinAgentPath(path, part)
		if err != nil {
			return "", err
		}
		path = next
	}
	return path, nil
}

func ValidateAgentName(name string) error {
	if name == "" {
		return fmt.Errorf("agent name must not be empty")
	}
	if name == rootName {
		return fmt.Errorf("agent name root is reserved")
	}
	if name == "." || name == ".." {
		return fmt.Errorf("agent name %q is reserved", name)
	}
	if strings.Contains(name, "/") {
		return fmt.Errorf("agent name must not contain /")
	}
	for _, ch := range name {
		if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '_' {
			continue
		}
		return fmt.Errorf("agent name must use only lowercase letters, digits, and underscores")
	}
	return nil
}
