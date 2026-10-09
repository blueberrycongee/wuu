package plugin

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestManifestDependencyContract(t *testing.T) {
	path := filepath.Join(t.TempDir(), ManifestFilename)
	writeFile(t, path, `{"id":"consumer","requires":["legacy"],"dependencies":[{"id":"provider","version":"^1.2.0"},{"id":"extra","optional":true}]}`)
	p, err := LoadManifest(path, "project")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.Requires, []string{"legacy", "provider"}) || len(p.Dependencies) != 2 {
		t.Fatalf("dependencies lost: %+v", p)
	}
	for _, value := range []string{`[{"id":"consumer"}]`, `[{"id":"other","version":"banana"}]`, `[{"id":"other"},{"id":"other"}]`, `[{"id":"legacy","optional":true}]`, `[{"id":"other","unknown":true}]`} {
		writeFile(t, path, `{"id":"consumer","requires":["legacy"],"dependencies":`+value+`}`)
		if _, err := LoadManifest(path, "project"); err == nil {
			t.Fatalf("accepted invalid dependencies %s", value)
		}
	}
}

func TestDependencyVersionContract(t *testing.T) {
	for _, tt := range []struct {
		version, constraint string
		want                bool
	}{
		{"1.2.3", "^1.2.0", true}, {"2.0.0", "^1.2.0", false}, {"0.2.9", "^0.2.0", true}, {"0.3.0", "^0.2.0", false}, {"0.0.4", "^0.0.3", false},
		{"1.3.0", "~1.2.0", false}, {"1.2.5", "~1.2.0", true}, {"1.5.0", ">=1.2.0 <2.0.0", true}, {"1.2.3+build", "=1.2.3", true},
		{"1.3.0-beta.1", ">=1.2.0", false}, {"1.3.0-beta.2", ">=1.3.0-beta.1 <1.3.0", true}, {"", "^1.2.0", false}, {"garbage", "", true},
	} {
		got, err := matchesDependencyVersion(tt.version, tt.constraint)
		if err != nil || got != tt.want {
			t.Errorf("%q %q = %v, %v; want %v", tt.version, tt.constraint, got, err, tt.want)
		}
	}
}

func TestActivationDependencyContract(t *testing.T) {
	provider := Plugin{Manifest: Manifest{ID: "provider", Version: "1.5.0"}}
	consumer := Plugin{Manifest: Manifest{ID: "consumer", Dependencies: []Dependency{{ID: "provider", Version: "^1.0.0"}}}}
	plan, err := BuildActivationPlan([]Plugin{consumer, provider})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Plugins) != 2 || plan.Plugins[0].ID != "provider" {
		t.Fatalf("provider must activate first: %+v", plan)
	}
	provider.Version = "2.0.0"
	plan, err = BuildActivationPlan([]Plugin{consumer, provider, {Manifest: Manifest{ID: "downstream", Requires: []string{"consumer"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Plugins) != 1 || len(plan.Issues["consumer"]) != 1 || len(plan.Issues["downstream"]) != 1 {
		t.Fatalf("bad version must cascade: %+v", plan)
	}
	consumer.Dependencies[0].Optional = true
	for _, providers := range [][]Plugin{nil, {provider}, {{Manifest: Manifest{ID: "provider", Version: "1.5.0"}}}} {
		plan, err = BuildActivationPlan(append(providers, consumer))
		if err != nil || len(plan.Plugins) != len(providers)+1 {
			t.Fatalf("optional must not block: %+v, %v", plan, err)
		}
		if len(providers) > 0 && providers[0].Version == "1.5.0" && plan.Plugins[0].ID != "provider" {
			t.Fatal("matching optional provider must activate first")
		}
	}
	consumer.Dependencies = []Dependency{{ID: "provider"}}
	provider.Requires = []string{"consumer"}
	if _, err = BuildActivationPlan([]Plugin{consumer, provider}); err == nil {
		t.Fatal("expected dependency cycle rejection")
	}
}

func TestOptionalDependencyCycleDoesNotBlockActivation(t *testing.T) {
	plan, err := BuildActivationPlan([]Plugin{
		{Manifest: Manifest{ID: "a", Dependencies: []Dependency{{ID: "b", Optional: true}}}},
		{Manifest: Manifest{ID: "b", Requires: []string{"a"}}},
	})
	if err != nil || len(plan.Plugins) != 2 {
		t.Fatalf("optional cycle must not block: %+v %v", plan, err)
	}
	if len(plan.Dependencies["a"]) != 0 || len(plan.Issues["a"]) != 1 {
		t.Fatalf("ignored optional edge needs diagnostic: %+v", plan)
	}
}

func TestDependencyConstraintChangesFingerprint(t *testing.T) {
	p := Plugin{Manifest: Manifest{ID: "consumer", Dependencies: []Dependency{{ID: "provider", Version: "^1.0"}}}}
	before, err := p.PackageContract()
	if err != nil {
		t.Fatal(err)
	}
	p.Dependencies[0].Version = "^2.0"
	after, err := p.PackageContract()
	if err != nil {
		t.Fatal(err)
	}
	if before.Fingerprint == after.Fingerprint {
		t.Fatal("dependency constraint omitted from package identity")
	}
}
