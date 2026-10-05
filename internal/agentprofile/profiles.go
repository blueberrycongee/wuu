package agentprofile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/securefs"
	"github.com/blueberrycongee/wuu/internal/statepath"
)

const profileMetadataFile = "profile.json"

type EnsureOptions struct {
	WuuHome     string
	Name        string
	Role        string
	Description string
}

type Summary struct {
	Name           string    `json:"name"`
	Role           string    `json:"role,omitempty"`
	Description    string    `json:"description,omitempty"`
	ProfileDir     string    `json:"profile_dir"`
	CreatedAt      time.Time `json:"created_at,omitempty"`
	LastResolvedAt time.Time `json:"last_resolved_at,omitempty"`
}

type profileMetadata struct {
	Name           string    `json:"name"`
	Role           string    `json:"role,omitempty"`
	Description    string    `json:"description,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	LastResolvedAt time.Time `json:"last_resolved_at"`
}

func List(wuuHome string) ([]Summary, error) {
	if strings.TrimSpace(wuuHome) == "" {
		return nil, fmt.Errorf("wuu home is required")
	}
	root := filepath.Join(wuuHome, "profiles")
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]Summary, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		meta, err := readProfileMetadata(filepath.Join(dir, profileMetadataFile))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		summary := summaryFromMetadata(meta, dir)
		if strings.TrimSpace(summary.Name) == "" {
			continue
		}
		out = append(out, summary)
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

func Ensure(opts EnsureOptions) (Summary, bool, error) {
	wuuHome := strings.TrimSpace(opts.WuuHome)
	if wuuHome == "" {
		return Summary{}, false, fmt.Errorf("wuu home is required")
	}
	name := strings.TrimSpace(opts.Name)
	if name == "" || strings.EqualFold(name, "default") {
		return Summary{}, false, fmt.Errorf("profile name must be a non-default named agent")
	}
	dir, err := statepath.ProfileDir(wuuHome, name)
	if err != nil {
		return Summary{}, false, err
	}
	if err := os.MkdirAll(dir, securefs.DirMode); err != nil {
		return Summary{}, false, err
	}

	path := filepath.Join(dir, profileMetadataFile)
	now := time.Now().UTC()
	meta, err := readProfileMetadata(path)
	created := false
	if os.IsNotExist(err) {
		created = true
		meta = profileMetadata{
			Name:      name,
			CreatedAt: now,
		}
	} else if err != nil {
		return Summary{}, false, err
	}
	if strings.TrimSpace(meta.Name) == "" {
		meta.Name = name
	}
	if role := strings.TrimSpace(opts.Role); role != "" {
		meta.Role = role
	}
	if desc := strings.TrimSpace(opts.Description); desc != "" {
		meta.Description = desc
	}
	if meta.CreatedAt.IsZero() {
		meta.CreatedAt = now
	}
	meta.LastResolvedAt = now
	if err := writeProfileMetadata(path, meta); err != nil {
		return Summary{}, false, err
	}
	return summaryFromMetadata(meta, dir), created, nil
}

func readProfileMetadata(path string) (profileMetadata, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return profileMetadata{}, err
	}
	var meta profileMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return profileMetadata{}, fmt.Errorf("parse profile metadata %s: %w", path, err)
	}
	return meta, nil
}

func writeProfileMetadata(path string, metadata profileMetadata) error {
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), securefs.FileMode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func summaryFromMetadata(meta profileMetadata, dir string) Summary {
	return Summary{
		Name:           strings.TrimSpace(meta.Name),
		Role:           strings.TrimSpace(meta.Role),
		Description:    strings.TrimSpace(meta.Description),
		ProfileDir:     dir,
		CreatedAt:      meta.CreatedAt,
		LastResolvedAt: meta.LastResolvedAt,
	}
}
