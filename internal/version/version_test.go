package version

import (
	"runtime/debug"
	"strings"
	"testing"
)

func TestVersion_DefaultsToDevelopmentVersion(t *testing.T) {
	if Version != defaultVersion {
		t.Fatalf("unexpected default Version: got %q want %q", Version, defaultVersion)
	}
}

func TestInfo_VersionResolution(t *testing.T) {
	tests := []struct {
		name          string
		linkedVersion string
		moduleVersion string
		want          string
	}{
		{
			name:          "ldflags take priority over module version",
			linkedVersion: "2.0.0-dev",
			moduleVersion: "v1.2.3",
			want:          "v2.0.0-dev",
		},
		{
			name:          "module semver is used without ldflags",
			linkedVersion: defaultVersion,
			moduleVersion: "v1.2.3-beta.1+build.7",
			want:          "v1.2.3-beta.1+build.7",
		},
		{
			name:          "development checkout keeps dev version",
			linkedVersion: defaultVersion,
			moduleVersion: "(devel)",
			want:          defaultVersion,
		},
		{
			name:          "invalid module version keeps dev version",
			linkedVersion: defaultVersion,
			moduleVersion: "v1.2",
			want:          defaultVersion,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			origVersion, origCommit, origDate := Version, Commit, Date
			origRead := readBuildInfo
			t.Cleanup(func() {
				Version, Commit, Date = origVersion, origCommit, origDate
				readBuildInfo = origRead
			})

			Version = tt.linkedVersion
			Commit = "none"
			Date = "unknown"
			readBuildInfo = func() (*debug.BuildInfo, bool) {
				return &debug.BuildInfo{
					Main: debug.Module{Version: tt.moduleVersion},
				}, true
			}

			if got := Info().Version; got != tt.want {
				t.Fatalf("unexpected resolved version: got %q want %q", got, tt.want)
			}
		})
	}
}

func TestInfo_FallsBackToBuildSettings(t *testing.T) {
	origVersion, origCommit, origDate := Version, Commit, Date
	origRead := readBuildInfo
	t.Cleanup(func() {
		Version, Commit, Date = origVersion, origCommit, origDate
		readBuildInfo = origRead
	})

	Version = "dev"
	Commit = "none"
	Date = "unknown"
	readBuildInfo = func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{
			Settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: "abcdef1234567890"},
				{Key: "vcs.time", Value: "2026-04-08T12:00:00Z"},
				{Key: "vcs.modified", Value: "true"},
			},
		}, true
	}

	info := Info()
	if info.Commit != "abcdef1" {
		t.Fatalf("expected short commit from vcs.revision, got %q", info.Commit)
	}
	if info.Date != "2026-04-08T12:00:00Z" {
		t.Fatalf("expected date from vcs.time, got %q", info.Date)
	}
	if !info.Dirty {
		t.Fatal("expected dirty=true from vcs.modified")
	}
	if info.VCSRevision != "abcdef1234567890" {
		t.Fatalf("unexpected vcs revision: %q", info.VCSRevision)
	}

	got := info.String()
	want := "wuu v0.1.0-dev (abcdef1-dirty 2026-04-08T12:00:00Z)"
	if got != want {
		t.Fatalf("unexpected String output: got %q want %q", got, want)
	}
}

func TestInfo_ReleaseFormatting(t *testing.T) {
	origVersion, origCommit, origDate := Version, Commit, Date
	origRead := readBuildInfo
	t.Cleanup(func() {
		Version, Commit, Date = origVersion, origCommit, origDate
		readBuildInfo = origRead
	})

	Version = "1.2.3"
	Commit = "1234567"
	Date = "2026-04-08T00:00:00Z"
	readBuildInfo = func() (*debug.BuildInfo, bool) {
		return nil, false
	}

	if got, want := String(), "wuu v1.2.3 (1234567 2026-04-08T00:00:00Z)"; got != want {
		t.Fatalf("unexpected String output: got %q want %q", got, want)
	}

	long := Info().LongString()
	for _, needle := range []string{
		"version: v1.2.3",
		"commit: 1234567",
		"date: 2026-04-08T00:00:00Z",
		"dirty: false",
	} {
		if !strings.Contains(long, needle) {
			t.Fatalf("LongString missing %q: %q", needle, long)
		}
	}
}

func TestInfo_BuiltFromSourceFallback(t *testing.T) {
	origVersion, origCommit, origDate := Version, Commit, Date
	origRead := readBuildInfo
	t.Cleanup(func() {
		Version, Commit, Date = origVersion, origCommit, origDate
		readBuildInfo = origRead
	})

	Version = ""
	Commit = "none"
	Date = "unknown"
	readBuildInfo = func() (*debug.BuildInfo, bool) {
		return nil, false
	}

	if got, want := String(), "wuu v0.1.0-dev (built from source)"; got != want {
		t.Fatalf("unexpected fallback output: got %q want %q", got, want)
	}
}
