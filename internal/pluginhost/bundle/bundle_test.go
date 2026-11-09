package bundle

import (
	"strings"
	"testing"
)

func testFixtures() []struct {
	name         string
	manifestJSON string
	content      map[string]string
	golden       string
} {
	return []struct {
		name         string
		manifestJSON string
		content      map[string]string
		golden       string
	}{
		{
			name: "full",
			manifestJSON: `{
  "schema_version": 2,
  "id": "com.example.image",
  "version": "1.2.3",
  "name": "Image Gen",
  "description": "Generates images.",
  "agent": {
    "command": "dist/agent",
    "args": ["--serve", "--port", "9000"],
    "env": { "WUU_MODE": "desktop" }
  },
  "desktop": { "entry": "dist/desktop.mjs" }
}`,
			content: map[string]string{
				"dist/agent":       "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				"dist/desktop.mjs": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			},
			golden: "8dbc76c3e01ddc5b80ec1b381a3230dc6119bdeec7b49d770e506df9817cbc0e",
		},
		{
			name: "agent-only",
			manifestJSON: `{
  "schema_version": 2,
  "id": "com.example.headless",
  "version": "0.2.0",
  "agent": { "command": "bin/agent" }
}`,
			content: map[string]string{
				"bin/agent": "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
			},
			golden: "8aba89d1c5e972a9818627b4f2d7549ec7ec23ee80710e9ea5bb177f8e17c64c",
		},
		{
			name: "desktop-only",
			manifestJSON: `{
  "schema_version": 2,
  "id": "com.example.theme",
  "version": "0.3.0",
  "desktop": { "entry": "index.mjs" }
}`,
			content: map[string]string{
				"index.mjs": "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
			},
			golden: "5600e1d7abdf8e8c806ab2e8215b015db43b8e13adeb1d1e2c21a54dc98e17f8",
		},
		{
			name:         "escaping",
			manifestJSON: "{\n  \"schema_version\": 2,\n  \"id\": \"com.example.escape\",\n  \"version\": \"0.1.0\",\n  \"description\": \"a<b>&\\\"c\\\"\\\\d\\ne\\tf 图 🎨\",\n  \"agent\": { \"command\": \"bin/agent\" }\n}",
			content: map[string]string{
				"bin/agent": "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
			},
			golden: "346410388be78541f0ee60d292d5e4a7a7e9a9b8c714e2efb6b0825989516db0",
		},
	}
}

func TestGenerationGolden(t *testing.T) {
	for _, tc := range testFixtures() {
		t.Run(tc.name, func(t *testing.T) {
			value, err := ParseManifestValue([]byte(tc.manifestJSON))
			if err != nil {
				t.Fatalf("parse manifest: %v", err)
			}
			got, err := Generation(GenerationInput{
				ContractVersion: ContractVersion,
				Manifest:        value,
				Content:         tc.content,
			})
			if err != nil {
				t.Fatalf("generation: %v", err)
			}
			if got != tc.golden {
				t.Fatalf("generation mismatch:\n got  %s\n want %s", got, tc.golden)
			}
		})
	}
}

func TestParseAndValidate(t *testing.T) {
	valid := []string{
		`{"schema_version":2,"id":"a.b","version":"1.0.0","agent":{"command":"bin/agent"}}`,
		`{"schema_version":2,"id":"a.b","version":"1.0.0","desktop":{"entry":"index.mjs"}}`,
	}
	for _, raw := range valid {
		if _, _, err := Parse([]byte(raw)); err != nil {
			t.Fatalf("valid manifest rejected: %v\n%s", err, raw)
		}
	}

	invalid := []struct {
		name string
		raw  string
		want string
	}{
		{"v1 schema", `{"schema_version":1,"id":"a.b","version":"1.0.0","agent":{"command":"bin/agent"}}`, "schema_version must be 2"},
		{"missing id", `{"schema_version":2,"version":"1.0.0","agent":{"command":"bin/agent"}}`, "manifest.id is required"},
		{"missing version", `{"schema_version":2,"id":"a.b","agent":{"command":"bin/agent"}}`, "manifest.version is required"},
		{"no surface", `{"schema_version":2,"id":"a.b","version":"1.0.0"}`, "at least one of agent or desktop"},
		{"agent missing command", `{"schema_version":2,"id":"a.b","version":"1.0.0","agent":{}}`, "manifest.agent.command is required"},
		{"desktop absolute entry", `{"schema_version":2,"id":"a.b","version":"1.0.0","desktop":{"entry":"/index.mjs"}}`, "package-relative"},
		{"desktop escaping entry", `{"schema_version":2,"id":"a.b","version":"1.0.0","desktop":{"entry":"../index.mjs"}}`, "must not escape"},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := Parse([]byte(tc.raw))
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not contain %q", err, tc.want)
			}
		})
	}
}

func TestCanonicalizeStable(t *testing.T) {
	value := map[string]any{
		"b": map[string]any{"x": "1", "a": ""},
		"a": []any{"z", "a<b>&\"c\""},
	}
	got, err := Canonicalize(value)
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}
	want := `{"a":["z","a<b>&\"c\""],"b":{"x":"1"}}`
	if string(got) != want {
		t.Fatalf("canonical bytes mismatch:\n got  %s\n want %s", got, want)
	}
}
