package enginecatalog

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestGrokLaunchAvoidsSharedLeaderAndUpdateCheck(t *testing.T) {
	entry, ok := Lookup("grok")
	if !ok {
		t.Fatal("grok catalog entry missing")
	}
	want := []string{"--no-auto-update", "agent", "--no-leader", "stdio"}
	if !slices.Equal(entry.Args, want) {
		t.Fatalf("grok args = %q, want %q", entry.Args, want)
	}
	found := false
	for _, path := range extraLookupPaths(entry) {
		if strings.HasSuffix(filepath.ToSlash(path), "/.grok/bin/grok") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("extra grok paths = %q, want ~/.grok/bin/grok", extraLookupPaths(entry))
	}
}

func TestExtraLookupPathsCoverNativeACPInstallDirs(t *testing.T) {
	cases := map[string][]string{
		"hermes": {".hermes/bin/hermes"},
		"devin":  {".local/bin/devin"},
		"pi":     {".npm-global/bin/pi-acp"},
	}
	for id, suffixes := range cases {
		entry, ok := Lookup(id)
		if !ok {
			t.Fatalf("%s catalog entry missing", id)
		}
		paths := extraLookupPaths(entry)
		for _, suffix := range suffixes {
			found := false
			for _, path := range paths {
				if strings.HasSuffix(filepath.ToSlash(path), "/"+suffix) {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("%s extra paths = %q, want %s", id, paths, suffix)
			}
		}
	}
}
