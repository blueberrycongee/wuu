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
	linkedVersion := strings.TrimSpace(Version)
	out := BuildInfo{
		Version: normalizeVersion(linkedVersion),
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
		if linkedVersion == defaultVersion && isModuleSemver(bi.Main.Version) {
			out.Version = normalizeVersion(bi.Main.Version)
		}
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

// isModuleSemver reports whether v is a valid Go module semantic version.
// Module versions include the leading "v" required by the Go toolchain.
func isModuleSemver(v string) bool {
	if !strings.HasPrefix(v, "v") {
		return false
	}

	version := v[1:]
	coreAndPrerelease, build, hasBuild := strings.Cut(version, "+")
	if hasBuild && !validSemverIdentifiers(build, false) {
		return false
	}

	core, prerelease, hasPrerelease := strings.Cut(coreAndPrerelease, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if !validSemverNumber(part) {
			return false
		}
	}
	return !hasPrerelease || validSemverIdentifiers(prerelease, true)
}

func validSemverNumber(value string) bool {
	if value == "" || (len(value) > 1 && value[0] == '0') {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func validSemverIdentifiers(value string, rejectLeadingZeroNumbers bool) bool {
	for _, identifier := range strings.Split(value, ".") {
		if identifier == "" {
			return false
		}
		numeric := true
		for _, r := range identifier {
			if (r < '0' || r > '9') && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') && r != '-' {
				return false
			}
			if r < '0' || r > '9' {
				numeric = false
			}
		}
		if rejectLeadingZeroNumbers && numeric && len(identifier) > 1 && identifier[0] == '0' {
			return false
		}
	}
	return true
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
