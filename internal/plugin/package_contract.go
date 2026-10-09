package plugin

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/blueberrycongee/wuu/internal/extensions"
)

// PackageContract is the stable policy identity calculated from one discovered
// package. It contains no secret values.
type PackageContract struct {
	SubjectID   string
	Fingerprint string
	Permissions []string
}

// PackageContract calculates the aggregate package fingerprint and permission
// union used by inventory, approval, and activation.
func (p Plugin) PackageContract() (PackageContract, error) {
	source := strings.TrimSpace(p.Source)
	if source == "" {
		source = "user"
	}
	spec := extensions.PackageSpec{
		ID:                   p.ID,
		Source:               source,
		Scope:                source,
		Official:             p.Official,
		Name:                 p.Name,
		Description:          p.Description,
		Version:              p.Version,
		Keywords:             append([]string(nil), p.Keywords...),
		Skills:               append([]string(nil), p.Skills...),
		RequestedPermissions: append([]string(nil), p.RequestedPermissions...),
		ActivityKinds:        append([]string(nil), p.ActivityKinds...),
		MinimumWuuVersion:    p.MinimumWuuVersion,
		Requires:             p.RequiredPluginIDs(),
		Dependencies:         append([]Dependency(nil), p.Dependencies...),
		Breaks:               append([]string(nil), p.Breaks...),
		Conflicts:            append([]string(nil), p.Conflicts...),
	}
	if p.Runtime != nil {
		command := p.Runtime.Command
		if p.RuntimePath != "" {
			command = p.RuntimePath
		}
		spec.Runtime = &extensions.RuntimeSpec{
			Protocol: p.Runtime.Protocol,
			Command:  command,
			Args:     append([]string(nil), p.Runtime.Args...),
			Timeout:  p.Runtime.Timeout,
			EnvNames: sortedKeys(p.Runtime.Env),
		}
	}
	if len(p.Hooks) > 0 {
		spec.Hooks = make(map[string][]extensions.HookEntry, len(p.Hooks))
		for event, entries := range p.Hooks {
			converted := make([]extensions.HookEntry, 0, len(entries))
			for _, entry := range entries {
				converted = append(converted, extensions.HookEntry{
					Matcher: entry.Matcher,
					Type:    entry.Type,
					Command: entry.Command,
					Prompt:  entry.Prompt,
					Model:   entry.Model,
					Timeout: entry.Timeout,
				})
			}
			spec.Hooks[event] = converted
		}
	}
	if len(p.MCPServers) > 0 {
		spec.MCPServers = make(map[string]extensions.MCPServerSpec, len(p.MCPServers))
		for name, server := range p.MCPServers {
			spec.MCPServers[name] = extensions.MCPServerSpec{
				Command:     server.Command,
				Args:        append([]string(nil), server.Args...),
				URL:         server.URL,
				Transport:   server.Transport,
				EnvNames:    sortedKeys(server.Env),
				HeaderNames: sortedKeys(server.Headers),
			}
		}
	}
	for _, command := range p.Commands {
		spec.Commands = append(spec.Commands, extensions.CommandSpec{
			ID:          command.ID,
			PublicID:    command.PublicID,
			Title:       command.Title,
			Description: command.Description,
			Kind:        extensions.CommandKind(command.Kind),
			Prompt:      command.Prompt,
			Contexts:    append([]string(nil), command.Contexts...),
			Aliases:     append([]string(nil), command.Aliases...),
			Keywords:    append([]string(nil), command.Keywords...),
		})
	}

	var entryHashes map[string]string
	if strings.TrimSpace(p.Root) != "" {
		var err error
		entryHashes, err = hashPackageEntries(p.Root, []string{"."})
		if err != nil {
			return PackageContract{}, err
		}
	}
	spec.EntryHashes = entryHashes
	spec.RequestedPermissions = extensions.RequestedPermissionUnion(spec)
	fingerprint, err := extensions.ComputeFingerprint(spec)
	if err != nil {
		return PackageContract{}, err
	}
	subjectID := extensions.SubjectID(source, p.ID)
	if source == "project" && strings.TrimSpace(p.WorkspaceID) != "" {
		subjectID = extensions.SubjectID(source, strings.TrimSpace(p.WorkspaceID)+":"+p.ID)
	}
	return PackageContract{
		SubjectID:   subjectID,
		Fingerprint: fingerprint,
		Permissions: append([]string(nil), spec.RequestedPermissions...),
	}, nil
}

func hashPackageEntries(root string, paths []string) (map[string]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	unique := make(map[string]struct{}, len(paths))
	for _, rel := range paths {
		rel = filepath.Clean(strings.TrimSpace(rel))
		if rel == "" {
			continue
		}
		unique[rel] = struct{}{}
	}
	ordered := make([]string, 0, len(unique))
	for rel := range unique {
		ordered = append(ordered, rel)
	}
	sort.Strings(ordered)
	out := make(map[string]string, len(ordered))
	for _, rel := range ordered {
		fullPath := filepath.Join(root, rel)
		info, err := os.Stat(fullPath)
		if os.IsNotExist(err) {
			// Keep discovery compatible with packages whose generated/runtime entry
			// is materialized at activation time. The normalized path declaration
			// remains covered by PackageSpec even when no content hash is available.
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("hash plugin entry %s: %w", rel, err)
		}
		if info.IsDir() {
			count := 0
			err := filepath.WalkDir(fullPath, func(path string, entry os.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if entry.IsDir() {
					return nil
				}
				count++
				if count > 10_000 {
					return fmt.Errorf("plugin entry tree exceeds 10000 files")
				}
				if err := ensureResolvedPathWithinRoot(root, path); err != nil {
					return err
				}
				entryInfo, err := os.Stat(path)
				if err != nil {
					return err
				}
				if !entryInfo.Mode().IsRegular() {
					return nil
				}
				entryRel, err := filepath.Rel(root, path)
				if err != nil {
					return err
				}
				digest, err := hashFile(path)
				if err != nil {
					return err
				}
				out[entryRel] = digest
				return nil
			})
			if err != nil {
				return nil, fmt.Errorf("hash plugin entry tree %s: %w", rel, err)
			}
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		digest, err := hashFile(fullPath)
		if err != nil {
			return nil, fmt.Errorf("hash plugin entry %s: %w", rel, err)
		}
		out[rel] = digest
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func sortedKeys[V any](values map[string]V) []string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, strings.TrimSpace(key))
	}
	sort.Strings(keys)
	return keys
}
