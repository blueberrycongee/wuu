package version

import (
	"fmt"
	"runtime/debug"
	"strings"
)

const defaultVersion = "v0.1.0-dev"

// Set via ldflags at build time:
//
//	go build -ldflags "-X github.com/blueberrycongee/wuu/internal/version.Version=v0.1.0"
var (
	Version = defaultVersion
	Commit  = "none"
	Date    = "unknown"
)

// readBuildInfo is overridden in tests.
var readBuildInfo = debug.ReadBuildInfo

// BuildInfo is the resolved version metadata at runtime.
type BuildInfo struct {
	Version     string `json:"version"`
	Commit      string `json:"commit"`
	Date        string `json:"date"`
	Dirty       bool   `json:"dirty"`
	VCSRevision string `json:"vcs_revision,omitempty"`
}

// Info returns resolved build metadata from ldflags + Go build info.
func Info() BuildInfo {
	out := BuildInfo{
		Version: normalizeVersion(Version),
		Commit:  strings.TrimSpace(Commit),
		Date:    strings.TrimSpace(Date),
	}
	if out.Commit == "" {
		out.Commit = "none"
	}
	if out.Date == "" {
		out.Date = "unknown"
	}

	if bi, ok := readBuildInfo(); ok && bi != nil {
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				out.VCSRevision = s.Value
			case "vcs.time":
				if out.Date == "unknown" && strings.TrimSpace(s.Value) != "" {
					out.Date = s.Value
				}
			case "vcs.modified":
				out.Dirty = s.Value == "true"
			}
		}
	}

	if (out.Commit == "" || out.Commit == "none") && out.VCSRevision != "" {
		out.Commit = shortCommit(out.VCSRevision)
	}

	return out
}

// String returns a human-readable version string.
func String() string {
	return Info().String()
}

// String returns a human-readable version string.
func (b BuildInfo) String() string {
	version := normalizeVersion(b.Version)

	commit := b.Commit
	if commit == "" {
		commit = "none"
	}
	if b.Dirty && commit != "none" {
		commit += "-dirty"
	}

	if commit == "none" {
		return fmt.Sprintf("wuu %s (built from source)", version)
	}
	if b.Date == "" || b.Date == "unknown" {
		return fmt.Sprintf("wuu %s (%s)", version, commit)
	}
	return fmt.Sprintf("wuu %s (%s %s)", version, commit, b.Date)
}

func normalizeVersion(v string) string {
	trimmed := strings.TrimSpace(v)
	if trimmed == "" || trimmed == "dev" {
		return defaultVersion
	}
	if strings.HasPrefix(trimmed, "v") {
		return trimmed
	}
	if trimmed[0] >= '0' && trimmed[0] <= '9' {
		return "v" + trimmed
	}
	return trimmed
}

// LongString returns a multi-line detailed version output.
func (b BuildInfo) LongString() string {
	var lines []string
	lines = append(lines, fmt.Sprintf("version: %s", normalizeVersion(b.Version)))
	lines = append(lines, fmt.Sprintf("commit: %s", b.Commit))
	lines = append(lines, fmt.Sprintf("date: %s", b.Date))
	lines = append(lines, fmt.Sprintf("dirty: %t", b.Dirty))
	if b.VCSRevision != "" {
		lines = append(lines, fmt.Sprintf("vcs_revision: %s", b.VCSRevision))
	}
	return strings.Join(lines, "\n")
}

func shortCommit(rev string) string {
	if len(rev) <= 7 {
		return rev
	}
	return rev[:7]
}
